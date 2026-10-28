package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type SearchMemoryInputV2 struct {
	Keywords         []string `json:"keywords,omitempty" jsonschema:"description=关键词数组，最多6项；留空表示按主体和时间浏览"`
	SubjectUserID    *int64   `json:"subject_user_id,omitempty" jsonschema:"description=-1 表示机器人自身，0 表示群组，正数表示成员 QQ"`
	Kind             string   `json:"kind,omitempty" jsonschema:"description=可选类型：fact/preference/constraint/goal/term/expression/alias"`
	Status           string   `json:"status,omitempty" jsonschema:"description=active 或 archived，默认 active"`
	ConversationKind string   `json:"conversation_kind,omitempty" jsonschema:"description=可选来源会话类型：group/private"`
	TargetID         int64    `json:"target_id,omitempty" jsonschema:"description=可选来源会话目标"`
	Limit            int      `json:"limit,omitempty" jsonschema:"description=默认8，最多20"`
	Offset           int      `json:"offset,omitempty"`
}

type ReadMemoryInputV2 struct {
	ItemID uint `json:"item_id"`
}

type ManageMemoryInputV2 struct {
	Changes []MemoryChangeInput `json:"changes" jsonschema:"description=1到8项，全部成功或全部回滚"`
}

type MemoryChangeInput struct {
	Action            string   `json:"action" jsonschema:"description=create/update/archive/restore"`
	ItemID            uint     `json:"item_id,omitempty"`
	SubjectUserID     *int64   `json:"subject_user_id,omitempty"`
	Kind              string   `json:"kind,omitempty"`
	Label             string   `json:"label,omitempty"`
	Content           string   `json:"content,omitempty"`
	IsCore            *bool    `json:"is_core,omitempty"`
	SourceMessageRefs []string `json:"source_message_refs,omitempty" jsonschema:"description=可选的当前会话 m 编号，不是保存门槛"`
	Reason            string   `json:"reason,omitempty"`
}

type SearchTopicsInput struct {
	Keywords         []string `json:"keywords,omitempty"`
	ConversationKind string   `json:"conversation_kind,omitempty"`
	TargetID         int64    `json:"target_id,omitempty"`
	Status           string   `json:"status,omitempty"`
	Limit            int      `json:"limit,omitempty"`
	Offset           int      `json:"offset,omitempty"`
}

type ReadTopicInput struct {
	TopicID uint `json:"topic_id"`
}

type SearchHistoryInputV2 struct {
	Keywords         []string `json:"keywords,omitempty"`
	PersonID         int64    `json:"person_id,omitempty"`
	ConversationKind string   `json:"conversation_kind,omitempty"`
	TargetID         int64    `json:"target_id,omitempty"`
	From             string   `json:"from,omitempty"`
	To               string   `json:"to,omitempty"`
	BeforeID         uint     `json:"before_id,omitempty" jsonschema:"description=倒序翻页时填上一页最后一条内部消息ID"`
	Limit            int      `json:"limit,omitempty"`
}

type ReadHistoryInputV2 struct {
	ConversationKind string `json:"conversation_kind,omitempty"`
	TargetID         int64  `json:"target_id,omitempty"`
	AroundMessageID  uint   `json:"around_message_id,omitempty"`
	AroundRef        string `json:"around_ref,omitempty" jsonschema:"description=可用历史编号 h12345 或当前会话 m3"`
	TopicID          uint   `json:"topic_id,omitempty"`
	Before           int    `json:"before,omitempty"`
	After            int    `json:"after,omitempty"`
}

type HistoryMessageView struct {
	ID               uint       `json:"id"`
	HistoryRef       string     `json:"history_ref"`
	MessageRef       string     `json:"message_ref,omitempty"`
	ConversationKind string     `json:"conversation_kind"`
	TargetID         int64      `json:"target_id"`
	UserID           int64      `json:"user_id"`
	Nickname         string     `json:"nickname"`
	Time             time.Time  `json:"time"`
	Content          string     `json:"content"`
	DisplayContent   string     `json:"display_content,omitempty"`
	RecalledAt       *time.Time `json:"recalled_at,omitempty"`
}

