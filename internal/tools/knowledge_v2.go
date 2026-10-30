package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type SearchMemoryInputV2 struct {
	Keywords        []string `json:"keywords,omitempty" jsonschema:"description=关键词数组；支持 or/and"`
	Match           string   `json:"match,omitempty" jsonschema:"description=or 或 and，默认 or"`
	ExcludeKeywords []string `json:"exclude_keywords,omitempty" jsonschema:"description=排除关键词"`
	SubjectType     string   `json:"subject_type,omitempty" jsonschema:"description=group/user/self/object"`
	SubjectTarget   string   `json:"subject_target,omitempty" jsonschema:"description=主体 ID 或 object 名称"`
	MemoryType      string   `json:"memory_type,omitempty" jsonschema:"description=profile 或 experience"`
	Status          string   `json:"status,omitempty" jsonschema:"description=active 或 archived，默认 active"`
	Cursor          string   `json:"cursor,omitempty" jsonschema:"description=上一页返回的 cursor"`
	Limit           int      `json:"limit,omitempty" jsonschema:"description=默认12，最大99"`
}

type ReadMemoryInputV2 struct {
	ItemID uint `json:"item_id"`
}

type ManageMemoryInputV2 struct {
	Changes []MemoryChangeInput `json:"changes" jsonschema:"description=1到8项，全部成功或全部回滚"`
}

