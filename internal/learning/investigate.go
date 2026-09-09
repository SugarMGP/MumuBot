package learning

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/memory"
	agenttools "mumu-bot/internal/tools"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

type investigation struct {
	manager   *memory.Manager
	batch     memory.KnowledgeBatch
	seen      map[uint]bool
	partial   map[uint]int
	required  []uint
	textChars int
	finished  bool
	rows      []memory.MessageLog
	topics    memory.ConversationContext
}

type finishInput struct {
	Topics      []memory.ConversationTopic      `json:"topics"`
	NoTopicIDs  []uint                          `json:"no_topic_ids"`
	Items       []memory.KnowledgeItemInput     `json:"items"`
	Relations   []memory.KnowledgeRelationInput `json:"relations"`
	ReviewedIDs []uint                          `json:"reviewed_ids"`
}
type searchInput struct {
	Query         string `json:"query"`
	SubjectUserID *int64 `json:"subject_user_id,omitempty"`
	Offset        int    `json:"offset,omitempty"`
	ItemID        uint   `json:"item_id,omitempty" jsonschema:"description=按知识 ID 读取正文、证据和关系，包括关系另一端；offset 同时分页证据组和关系"`
}
type messageInput struct {
	Text             string     `json:"text"`
	UserID           int64      `json:"user_id,omitempty"`
	AfterID          uint       `json:"after_id,omitempty"`
	ReplyToMessageID *int64     `json:"reply_to_message_id,omitempty"`
	From             *time.Time `json:"from,omitempty"`
	To               *time.Time `json:"to,omitempty"`
}
type contextInput struct {
	MessageID uint   `json:"message_id"`
	Mode      string `json:"mode" jsonschema:"enum=message,enum=replies,enum=topic,enum=window"`
	AfterID   uint   `json:"after_id,omitempty"`
	Offset    int    `json:"offset,omitempty" jsonschema:"description=仅 message 模式，续读长原文的字符位置"`
}

func (l *Learner) investigate(groupID, selfID int64, after, upper uint, advance bool, rows []memory.MessageLog, candidates []memory.KnowledgeItem) error {
	ctx, cancel := context.WithTimeout(l.ctx, time.Duration(config.Get().Learning.TimeoutSeconds)*time.Second)
	defer cancel()
	ctx = llm.WithTask(ctx, "memory_agent", config.Get().ModelTiers.Low.Model)
	run := &investigation{manager: l.memMgr, batch: memory.KnowledgeBatch{GroupID: groupID, SelfID: selfID, AfterID: after, ThroughID: upper, AdvanceCursor: advance, RequireAssigned: true, ExpectedItems: make(map[uint]time.Time)}, seen: make(map[uint]bool), partial: make(map[uint]int)}
	run.rows = rows
	var err error
	if len(rows) > 0 {
		run.topics, err = l.memMgr.ConversationContext(ctx, groupID, upper, rows)
		if err != nil {
			return err
		}
	}
	validRows := []memory.MessageLog{}
	for _, row := range rows {
		if row.RecalledAt != nil || row.TextContent == "" {
			continue
		}
		run.required = append(run.required, row.ID)
		validRows = append(validRows, row)
	}
	initial, err := run.renderMessages(memory.KnowledgeMessagePage{Messages: validRows}, 0)
	if err != nil {
		return err
	}
	if len(validRows) == 0 && len(candidates) == 0 {
		_, err := run.finish(ctx, &finishInput{})
		return err
	}
	if len(validRows) > 0 {
		page, err := l.memMgr.ReadKnowledgeContext(ctx, groupID, upper, validRows[0].ID, 0, "window")
		if err != nil {
			return err
		}
		previous := []memory.MessageLog{}
		for _, row := range page.Messages {
			if row.ID < validRows[0].ID {
				previous = append(previous, row)
			}
		}
		history, err := run.renderMessages(memory.KnowledgeMessagePage{Messages: previous}, 0)
		if err != nil {
			return err
		}
		initial += "\n仅供上下文，不重新分配：" + history
	}
	for _, item := range candidates {
		run.batch.ExpectedItems[item.ID] = item.UpdatedAt
	}
	candidateText, err := sonic.MarshalString(candidates)
	if err != nil {
		return err
	}
	available, err := run.tools()
	if err != nil {
		return err
	}
	messages := []*schema.Message{schema.SystemMessage(memoryPrompt), schema.UserMessage(fmt.Sprintf("群 %d，机器人 %d，固定内部消息范围 (%d,%d]。本批需完整读取的原文 ID：%v\n原文：%s\n待复核候选：%s", groupID, selfID, after, upper, run.required, initial, candidateText))}
	topicText, err := sonic.MarshalString(run.topics)
	if err != nil {
		return err
	}
	messages = append(messages, schema.UserMessage("已有话题及原归属："+topicText))
	run.textChars = utf8.RuneCountInString(initial) + utf8.RuneCountInString(candidateText) + utf8.RuneCountInString(topicText)
	if run.textChars > 24000 {
		return fmt.Errorf("整理输入超出预算")
	}
	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel:   l.model,
		ToolsConfig:        compose.ToolsNodeConfig{Tools: available, ExecuteSequentially: true, ToolCallMiddlewares: []compose.ToolMiddleware{{Invokable: agenttools.ToolDedupMiddleware()}}},
		MaxStep:            config.Get().Learning.MaxStep,
		ToolReturnDirectly: map[string]struct{}{"finishMemoryBatch": {}},
	})
	if err != nil {
		return err
	}
	if _, err := agent.Generate(ctx, messages); err != nil {
		return err
	}
	if !run.finished {
		return noFinishError()
	}
	return nil
}