func sourceMetaJSON(tc *ToolContext, refs []string) (string, error) {
	source := map[string]any{
		"conversation_kind": tc.ConversationKind,
		"target_id":         tc.TargetID,
	}
	if tc.Session != nil {
		ids := make([]uint, 0, len(refs))
		for _, ref := range refs {
			id, ok := tc.Session.ResolveMessageLogRef(ref)
			if !ok {
				return "", fmt.Errorf("来源消息 %s 不属于当前会话", ref)
			}
			ids = append(ids, id)
		}
		if len(ids) > 0 {
			source["message_log_ids"] = ids
		}
	}
	raw, err := sonic.MarshalString(source)
	if err != nil {
		return "", err
	}
	return raw, nil
}

func resolveSubject(tc *ToolContext, subject *int64) (*int64, error) {
	if subject == nil {
		return nil, nil
	}
	value := *subject
	if value == -1 {
		if tc.Bot == nil || tc.Bot.GetSelfID() <= 0 {
			return nil, fmt.Errorf("机器人账号尚未就绪")
		}
		value = tc.Bot.GetSelfID()
	}
	if value < 0 {
		return nil, fmt.Errorf("无效的记忆主体")
	}
	return &value, nil
}

func searchMemoryV2(ctx context.Context, input *SearchMemoryInputV2) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("记忆检索能力未初始化"))
	}
	if input == nil {
		input = &SearchMemoryInputV2{}
	}
	if len(input.Keywords) > 6 {
		return nil, fmt.Errorf("关键词最多 6 项")
	}
	subject, err := resolveSubject(tc, input.SubjectUserID)
	if err != nil {
		return nil, err
	}
	hits, err := tc.MemoryMgr.SearchMemories(ctx, memory.MemorySearchQuery{
		Keywords: input.Keywords, ConversationKind: input.ConversationKind, TargetID: input.TargetID,
		SubjectUserID: subject, Kind: input.Kind, Status: input.Status, Limit: input.Limit, Offset: input.Offset,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "matches": hits}, nil
}

func readMemoryV2(ctx context.Context, input *ReadMemoryInputV2) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("记忆读取能力未初始化"))
	}
	if input == nil || input.ItemID == 0 {
		return nil, fmt.Errorf("请提供 item_id")
	}
	detail, err := tc.MemoryMgr.ReadMemory(ctx, input.ItemID)
	if err != nil {
		return nil, err
	}
	tc.MarkMemoryRead(detail.Item.ID, detail.Item.Version)
	return map[string]any{"success": true, "item": detail.Item, "revisions": detail.Revisions}, nil
}

func manageMemoryV2(ctx context.Context, input *ManageMemoryInputV2) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("记忆维护能力未初始化"))
	}
	if input == nil || len(input.Changes) == 0 || len(input.Changes) > 8 {
		return nil, fmt.Errorf("一次记忆变更必须为 1 到 8 项")
	}
	changes := make([]memory.MemoryChange, 0, len(input.Changes))
	for _, raw := range input.Changes {
		subject, err := resolveSubject(tc, raw.SubjectUserID)
		if err != nil {
			return nil, err
		}
		source, err := sourceMetaJSON(tc, raw.SourceMessageRefs)
		if err != nil {
			return nil, err
		}
		change := memory.MemoryChange{
			Action: raw.Action, ItemID: raw.ItemID, SubjectUserID: subject, Kind: raw.Kind,
			Label: raw.Label, Content: raw.Content, IsCore: raw.IsCore, Reason: raw.Reason,
			SourceMeta: source, SourceConversation: tc.ConversationKind, SourceTargetID: tc.TargetID,
		}
		if raw.Action == "update" || raw.Action == "archive" || raw.Action == "restore" {
			version, ok := tc.MemoryVersion(raw.ItemID)
			if !ok {
				return nil, fmt.Errorf("修改记忆 %d 前必须先用 searchMemory 或 readMemory 完整读取", raw.ItemID)
			}
			change.ExpectedVersion = version
		}
		changes = append(changes, change)
	}
	results, err := tc.MemoryMgr.ManageMemories(ctx, changes)
	if err != nil {
		return nil, err
	}
	for _, result := range results {
		tc.MarkMemoryRead(result.ItemID, result.Version)
	}
	return map[string]any{"success": true, "results": results}, nil
}

