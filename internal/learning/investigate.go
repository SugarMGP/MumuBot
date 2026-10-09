package learning

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"mumu-bot/internal/memory"
	agenttools "mumu-bot/internal/tools"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

type groupInvestigation struct {
	knowledgeInvestigation
	required    []uint
	finished    bool
	finishAlone bool
	rows        []memory.MessageLog
	topics      memory.ConversationContext
}

type groupFinishInput struct {
	Topics     []memory.ConversationTopic      `json:"topics"`
	NoTopicIDs []uint                          `json:"no_topic_ids"`
	Items      []memory.KnowledgeItemInput     `json:"items"`
	Relations  []memory.KnowledgeRelationInput `json:"relations"`
}
type groupMessageSearchInput struct {
	Text             string     `json:"text"`
	UserID           int64      `json:"user_id,omitempty"`
	AfterID          uint       `json:"after_id,omitempty"`
	ReplyToMessageID *int64     `json:"reply_to_message_id,omitempty"`
	From             *time.Time `json:"from,omitempty"`
	To               *time.Time `json:"to,omitempty"`
}
type groupContextInput struct {
	MessageID uint   `json:"message_id"`
	Mode      string `json:"mode" jsonschema:"enum=message,enum=replies,enum=topic,enum=window"`
	AfterID   uint   `json:"after_id,omitempty"`
	Offset    int    `json:"offset,omitempty" jsonschema:"description=仅 message 模式，续读长原文的字符位置"`
}

const learningInputBudget = 24000

func (l *Learner) investigateGroup(groupID, selfID int64, after, upper uint, rows []memory.MessageLog) error {
	ctx, cancel, cfg := l.investigateContext()
	defer cancel()
	run := &groupInvestigation{knowledgeInvestigation: knowledgeInvestigation{manager: l.memMgr, batch: memory.KnowledgeBatch{ConversationKind: memory.ConversationKindGroup, TargetID: groupID, SelfID: selfID, AfterID: after, ThroughID: upper, AdvanceCursor: true, RequireAssigned: true, ExpectedItems: make(map[uint]time.Time)}, seen: make(map[uint]bool), partial: make(map[uint]int)}}
	run.rows = rows
	var err error
	run.topics, err = l.memMgr.ConversationContext(ctx, groupID, upper, rows)
	if err != nil {
		return err
	}
	validRows, required := filterUsableRows(rows)
	run.required = required
	initialValue, err := run.renderMessages(memory.KnowledgeMessagePage{Messages: validRows}, 0)
	if err != nil {
		return err
	}
	initial, err := sonic.MarshalString(initialValue)
	if err != nil {
		return err
	}
	if len(validRows) == 0 {
		_, err := run.finish(ctx, &groupFinishInput{})
		return err
	}
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
	historyValue, err := run.renderMessages(memory.KnowledgeMessagePage{Messages: previous}, 0)
	if err != nil {
		return err
	}
	history, err := sonic.MarshalString(historyValue)
	if err != nil {
		return err
	}
	initial += "\n仅供上下文，不重新分配：" + history
	messages := []*schema.Message{schema.SystemMessage(groupMemoryPrompt), schema.UserMessage(fmt.Sprintf("群 %d，机器人 %d，固定内部消息范围 (%d,%d]。本批需完整读取的原文 ID：%v\n原文：%s", groupID, selfID, after, upper, run.required, initial))}
	// 话题上下文按优先级排序（本批归属 > 回复目标 > 近期活跃），超出预算时从末尾低优先级话题开始裁剪；
	// 只裁剪提示词，run.topics 观察快照保持完整，CommitConversation 的校验不受影响
	promptTopics := run.topics
	var topicText string
	for {
		text, err := sonic.MarshalString(promptTopics)
		if err != nil {
			return err
		}
		if utf8.RuneCountInString(text) <= learningInputBudget-utf8.RuneCountInString(initial) || len(promptTopics.Topics) == 0 {
			topicText = text
			break
		}
		promptTopics.Topics = promptTopics.Topics[:len(promptTopics.Topics)-1]
	}
	if len(promptTopics.Topics) < len(run.topics.Topics) {
		topicText += "（话题较多，仅展示与本批最相关的部分，其余可用 searchTopics 查询）"
	}
	messages = append(messages, schema.UserMessage("已有话题及原归属："+topicText))
	agent, err := run.newAgent(ctx, l.model, cfg.Learning.MaxStep)
	if err != nil {
		return err
	}
	return runUntilFinish(ctx, agent, messages, &run.finished)
}

