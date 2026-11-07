package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"mumu-bot/internal/filemeta"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/refstore"
	"mumu-bot/internal/schedule"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type SearchMemoryInputV2 struct {
	Keywords        []string `json:"keywords,omitempty" jsonschema:"description=关键词数组；一次给多个同义词/别名/相关词，配合 or 提高召回"`
	Match           string   `json:"match,omitempty" jsonschema:"description=or 或 and，默认 or"`
	Mode            string   `json:"mode,omitempty" jsonschema:"description=exact 或 fuzzy；默认 exact；精确找不到时用 fuzzy 再查一次"`
	ExcludeKeywords []string `json:"exclude_keywords,omitempty" jsonschema:"description=排除关键词"`
	SubjectType     string   `json:"subject_type,omitempty" jsonschema:"description=group/user/self/object"`
	SubjectTarget   string   `json:"subject_target,omitempty" jsonschema:"description=主体 ID 或 object 名称"`
	MemoryType      string   `json:"memory_type,omitempty" jsonschema:"description=profile 或 experience"`
	UpdatedFrom     string   `json:"updated_from,omitempty" jsonschema:"description=更新时间下限，格式 2006-01-02 或 RFC3339"`
	UpdatedTo       string   `json:"updated_to,omitempty" jsonschema:"description=更新时间上限，格式 2006-01-02 或 RFC3339"`
	Cursor          string   `json:"cursor,omitempty" jsonschema:"description=上一页返回的 cursor"`
	Limit           int      `json:"limit,omitempty" jsonschema:"description=默认12，最大99"`
}

type ReadMemoryInputV2 struct {
	ItemID uint `json:"item_id"`
}

type ManageMemoryInputV2 struct {
	Changes []MemoryChangeInput `json:"changes" jsonschema:"description=至少1项，全部成功或全部回滚"`
}

type MemoryChangeInput struct {
	Action        string `json:"action" jsonschema:"description=create/update/archive；归档后不可恢复"`
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
	ConversationKind string   `json:"conversation_kind,omitempty" jsonschema:"description=可选来源种类：group 或 private；省略默认全局"`
	TargetID         int64    `json:"target_id,omitempty" jsonschema:"description=可选来源群号或好友 QQ；省略默认全局"`
	Status           string   `json:"status,omitempty"`
	From             string   `json:"from,omitempty" jsonschema:"description=来源消息时间下限，格式 2006-01-02 或 RFC3339"`
	To               string   `json:"to,omitempty" jsonschema:"description=来源消息时间上限，纯日期包含当天，格式 2006-01-02 或 RFC3339"`
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
	UserID           int64    `json:"user_id,omitempty" jsonschema:"description=可选发言人 QQ"`
	ConversationKind string   `json:"conversation_kind,omitempty" jsonschema:"description=可选来源种类：group 或 private；省略默认全局"`
	TargetID         int64    `json:"target_id,omitempty" jsonschema:"description=可选来源群号或好友 QQ；省略默认全局"`
	From             string   `json:"from,omitempty" jsonschema:"description=来源消息时间下限，格式 2006-01-02 或 RFC3339"`
	To               string   `json:"to,omitempty"`
	Cursor           string   `json:"cursor,omitempty" jsonschema:"description=上一页返回的 cursor"`
	Limit            int      `json:"limit,omitempty" jsonschema:"description=默认12，最大99"`
}

type ReadHistoryInputV2 struct {
	ConversationKind string `json:"conversation_kind,omitempty" jsonschema:"description=要读取的会话种类；省略默认当前会话"`
	TargetID         int64  `json:"target_id,omitempty" jsonschema:"description=要读取的群号或好友 QQ；省略默认当前会话"`
	AroundMessageID  int64  `json:"around_message_id,omitempty" jsonschema:"description=OneBot 消息 ID；与 topic_id 二选一"`
	TopicID          uint   `json:"topic_id,omitempty" jsonschema:"description=话题编号；与 around_message_id 二选一"`
	Before           int    `json:"before,omitempty" jsonschema:"description=向前读取条数，默认5，最大15"`
	After            int    `json:"after,omitempty" jsonschema:"description=向后读取条数，默认5，最大15"`
}