func searchTopics(ctx context.Context, input *SearchTopicsInput) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("话题检索能力未初始化"))
	}
	if input == nil {
		input = &SearchTopicsInput{}
	}
	if tc.CurrentConversationOnly {
		if (input.ConversationKind != "" && input.ConversationKind != tc.ConversationKind) || (input.TargetID != 0 && input.TargetID != tc.TargetID) {
			return nil, fmt.Errorf("压缩器只能读取当前会话")
		}
		input.ConversationKind = tc.ConversationKind
		input.TargetID = tc.TargetID
	}
	if len(input.Keywords) > 6 {
		return nil, fmt.Errorf("关键词最多 6 项")
	}
	if input.Limit <= 0 {
		input.Limit = 8
	}
	if input.Limit > 20 {
		return nil, fmt.Errorf("话题检索每页最多 20 项")
	}
	hits, err := tc.MemoryMgr.SearchTopics(ctx, memory.TopicSearchQuery{
		Keywords: input.Keywords, ConversationKind: input.ConversationKind, TargetID: input.TargetID,
		Status: input.Status, Limit: input.Limit, Offset: input.Offset,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "topics": hits}, nil
}

func readTopic(ctx context.Context, input *ReadTopicInput) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("话题读取能力未初始化"))
	}
	if input == nil || input.TopicID == 0 {
		return nil, fmt.Errorf("请提供 topic_id")
	}
	detail, err := tc.MemoryMgr.ReadTopic(ctx, input.TopicID)
	if err != nil {
		return nil, err
	}
	if tc.CurrentConversationOnly && (detail.Topic.ConversationKind != tc.ConversationKind || detail.Topic.TargetID != tc.TargetID) {
		return nil, fmt.Errorf("压缩器只能读取当前会话")
	}
	return map[string]any{"success": true, "topic": detail}, nil
}

func searchHistoryV2(ctx context.Context, input *SearchHistoryInputV2) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("历史检索能力未初始化"))
	}
	if input == nil {
		input = &SearchHistoryInputV2{}
	}
	kind := input.ConversationKind
	target := input.TargetID
	if tc.CurrentConversationOnly {
		if (kind != "" && kind != tc.ConversationKind) || (target != 0 && target != tc.TargetID) {
			return nil, fmt.Errorf("压缩器只能读取当前会话")
		}
		kind, target = tc.ConversationKind, tc.TargetID
	} else if kind == "" && target > 0 {
		kind = tc.ConversationKind
	}
	from, err := parseHistoryTime(input.From)
	if err != nil {
		return nil, err
	}
	to, err := parseHistoryTime(input.To)
	if err != nil {
		return nil, err
	}
	beforeID := input.BeforeID
	if kind == tc.ConversationKind && target == tc.TargetID && tc.SnapshotLogID > 0 && (beforeID == 0 || beforeID > tc.SnapshotLogID) {
		beforeID = tc.SnapshotLogID
	}
	hits, err := tc.MemoryMgr.SearchHistoryV2(ctx, memory.HistoryQueryV2{
		ConversationKind: kind, TargetID: target, UserID: input.PersonID, Keywords: input.Keywords,
		From: from, To: to, BeforeID: beforeID, Limit: input.Limit,
	})
	if err != nil {
		return nil, err
	}
	messages := make([]HistoryMessageView, 0, len(hits))
	for _, row := range hits {
		if tc.Session != nil && row.ConversationKind == tc.ConversationKind && row.TargetID == tc.TargetID {
			tc.Session.RegisterMediaRefs(row.MediaRefs)
		}
		messages = append(messages, historyView(row))
	}
	return map[string]any{"success": true, "messages": messages}, nil
}