type MemoryChangeInput struct {
	Action        string `json:"action" jsonschema:"description=create/update/archive/restore"`
	ItemID        uint   `json:"item_id,omitempty"`
	SubjectType   string `json:"subject_type,omitempty" jsonschema:"description=group/user/self/object"`
	SubjectTarget string `json:"subject_target,omitempty" jsonschema:"description=主体 ID 或 object 名称"`
	MemoryType    string `json:"memory_type,omitempty" jsonschema:"description=profile 或 experience"`
	Label         string `json:"label,omitempty"`
	Content       string `json:"content,omitempty"`
	Pinned        *bool  `json:"pinned,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type SearchTopicsInput struct {
	Keywords         []string `json:"keywords,omitempty"`
	Match            string   `json:"match,omitempty" jsonschema:"description=or 或 and，默认 or"`
	ExcludeKeywords  []string `json:"exclude_keywords,omitempty"`
	ConversationKind string   `json:"conversation_kind,omitempty"`
	TargetID         int64    `json:"target_id,omitempty"`
	Status           string   `json:"status,omitempty"`
	Cursor           string   `json:"cursor,omitempty"`
	Limit            int      `json:"limit,omitempty" jsonschema:"description=默认12，最大99"`
}

type ReadTopicInput struct {
	TopicID uint `json:"topic_id"`
}

type SearchHistoryInputV2 struct {
	Keywords         []string `json:"keywords,omitempty"`
	Match            string   `json:"match,omitempty" jsonschema:"description=or 或 and，默认 or"`
	ExcludeKeywords  []string `json:"exclude_keywords,omitempty"`
	PersonID         int64    `json:"person_id,omitempty"`
	ConversationKind string   `json:"conversation_kind,omitempty"`
	TargetID         int64    `json:"target_id,omitempty"`
	From             string   `json:"from,omitempty"`
	To               string   `json:"to,omitempty"`
	Cursor           string   `json:"cursor,omitempty" jsonschema:"description=上一页返回的 cursor"`
	Limit            int      `json:"limit,omitempty" jsonschema:"description=默认12，最大99"`
}

type ReadHistoryInputV2 struct {
	ConversationKind string `json:"conversation_kind,omitempty"`
	TargetID         int64  `json:"target_id,omitempty"`
	AroundMessageID  int64  `json:"around_message_id,omitempty" jsonschema:"description=OneBot 消息 ID"`
	TopicID          uint   `json:"topic_id,omitempty"`
	Before           int    `json:"before,omitempty"`
	After            int    `json:"after,omitempty"`
}

type HistoryMessageView struct {
	MessageID        int64      `json:"message_id"`
	ConversationKind string     `json:"conversation_kind"`
	TargetID         int64      `json:"target_id"`
	UserID           int64      `json:"user_id"`
	Nickname         string     `json:"nickname"`
	Time             time.Time  `json:"time"`
	Content          string     `json:"content"`
	DisplayContent   string     `json:"display_content,omitempty"`
	RecalledAt       *time.Time `json:"recalled_at,omitempty"`
}

func searchMemoryV2(ctx context.Context, input *SearchMemoryInputV2) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("记忆检索能力未初始化"))
	}
	if input == nil {
		input = &SearchMemoryInputV2{}
	}
	page, err := tc.MemoryMgr.SearchMemories(ctx, memory.MemorySearchQuery{
		Keywords: input.Keywords, Match: input.Match, ExcludeKeywords: input.ExcludeKeywords,
		SubjectType: input.SubjectType, SubjectTarget: input.SubjectTarget, MemoryType: input.MemoryType,
		Status: input.Status, Cursor: input.Cursor, Limit: input.Limit,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "matches": page.Items, "next_cursor": page.NextCursor, "has_more": page.HasMore}, nil
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
	tc.MarkReferenceSeen("memory:"+fmt.Sprint(detail.Item.ID), fmt.Sprintf("v%d", detail.Item.Version))
	return map[string]any{"success": true, "item": detail.Item}, nil
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
		change := memory.MemoryChange{
			Action: raw.Action, ItemID: raw.ItemID, SubjectType: raw.SubjectType,
			SubjectTarget: raw.SubjectTarget, MemoryType: raw.MemoryType, Label: raw.Label,
			Content: raw.Content, Pinned: raw.Pinned, Reason: raw.Reason,
		}
		if raw.Action == "update" || raw.Action == "archive" || raw.Action == "restore" {
			version, ok := tc.MemoryVersion(raw.ItemID)
			if !ok {
				return nil, fmt.Errorf("修改记忆 %d 前必须先用 readMemory 完整读取", raw.ItemID)
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
		tc.MarkReferenceSeen("memory:"+fmt.Sprint(result.ItemID), fmt.Sprintf("v%d", result.Version))
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
		if input.ConversationKind != "" && input.ConversationKind != tc.ConversationKind || input.TargetID != 0 && input.TargetID != tc.TargetID {
			return nil, fmt.Errorf("压缩器只能读取当前会话")
		}
		input.ConversationKind, input.TargetID = tc.ConversationKind, tc.TargetID
	}
	page, err := tc.MemoryMgr.SearchTopics(ctx, memory.TopicSearchQuery{
		Keywords: input.Keywords, Match: input.Match, ExcludeKeywords: input.ExcludeKeywords,
		ConversationKind: input.ConversationKind, TargetID: input.TargetID, Status: input.Status,
		Cursor: input.Cursor, Limit: input.Limit,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "topics": page.Items, "next_cursor": page.NextCursor, "has_more": page.HasMore}, nil
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
	kind, target := input.ConversationKind, input.TargetID
	if tc.CurrentConversationOnly {
		if kind != "" && kind != tc.ConversationKind || target != 0 && target != tc.TargetID {
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
	page, err := tc.MemoryMgr.SearchHistoryV2(ctx, memory.HistoryQueryV2{
		ConversationKind: kind, TargetID: target, UserID: input.PersonID,
		Keywords: input.Keywords, Match: input.Match, ExcludeKeywords: input.ExcludeKeywords,
		From: from, To: to, MaxMessageLogID: historyMaxMessageLogID(tc, kind, target),
		Cursor: input.Cursor, Limit: input.Limit,
	})
	if err != nil {
		return nil, err
	}
	messages := make([]HistoryMessageView, 0, len(page.Items))
	for _, row := range page.Items {
		if tc.Session != nil && row.ConversationKind == tc.ConversationKind && row.TargetID == tc.TargetID {
			tc.Session.RegisterMediaRefs(row.MediaRefs)
		}
		messages = append(messages, historyViewPreview(row))
	}
	return map[string]any{"success": true, "messages": messages, "next_cursor": page.NextCursor, "has_more": page.HasMore}, nil
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
	kind, target := input.ConversationKind, input.TargetID
	if kind == "" && target == 0 && (input.AroundMessageID != 0 || input.TopicID != 0) {
		kind, target = tc.ConversationKind, tc.TargetID
	}
	if tc.CurrentConversationOnly {
		if kind != "" && kind != tc.ConversationKind || target != 0 && target != tc.TargetID {
			return nil, fmt.Errorf("压缩器只能读取当前会话")
		}
		kind, target = tc.ConversationKind, tc.TargetID
	} else if kind == "" && target > 0 {
		kind = tc.ConversationKind
	}
	rows, err := tc.MemoryMgr.ReadHistoryRange(ctx, memory.HistoryRange{
		ConversationKind: kind, TargetID: target, AroundMessageID: input.AroundMessageID,
		TopicID: input.TopicID, MaxMessageLogID: historyMaxMessageLogID(tc, kind, target),
		Before: input.Before, After: input.After,
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
		MessageID: row.OneBotMessageID, ConversationKind: row.ConversationKind, TargetID: row.TargetID,
		UserID: row.UserID, Nickname: row.Nickname, Time: row.MessageTime,
		Content: row.TextContent, DisplayContent: row.DisplayContent, RecalledAt: row.RecalledAt,
	}
}

func historyMaxMessageLogID(tc *ToolContext, kind string, targetID int64) uint {
	if tc == nil || tc.SnapshotLogID == 0 || kind != tc.ConversationKind || targetID != tc.TargetID {
		return 0
	}
	return tc.SnapshotLogID
}

func historyViewPreview(row memory.MessageLog) HistoryMessageView {
	view := historyView(row)
	view.Content = truncateToolLogString(view.Content, 300)
	view.DisplayContent = truncateToolLogString(view.DisplayContent, 300)
	if view.DisplayContent == view.Content {
		view.DisplayContent = ""
	}
	return view
}

func NewSearchMemoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("searchMemory", "按关键词、主体和类型检索长期记忆；支持 or/and 和 cursor 翻页。", searchMemoryV2)
}

func NewReadMemoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("readMemory", "按编号完整读取长期记忆正文。", readMemoryV2)
}

func NewManageMemoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("manageMemory", "批量创建、更新、归档或恢复长期记忆；1到8项全成功或全回滚。", manageMemoryV2)
}

func NewSearchTopicsTool() (tool.InvokableTool, error) {
	return utils.InferTool("searchTopics", "按关键词检索群聊和私聊话题，返回概览、未完事项和关键消息编号。", searchTopics)
}

func NewReadTopicTool() (tool.InvokableTool, error) {
	return utils.InferTool("readTopic", "读取一个话题的当前摘要、来源消息和关键消息。", readTopic)
}

func NewSearchHistoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("searchHistory", "跨群聊和私聊检索原始消息；支持关键词、人物、会话、时间筛选和 cursor 翻页。", searchHistoryV2)
}

func NewReadHistoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("readHistory", "按话题或 OneBot 消息 ID 读取前后原文。", readHistoryV2)
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