type HistoryImage struct {
	ImageRef string `json:"image_ref"`
	Mime     string `json:"mime,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Sticker  bool   `json:"sticker,omitempty"`
}

type HistoryMessageView struct {
	MessageID        int64          `json:"message_id"`
	ConversationKind string         `json:"conversation_kind"`
	TargetID         int64          `json:"target_id"`
	UserID           int64          `json:"user_id"`
	Nickname         string         `json:"nickname"`
	Time             time.Time      `json:"time"`
	Content          string         `json:"content"`
	DisplayContent   string         `json:"display_content,omitempty"`
	Files            filemeta.Refs  `json:"files,omitempty"`
	Images           []HistoryImage `json:"images,omitempty"`
	RecalledAt       *time.Time     `json:"recalled_at,omitempty"`
}

func searchMemoryV2(ctx context.Context, input *SearchMemoryInputV2) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("记忆检索能力未初始化"))
	}
	if input == nil {
		input = &SearchMemoryInputV2{}
	}
	updatedFrom, updatedTo, err := schedule.ParseRange(input.UpdatedFrom, input.UpdatedTo)
	if err != nil {
		return nil, err
	}
	page, err := tc.MemoryMgr.SearchMemories(ctx, memory.MemorySearchQuery{
		Keywords: input.Keywords, Match: input.Match, Mode: input.Mode, ExcludeKeywords: input.ExcludeKeywords,
		SubjectType: input.SubjectType, SubjectTarget: input.SubjectTarget, MemoryType: input.MemoryType,
		UpdatedFrom: updatedFrom, UpdatedTo: updatedTo, Cursor: input.Cursor, Limit: input.Limit,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(page.Items))
	if !tc.CurrentConversationOnly {
		for _, hit := range page.Items {
			if tc.MemoryRecallNeeded(hit.ID) {
				ids = append(ids, hit.ID)
			}
		}
	}
	if len(ids) > 0 {
		if _, err := tc.MemoryMgr.ReinforceMemories(ctx, ids, time.Now()); err != nil {
			return nil, err
		}
		for _, id := range ids {
			tc.MarkMemoryRecall(id)
		}
	}
	return map[string]any{"success": true, "mode": page.Mode, "matches": page.Items, "next_cursor": page.NextCursor, "has_more": page.HasMore}, nil
}

func readMemoryV2(ctx context.Context, input *ReadMemoryInputV2) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("记忆读取能力未初始化"))
	}
	if input == nil || input.ItemID == 0 {
		return nil, fmt.Errorf("请提供 item_id")
	}
	item, err := tc.MemoryMgr.ReadMemory(ctx, input.ItemID)
	if err != nil {
		return nil, err
	}
	if !tc.CurrentConversationOnly && tc.MemoryRecallNeeded(item.ID) {
		if _, err := tc.MemoryMgr.ReinforceMemories(ctx, []uint{item.ID}, time.Now()); err != nil {
			return nil, err
		}
		tc.MarkMemoryRecall(item.ID)
	}
	tc.MarkMemoryRead(item.ID, item.Version)
	return map[string]any{"success": true, "item": item}, nil
}

func manageMemoryV2(ctx context.Context, input *ManageMemoryInputV2) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("记忆维护能力未初始化"))
	}
	if input == nil || len(input.Changes) == 0 {
		return nil, fmt.Errorf("一次记忆变更至少需要 1 项")
	}
	changes := make([]memory.MemoryChange, 0, len(input.Changes))
	for _, raw := range input.Changes {
		change := memory.MemoryChange{
			Action: raw.Action, ItemID: raw.ItemID, SubjectType: raw.SubjectType,
			SubjectTarget: raw.SubjectTarget, MemoryType: raw.MemoryType, Label: raw.Label,
			Content: raw.Content, Pinned: raw.Pinned, Reason: raw.Reason,
		}
		if raw.Action == "update" || raw.Action == "archive" {
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
	}
	tc.MemoryMaintained = true
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
	from, until, err := schedule.ParseRange(input.From, input.To)
	if err != nil {
		return nil, err
	}
	page, err := tc.MemoryMgr.SearchTopics(ctx, memory.TopicSearchQuery{
		Keywords: input.Keywords, Match: input.Match, ExcludeKeywords: input.ExcludeKeywords,
		ConversationKind: input.ConversationKind, TargetID: input.TargetID, Status: input.Status,
		From: from, To: until, Cursor: input.Cursor, Limit: input.Limit,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(page.Items))
	if !tc.CurrentConversationOnly {
		for _, hit := range page.Items {
			if tc.TopicRecallNeeded(hit.TopicID) {
				ids = append(ids, hit.TopicID)
			}
		}
	}
	if len(ids) > 0 {
		if _, err := tc.MemoryMgr.ReinforceTopics(ctx, ids, time.Now()); err != nil {
			return nil, err
		}
		for _, id := range ids {
			tc.MarkTopicRecall(id)
		}
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
	if !tc.CurrentConversationOnly && tc.TopicRecallNeeded(detail.Topic.ID) {
		if _, err := tc.MemoryMgr.ReinforceTopics(ctx, []uint{detail.Topic.ID}, time.Now()); err != nil {
			return nil, err
		}
		tc.MarkTopicRecall(detail.Topic.ID)
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
		if tc.ConversationKind == memory.ConversationKindLife {
			return nil, fmt.Errorf("跨会话检索需要同时给 conversation_kind 和 target_id")
		}
		kind = tc.ConversationKind
	}
	from, until, err := schedule.ParseRange(input.From, input.To)
	if err != nil {
		return nil, err
	}
	page, err := tc.MemoryMgr.SearchHistoryV2(ctx, memory.HistoryQueryV2{
		ConversationKind: kind, TargetID: target, UserID: input.UserID,
		Keywords: input.Keywords, Match: input.Match, ExcludeKeywords: input.ExcludeKeywords,
		From: from, To: until, MaxMessageLogID: historyMaxMessageLogID(tc),
		Cursor: input.Cursor, Limit: input.Limit,
	})
	if err != nil {
		return nil, err
	}
	messages := make([]HistoryMessageView, 0, len(page.Items))
	for _, row := range page.Items {
		if tc.Session != nil && readableMediaScope(tc, row.ConversationKind, row.TargetID) {
			tc.Session.RegisterMediaRefs(row.MediaRefs)
		}
		messages = append(messages, historyViewPreview(row, ctx, tc))
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
	if input.AroundMessageID == 0 && input.TopicID == 0 {
		return nil, fmt.Errorf("必须提供 around_message_id 或 topic_id")
	}
	if input.AroundMessageID != 0 && input.TopicID != 0 {
		return nil, fmt.Errorf("按消息和按话题读取只能选择一个")
	}
	kind, target := input.ConversationKind, input.TargetID
	if kind == "" && target == 0 && (input.AroundMessageID != 0 || input.TopicID != 0) {
		if tc.ConversationKind == memory.ConversationKindLife {
			return nil, fmt.Errorf("跨会话读取需要同时给 conversation_kind 和 target_id")
		}
		kind, target = tc.ConversationKind, tc.TargetID
	}
	if tc.CurrentConversationOnly {
		if kind != "" && kind != tc.ConversationKind || target != 0 && target != tc.TargetID {
			return nil, fmt.Errorf("压缩器只能读取当前会话")
		}
		kind, target = tc.ConversationKind, tc.TargetID
	} else if kind == "" && target > 0 {
		if tc.ConversationKind == memory.ConversationKindLife {
			return nil, fmt.Errorf("跨会话读取需要同时给 conversation_kind 和 target_id")
		}
		kind = tc.ConversationKind
	}
	result, err := tc.MemoryMgr.ReadHistoryRange(ctx, memory.HistoryRange{
		ConversationKind: kind, TargetID: target, AroundMessageID: input.AroundMessageID,
		TopicID: input.TopicID, MaxMessageLogID: historyMaxMessageLogID(tc),
		Before: input.Before, After: input.After,
	})
	if err != nil {
		return nil, err
	}
	messages := make([]HistoryMessageView, 0, len(result.Messages))
	for _, row := range result.Messages {
		if tc.Session != nil && readableMediaScope(tc, row.ConversationKind, row.TargetID) {
			tc.Session.RegisterMediaRefs(row.MediaRefs)
		}
		messages = append(messages, historyViewFor(ctx, tc, row))
	}
	output := map[string]any{"success": true, "messages": messages}
	if input.TopicID != 0 {
		output["source_total"] = result.SourceTotal
		output["source_available"] = result.SourceAvailable
		output["source_expired"] = result.SourceTotal - result.SourceAvailable
	}
	return output, nil
}

func historyView(row memory.MessageLog) HistoryMessageView {
	return HistoryMessageView{
		MessageID: row.OneBotMessageID, ConversationKind: row.ConversationKind, TargetID: row.TargetID,
		UserID: row.UserID, Nickname: row.Nickname, Time: row.MessageTime,
		Content: row.TextContent, DisplayContent: row.DisplayContent, Files: row.FileRefs, RecalledAt: row.RecalledAt,
	}
}

// historyViewFor 在历史结果中补齐可用的 file_ref/image_ref，旧 locator 不重写消息正文
func historyViewFor(ctx context.Context, tc *ToolContext, row memory.MessageLog) HistoryMessageView {
	view := historyView(row)
	if tc == nil || tc.MemoryMgr == nil || tc.MemoryMgr.RefStore() == nil {
		return view
	}
	store := tc.MemoryMgr.RefStore()
	for index := range view.Files {
		file := &view.Files[index]
		if file.SnapshotRef != "" {
			if _, err := store.Get(file.SnapshotRef); err == nil {
				continue
			}
			file.SnapshotRef = ""
		}
		if strings.TrimSpace(file.FileID) == "" {
			continue
		}
		ref, err := store.CreatePending(file.Name, refstore.Locator{
			ConversationKind: row.ConversationKind, TargetID: row.TargetID,
			FileID: file.FileID, FileHash: file.FileHash, BusID: file.BusID, Name: file.Name, Size: file.Size,
		})
		if err == nil {
			file.SnapshotRef = ref.FileRef()
		}
	}
	for _, item := range row.MediaRefs {
		view.Images = append(view.Images, HistoryImage{Mime: item.Mime, Size: item.Bytes, Sticker: item.Sticker})
	}
	for index := range view.Images {
		item := &row.MediaRefs[index]
		imageRef := item.ImageRef
		if imageRef != "" {
			if _, err := store.Get(imageRef); err != nil {
				imageRef = ""
			}
		}
		if imageRef == "" && item.Path != "" {
			name := item.Short + ".webp"
			if item.Sticker {
				name = item.Short + filepath.Ext(item.Path)
			}
			if ref, err := store.CreateReadyFromFile(ctx, name, item.Path, 10*1024*1024, nil); err == nil {
				imageRef = ref.ImageRef()
			}
		}
		view.Images[index].ImageRef = imageRef
		if item.Sticker && imageRef != "" {
			_ = store.MarkSticker(imageRef)
		}
	}
	return view
}

// historyMaxMessageLogID 把本轮读取快照应用到任意会话；空快照与未设上限是两回事
func historyMaxMessageLogID(tc *ToolContext) *uint {
	if tc == nil || !tc.SnapshotLogIDSet {
		return nil
	}
	id := tc.SnapshotLogID
	return &id
}

// readableMediaScope 判断返回的历史媒体能否登记到当前会话的媒体索引：
// 当前会话自己的记录，或 life 跨会话读取到的记录
func readableMediaScope(tc *ToolContext, kind string, targetID int64) bool {
	if tc == nil {
		return false
	}
	if tc.ConversationKind == memory.ConversationKindLife {
		return true
	}
	return kind == tc.ConversationKind && targetID == tc.TargetID
}

func historyViewPreview(row memory.MessageLog, ctx context.Context, tc *ToolContext) HistoryMessageView {
	view := historyViewFor(ctx, tc, row)
	view.Content = truncateToolLogString(view.Content, 300)
	view.DisplayContent = truncateToolLogString(view.DisplayContent, 300)
	if view.DisplayContent == view.Content {
		view.DisplayContent = ""
	}
	return view
}

func NewSearchMemoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("searchMemory", "按关键词、主体、类型和更新时间检索长期记忆；默认精确子串，mode=fuzzy 用相似度召回；支持 or/and 和 cursor 翻页。", searchMemoryV2)
}

func NewReadMemoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("readMemory", "按编号完整读取长期记忆正文。", readMemoryV2)
}

func NewManageMemoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("manageMemory", "批量创建、更新或归档长期记忆；归档后不可恢复；至少1项，全部成功或全部回滚。", manageMemoryV2)
}

func NewSearchTopicsTool() (tool.InvokableTool, error) {
	return utils.InferTool("searchTopics", "按关键词和相似度检索话题，默认搜索全局群聊与私聊并返回来源；可用 conversation_kind/target_id 收窄来源。它只找话题，读完用 readTopic 或 readHistory。", searchTopics)
}

func NewReadTopicTool() (tool.InvokableTool, error) {
	return utils.InferTool("readTopic", "读取一个话题的当前摘要、来源消息和关键消息。", readTopic)
}

func NewSearchHistoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("searchHistory", "检索原始消息，默认搜索全局群聊与私聊并返回来源；支持关键词、人物、会话、时间和 cursor。已知来源、日期或消息 ID 时不必先搜话题。", searchHistoryV2)
}

func NewReadHistoryToolV2() (tool.InvokableTool, error) {
	return utils.InferTool("readHistory", "按话题或 OneBot 消息 ID 二选一读取前后原文；省略会话范围时默认当前会话，命中跨会话来源时要明确带上来源。", readHistoryV2)
}