func readHistoryV2(ctx context.Context, input *ReadHistoryInputV2) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("历史读取能力未初始化"))
	}
	if input == nil {
		return nil, fmt.Errorf("历史读取参数不能为空")
	}
	if input.AroundMessageID != 0 && input.TopicID != 0 {
		return nil, fmt.Errorf("按消息和按话题读取只能选择一个")
	}
	if ref := strings.TrimSpace(input.AroundRef); ref != "" {
		var id uint
		if strings.HasPrefix(ref, "h") {
			value, err := strconv.ParseUint(strings.TrimPrefix(ref, "h"), 10, 64)
			if err != nil || value == 0 {
				return nil, fmt.Errorf("历史编号 %s 无效", ref)
			}
			id = uint(value)
		} else if strings.HasPrefix(ref, "m") && tc.Session != nil {
			value, ok := tc.Session.ResolveMessageLogRef(ref)
			if !ok {
				return nil, fmt.Errorf("消息 %s 不在当前会话", ref)
			}
			id = value
		} else {
			return nil, fmt.Errorf("历史编号必须是 h 或当前会话 m 编号")
		}
		if input.AroundMessageID != 0 && input.AroundMessageID != id {
			return nil, fmt.Errorf("around_message_id 与 around_ref 不一致")
		}
		input.AroundMessageID = id
	}
	kind := input.ConversationKind
	target := input.TargetID
	if kind == "" && target == 0 && (input.AroundMessageID != 0 || input.TopicID != 0) {
		kind, target = tc.ConversationKind, tc.TargetID
	}
	if tc.CurrentConversationOnly {
		if (kind != "" && kind != tc.ConversationKind) || (target != 0 && target != tc.TargetID) {
			return nil, fmt.Errorf("压缩器只能读取当前会话")
		}
		kind, target = tc.ConversationKind, tc.TargetID
	} else if kind == "" && target > 0 {
		kind = tc.ConversationKind
	}
	throughID := uint(0)
	if kind == tc.ConversationKind && target == tc.TargetID {
		throughID = tc.SnapshotLogID
	}
	exact := input.TopicID == 0 && input.AroundMessageID != 0 && input.Before == 0 && input.After == 0
	rows, err := tc.MemoryMgr.ReadHistoryRange(ctx, memory.HistoryRange{
		ConversationKind: kind, TargetID: target, AroundMessageID: input.AroundMessageID,
		TopicID: input.TopicID, ThroughID: throughID, Before: input.Before, After: input.After, Exact: exact,
	})
	if err != nil {
		return nil, err
	}
	messages := make([]HistoryMessageView, 0, len(rows))
	for _, row := range rows {
		if tc.Session != nil && row.ConversationKind == tc.ConversationKind && row.TargetID == tc.TargetID {
			tc.Session.RegisterMediaRefs(row.MediaRefs)
		}
		messages = append(messages, historyView(row))
	}
	return map[string]any{"success": true, "messages": messages}, nil
}

func historyView(row memory.MessageLog) HistoryMessageView {
	return HistoryMessageView{
		ID: row.ID, HistoryRef: fmt.Sprintf("h%d", row.ID), MessageRef: row.MessageRef, ConversationKind: row.ConversationKind, TargetID: row.TargetID,
		UserID: row.UserID, Nickname: row.Nickname, Time: row.MessageTime,
		Content: row.TextContent, DisplayContent: row.DisplayContent, RecalledAt: row.RecalledAt,
	}
}

func NewSearchMemoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("searchMemory", "按多关键词、主体、类型、状态和来源会话检索长期记忆；关键词留空可浏览。跨群聊和私聊可用。", searchMemoryV2)
}

func NewReadMemoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("readMemory", "按编号完整读取长期记忆正文、来源和修订历史；修改前必须先读取。", readMemoryV2)
}

func NewManageMemoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("manageMemory", "批量创建、更新、归档或恢复长期记忆；更新和归档前必须先完整读取。", manageMemoryV2)
}

func NewSearchTopicsTool() (tool.InvokableTool, error) {
	return utils.InferTool("searchTopics", "按关键词检索群聊和私聊话题，返回概览、未完事项和关键消息编号。", searchTopics)
}

func NewReadTopicTool() (tool.InvokableTool, error) {
	return utils.InferTool("readTopic", "读取一个话题的完整摘要、关键消息、来源消息和版本历史。", readTopic)
}

func NewSearchHistoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("searchHistory", "跨群聊和私聊检索原始消息；可使用关键词、人物、会话和时间筛选，已撤回原文仍会返回并带标记。", searchHistoryV2)
}

func NewReadHistoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("readHistory", "按话题或某一关键消息读取前后原文；不填 before/after 的 h 编号读取单条原文。", readHistoryV2)
}

func parseHistoryTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if value, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return &value, nil
		}
	}
	return nil, fmt.Errorf("时间格式无效：%s", raw)
}