func (r *groupInvestigation) tools() ([]tool.BaseTool, error) {
	a, err := utils.InferTool("searchKnowledge", "查本群知识，包含归档及原文已失效的历史记录；offset 分页。", r.search)
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
	d, err := utils.InferTool("finishMemoryBatch", "调查结束时单独调用，原子提交知识、关系、完整证据组和话题归属；无结果也需调用。", r.finishTool)
	if err != nil {
		return nil, err
	}
	e, err := utils.InferTool("searchTopics", "按关键词查本群历史话题，最多6项；仅在近期话题不足以解释当前消息时使用。", r.searchTopics)
	if err != nil {
		return nil, err
	}
	web, err := webTools()
	if err != nil {
		return nil, err
	}
	return append([]tool.BaseTool{a, b, c, d, e}, web...), nil
}

func (r *groupInvestigation) searchTopics(ctx context.Context, input *struct {
	Query string `json:"query"`
}) (any, error) {
	if input == nil {
		return nil, fmt.Errorf("缺少话题查询参数")
	}
	items, err := r.manager.SearchConversationTopics(ctx, r.batch.TargetID, r.batch.ThroughID, input.Query)
	if err != nil {
		return nil, err
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
	return items, nil
}

func (r *groupInvestigation) searchMessages(ctx context.Context, input *groupMessageSearchInput) (any, error) {
	if input == nil {
		return nil, fmt.Errorf("缺少历史查询参数")
	}
	page, err := r.manager.SearchKnowledgeMessages(ctx, memory.KnowledgeMessageQuery{TargetID: r.batch.TargetID, ThroughID: r.batch.ThroughID, AfterID: input.AfterID, Text: input.Text, UserID: input.UserID, ReplyToMessageID: input.ReplyToMessageID, From: input.From, To: input.To, Limit: 30})
	if err != nil {
		return nil, err
	}
	return r.renderMessages(page, 0)
}

func (r *groupInvestigation) readContext(ctx context.Context, input *groupContextInput) (any, error) {
	if input == nil || input.Offset < 0 || (input.Offset > 0 && input.Mode != "message") {
		return nil, fmt.Errorf("无效长文位置")
	}
	page, err := r.manager.ReadKnowledgeContext(ctx, r.batch.TargetID, r.batch.ThroughID, input.MessageID, input.AfterID, input.Mode)
	if err != nil {
		return nil, err
	}
	return r.renderMessages(page, input.Offset)
}

func (r *groupInvestigation) finish(ctx context.Context, input *groupFinishInput) (any, error) {
	if input == nil || r.finished {
		return nil, fmt.Errorf("缺少提交参数或本轮已经提交")
	}
	batch := r.batch
	batch.ReadMessageIDs = nil
	batch.Items = slices.Clone(input.Items)
	batch.Relations = slices.Clone(input.Relations)
	noTopic := slices.Clone(input.NoTopicIDs)
	for _, id := range r.required {
		if !r.seen[id] {
			return nil, fmt.Errorf("本批消息 %d 尚未完整读取", id)
		}
	}
	for id := range r.seen {
		batch.ReadMessageIDs = append(batch.ReadMessageIDs, id)
	}
	for i := range batch.Items {
		if batch.Items[i].SubjectUserID == memory.SubjectSelfInputID {
			batch.Items[i].SubjectUserID = batch.SelfID
		}
	}
	for _, row := range r.rows {
		if row.RecalledAt != nil || strings.TrimSpace(row.TextContent) == "" {
			noTopic = append(noTopic, row.ID)
		}
	}
	result, err := r.manager.CommitConversation(ctx, batch, r.rows, r.topics, input.Topics, noTopic)
	if err != nil {
		var validation *memory.ValidationError
		if errors.As(err, &validation) || errors.Is(err, memory.ErrSnapshotChanged) {
			return nil, err
		}
		return nil, agenttools.NewTerminalToolError(err)
	}
	r.finished = true
	return result, nil
}

func (r *groupInvestigation) finishTool(ctx context.Context, input *groupFinishInput) (any, error) {
	if !r.finishAlone {
		return map[string]any{"success": false, "message": "finishMemoryBatch 必须单独调用，请在其他读取完成后再提交"}, nil
	}
	result, err := r.finish(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := react.SetReturnDirectly(ctx); err != nil {
		return nil, agenttools.NewTerminalToolError(err)
	}
	return result, nil
}