func (r *investigation) tools() ([]tool.BaseTool, error) {
	a, err := utils.InferTool("searchKnowledge", "查本群知识，包含候选和历史状态；offset 分页。", r.search)
	if err != nil {
		return nil, err
	}
	b, err := utils.InferTool("searchMessages", "搜索已处理历史原文；after_id 使用上页 next_id。", r.searchMessages)
	if err != nil {
		return nil, err
	}
	c, err := utils.InferTool("readContext", "按内部消息 ID 阅读原文、回复双方、附近窗口或话题；长原文用 offset 续读。", r.readContext)
	if err != nil {
		return nil, err
	}
	d, err := utils.InferTool("finishMemoryBatch", "调查结束时单独调用，原子提交知识、关系、完整证据组和复核记录；无结果也需调用。", r.finish)
	if err != nil {
		return nil, err
	}
	e, err := utils.InferTool("searchTopics", "按关键词查本群历史话题，最多6项；仅在近期话题不足以解释当前消息时使用。", r.searchTopics)
	if err != nil {
		return nil, err
	}
	return []tool.BaseTool{a, b, c, d, e}, nil
}

func (r *investigation) searchTopics(ctx context.Context, input *struct {
	Query string `json:"query"`
}) (string, error) {
	items, err := r.manager.SearchConversationTopics(ctx, r.batch.GroupID, r.batch.ThroughID, input.Query)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		found := false
		for i, old := range r.topics.Topics {
			if old.ID == item.ID {
				r.topics.Topics[i] = item
				found = true
				break
			}
		}
		if !found {
			r.topics.Topics = append(r.topics.Topics, item)
		}
	}
	return sonic.MarshalString(items)
}

func (r *investigation) search(ctx context.Context, input *searchInput) (string, error) {
	if input.Offset < 0 {
		return "", fmt.Errorf("offset 不得为负数")
	}
	if input.ItemID != 0 {
		items, err := r.manager.SearchKnowledge(ctx, memory.KnowledgeSearchOptions{GroupID: r.batch.GroupID, ItemID: input.ItemID, IncludeInactive: true, ThroughID: r.batch.ThroughID, Limit: 1})
		if err != nil {
			return "", err
		}
		if len(items) != 1 {
			return "", fmt.Errorf("知识不存在或不在本轮范围内")
		}
		r.batch.ExpectedItems[input.ItemID] = items[0].UpdatedAt
		sets, err := r.manager.ListKnowledgeEvidence(ctx, r.batch.GroupID, input.ItemID, 0)
		if err != nil {
			return "", err
		}
		var groups [][]uint
		for _, set := range sets {
			var ids []uint
			for _, row := range set.Messages {
				if row.ID <= r.batch.ThroughID && row.RecalledAt == nil {
					ids = append(ids, row.ID)
				}
			}
			if len(ids) == len(set.Messages) && set.Valid {
				groups = append(groups, ids)
			}
		}
		start := min(len(groups), input.Offset)
		end := min(len(groups), input.Offset+10)
		var relations []memory.KnowledgeRelation
		if err := r.manager.GetDB().WithContext(ctx).Table("knowledge_relations kr").Select("kr.*").Joins("JOIN knowledge_items s ON s.id=kr.source_item_id JOIN knowledge_items t ON t.id=kr.target_item_id").Where("s.group_id=? AND t.group_id=? AND s.reviewed_through_id<=? AND t.reviewed_through_id<=? AND (kr.source_item_id=? OR kr.target_item_id=?)", r.batch.GroupID, r.batch.GroupID, r.batch.ThroughID, r.batch.ThroughID, input.ItemID, input.ItemID).Order("kr.id").Offset(input.Offset).Limit(11).Scan(&relations).Error; err != nil {
			return "", err
		}
		more := end < len(groups) || len(relations) > 10
		if len(relations) > 10 {
			relations = relations[:10]
		}
		return sonic.MarshalString(map[string]any{"item": items[0], "evidence_sets": groups[start:end], "relations": relations, "has_more": more, "next_offset": input.Offset + 10, "instruction": "使用 readContext 阅读原文后才能作为本轮证据；用 item_id 直接读取关系另一端"})
	}
	if input.Query != "" {
	}
	items, err := r.manager.SearchKnowledge(ctx, memory.KnowledgeSearchOptions{GroupID: r.batch.GroupID, Query: input.Query, SubjectUserID: input.SubjectUserID, IncludeInactive: true, ThroughID: r.batch.ThroughID, Limit: 11, Offset: input.Offset})
	if err != nil {
		return "", err
	}
	more := len(items) > 10
	if more {
		items = items[:10]
	}
	for _, item := range items {
		r.batch.ExpectedItems[item.ID] = item.UpdatedAt
	}
	return sonic.MarshalString(map[string]any{"items": items, "has_more": more, "next_offset": input.Offset + len(items)})
}

func (r *investigation) searchMessages(ctx context.Context, input *messageInput) (string, error) {
	page, err := r.manager.SearchKnowledgeMessages(ctx, memory.KnowledgeMessageQuery{GroupID: r.batch.GroupID, ThroughID: r.batch.ThroughID, AfterID: input.AfterID, Text: input.Text, UserID: input.UserID, ReplyToMessageID: input.ReplyToMessageID, From: input.From, To: input.To, Limit: 30})
	if err != nil {
		return "", err
	}
	return r.renderMessages(page, 0)
}

func (r *investigation) readContext(ctx context.Context, input *contextInput) (string, error) {
	if input.Offset < 0 || (input.Offset > 0 && input.Mode != "message") {
		return "", fmt.Errorf("无效长文位置")
	}
	page, err := r.manager.ReadKnowledgeContext(ctx, r.batch.GroupID, r.batch.ThroughID, input.MessageID, input.AfterID, input.Mode)
	if err != nil {
		return "", err
	}
	return r.renderMessages(page, input.Offset)
}

func (r *investigation) finish(ctx context.Context, input *finishInput) (string, error) {
	for _, id := range r.required {
		if !r.seen[id] {
			return "", fmt.Errorf("本批消息 %d 尚未完整读取", id)
		}
	}
	for id := range r.seen {
		r.batch.ReadMessageIDs = append(r.batch.ReadMessageIDs, id)
	}
	for i := range input.Items {
		if input.Items[i].SubjectUserID == memory.SubjectSelfInputID {
			input.Items[i].SubjectUserID = r.batch.SelfID
		}
	}
	r.batch.Items = input.Items
	r.batch.Relations = input.Relations
	r.batch.ReviewedIDs = input.ReviewedIDs
	var result *memory.KnowledgeCommitResult
	var err error
	if r.batch.AdvanceCursor {
		for _, row := range r.rows {
			if row.RecalledAt != nil || row.TextContent == "" {
				input.NoTopicIDs = append(input.NoTopicIDs, row.ID)
			}
		}
		result, err = r.manager.CommitConversation(ctx, r.batch, r.rows, r.topics, input.Topics, input.NoTopicIDs)
	} else {
		if len(input.Topics) > 0 || len(input.NoTopicIDs) > 0 {
			return "", fmt.Errorf("复核轮次不能修改话题")
		}
		result, err = r.manager.CommitKnowledgeBatch(ctx, r.batch)
	}
	if err != nil {
		return "", err
	}
	r.finished = true
	return sonic.MarshalString(result)
}

func (r *investigation) renderMessages(page memory.KnowledgeMessagePage, offset int) (string, error) {
	records := make([]map[string]any, 0, len(page.Messages))
	remaining := 6500
	next := uint(0)
	for i, row := range page.Messages {
		text := []rune(row.TextContent)
		if offset > len(text) {
			return "", fmt.Errorf("读取位置超出原文长度")
		}
		if remaining < 500 {
			page.HasMore = true
			break
		}
		end := min(len(text), offset+remaining-300)
		complete := end == len(text)
		records = append(records, map[string]any{"id": row.ID, "user_id": row.UserID, "nickname": row.Nickname, "time": row.MessageTime, "onebot_message_id": row.OneBotMessageID, "reply_to_message_id": row.ReplyToMessageID, "text": string(text[offset:end]), "offset": offset, "next_offset": end, "complete": complete})
		remaining -= end - offset + 300
		next = row.ID
		if offset <= r.partial[row.ID] {
			r.partial[row.ID] = max(r.partial[row.ID], end)
			if complete {
				r.seen[row.ID] = true
			}
		}
		if i < len(page.Messages)-1 && remaining < 500 {
			page.HasMore = true
			break
		}
	}
	return sonic.MarshalString(map[string]any{"messages": records, "has_more": page.HasMore, "next_id": next})
}
