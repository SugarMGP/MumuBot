package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

type ListSessionsInput struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=默认12，最大99"`
}

type SessionSummary struct {
	SessionID string    `json:"session_id"`
	Kind      string    `json:"kind"`
	TargetID  int64     `json:"target_id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	LastError string    `json:"last_error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ListSessionsOutput struct {
	Success    bool             `json:"success"`
	Sessions   []SessionSummary `json:"sessions"`
	NextCursor string           `json:"next_cursor,omitempty"`
	HasMore    bool             `json:"has_more"`
}

func listSessions(ctx context.Context, input *ListSessionsInput) (*ListSessionsOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("会话列表能力未初始化"))
	}
	if input == nil {
		input = &ListSessionsInput{}
	}
	limit := memory.NormalizeLimit(input.Limit)
	filter := memory.SessionDirectoryFilter
	cursor, err := memory.DecodeCursor(input.Cursor)
	if err != nil {
		return nil, err
	}
	if cursor.Sort != "" && cursor.Sort != "directory" {
		return nil, memory.ErrInvalidCursor
	}
	if cursor.FilterHash == "" {
		cursor.FilterHash = memory.CursorFilterHash(filter)
	}
	if err := memory.ValidateCursorFilter(cursor, filter); err != nil {
		return nil, err
	}
	snapshot := memory.CursorTimeOrNow(cursor)
	rows, err := tc.MemoryMgr.ListSessionDirectory(ctx, snapshot, cursor.LastTime, int(cursor.LastKey), cursor.LastID, limit+1)
	if err != nil {
		return nil, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	sessions := make([]SessionSummary, 0, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row.Name)
		if name == "" {
			name = fmt.Sprintf("%s:%d", row.Kind, row.TargetID)
		}
		status := row.WorkerStatus
		if status == "" {
			switch {
			case row.Blocked:
				status = "blocked"
			case !row.Active:
				status = "inactive"
			default:
				status = "active"
			}
		}
		sessions = append(sessions, SessionSummary{
			SessionID: row.Kind + ":" + strconv.FormatInt(row.TargetID, 10),
			Kind:      row.Kind, TargetID: row.TargetID, Name: name,
			Status: status, LastError: row.WorkerLastError, UpdatedAt: row.UpdatedAt,
		})
	}
	out := &ListSessionsOutput{Success: true, Sessions: sessions, HasMore: hasMore}
	if hasMore && len(sessions) > 0 {
		last := sessions[len(sessions)-1]
		out.NextCursor = memory.NewPageCursor("directory", float64(sessionKindRank(last.Kind)), last.UpdatedAt, uint(last.TargetID), snapshot, filter)
	}
	return out, nil
}

func sessionKindRank(kind string) int {
	switch kind {
	case memory.ConversationKindGroup:
		return 0
	case memory.ConversationKindPrivate:
		return 1
	default:
		return 2
	}
}

func NewListSessionsTool() (tool.InvokableTool, error) {
	return utils.InferTool("listSessions", "列出所有群聊、私聊和当前 worker 池的元数据，包括类型、名称、状态、失败原因和更新时间；不返回聊天正文。", listSessions)
}

type SendSessionMessageInput struct {
	TargetSessionID string `json:"target_session_id" jsonschema:"description=例如 group:123、private:456 或 worker:1"`
	Text            string `json:"text" jsonschema:"description=自然语言消息，最多2000字"`
}

type SendSessionMessageOutput struct {
	Success   bool `json:"success"`
	MessageID uint `json:"message_id"`
}

func parseSessionID(raw string) (string, int64, error) {
	kind, rawTarget, ok := strings.Cut(strings.TrimSpace(raw), ":")
	kind = strings.TrimSpace(kind)
	if !ok || (kind != memory.ConversationKindGroup && kind != memory.ConversationKindPrivate && kind != memory.ConversationKindWorker) {
		return "", 0, fmt.Errorf("目标会话格式应为 group:ID、private:ID 或 worker:ID")
	}
	targetID, err := strconv.ParseInt(strings.TrimSpace(rawTarget), 10, 64)
	if err != nil || targetID <= 0 {
		return "", 0, fmt.Errorf("目标会话 ID 无效")
	}
	return kind, targetID, nil
}

func sendSessionMessage(ctx context.Context, input *SendSessionMessageInput) (*SendSessionMessageOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("会话通信能力未初始化"))
	}
	if input == nil || strings.TrimSpace(input.Text) == "" {
		return nil, fmt.Errorf("消息正文不能为空")
	}
	if utf8.RuneCountInString(strings.TrimSpace(input.Text)) > memory.MaxSessionMessageRunes {
		return nil, fmt.Errorf("消息正文不能超过 %d 字符", memory.MaxSessionMessageRunes)
	}
	kind, targetID, err := parseSessionID(input.TargetSessionID)
	if err != nil {
		return nil, err
	}
	if tc.SessionMessageCallback == nil {
		return nil, NewTerminalToolError(fmt.Errorf("会话消息投递能力未初始化"))
	}
	row, err := tc.SessionMessageCallback(ctx, tc.ConversationKind, tc.TargetID, kind, targetID, input.Text)
	if err != nil {
		return nil, err
	}
	return &SendSessionMessageOutput{Success: true, MessageID: row.ID}, nil
}

func NewSendSessionMessageTool() (tool.InvokableTool, error) {
	return utils.InferTool("sendSessionMessage", "向另一个会话投递一条消息。", sendSessionMessage)
}

type ReadSessionHistoryInput struct {
	SessionID string `json:"session_id" jsonschema:"description=例如 group:123、private:456 或 worker:1"`
	BeforeID  uint   `json:"before_id,omitempty" jsonschema:"description=向前翻页的 transcript 条目 ID，0 表示最新"`
	Limit     int    `json:"limit,omitempty" jsonschema:"description=默认20，最大50"`
}

type SessionHistoryEntry struct {
	ID         uint      `json:"id"`
	At         time.Time `json:"at"`
	Role       string    `json:"role"`
	Text       string    `json:"text"`
	ToolName   string    `json:"tool_name,omitempty"`
	ToolStatus string    `json:"tool_status,omitempty"`
}

type SessionHistoryPendingEntry struct {
	ID       uint      `json:"id"`
	At       time.Time `json:"at"`
	Role     string    `json:"role"`
	Text     string    `json:"text"`
	InputSeq *uint64   `json:"input_seq,omitempty"`
}

type ReadSessionHistoryOutput struct {
	Success        bool                         `json:"success"`
	SessionID      string                       `json:"session_id"`
	Kind           string                       `json:"kind"`
	Name           string                       `json:"name"`
	Entries        []SessionHistoryEntry        `json:"entries"`
	PendingEntries []SessionHistoryPendingEntry `json:"pending_entries"`
	PendingCount   int                          `json:"pending_count"`
	HasMore        bool                         `json:"has_more"`
	NextBeforeID   uint                         `json:"next_before_id,omitempty"`
}

func readSessionHistory(ctx context.Context, input *ReadSessionHistoryInput) (*ReadSessionHistoryOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("会话历史能力未初始化"))
	}
	if input == nil {
		return nil, fmt.Errorf("需要 session_id")
	}
	kind, targetID, err := parseSessionID(input.SessionID)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	target, err := tc.MemoryMgr.GetConversationTarget(ctx, kind, targetID)
	if err != nil {
		return nil, fmt.Errorf("读取会话失败: %w", err)
	}
	name := strings.TrimSpace(target.Name)
	if name == "" {
		name = fmt.Sprintf("%s:%d", kind, targetID)
	}
	pending, err := tc.MemoryMgr.PendingInputs(ctx, kind, targetID)
	if err != nil {
		return nil, err
	}
	pendingCount := len(pending)
	pendingEntries := make([]SessionHistoryPendingEntry, 0, len(pending))
	pendingIDs := make(map[uint]bool, len(pending))
	for _, row := range pending {
		pendingIDs[row.ID] = true
		pendingEntries = append(pendingEntries, SessionHistoryPendingEntry{
			ID: row.ID, At: row.CreatedAt, Role: historyRole(row.EntryKind),
			Text: truncateHistoryText(decodeTranscriptText(row.Payload)), InputSeq: row.InputSeq,
		})
	}
	recent, err := tc.MemoryMgr.RecentVisibleTranscript(ctx, kind, targetID, input.BeforeID, limit+1)
	if err != nil {
		return nil, err
	}
	hasMore := len(recent) > limit
	if hasMore {
		recent = recent[:limit]
	}
	entries := make([]SessionHistoryEntry, 0, len(recent))
	for _, row := range recent {
		if pendingIDs[row.ID] {
			continue
		}
		entries = append(entries, SessionHistoryEntry{
			ID: row.ID, At: row.CreatedAt, Role: historyRole(row.EntryKind),
			Text:     truncateHistoryText(decodeTranscriptText(row.Payload)),
			ToolName: row.ToolName, ToolStatus: row.ToolStatus,
		})
	}
	out := &ReadSessionHistoryOutput{
		Success: true, SessionID: input.SessionID, Kind: kind, Name: name,
		Entries: entries, PendingEntries: pendingEntries, PendingCount: pendingCount, HasMore: hasMore,
	}
	if len(entries) > 0 {
		out.NextBeforeID = entries[len(entries)-1].ID
	}
	return out, nil
}

func historyRole(entryKind string) string {
	switch entryKind {
	case "agent_message":
		return "agent_message"
	case "system_note":
		return "system_note"
	default:
		return entryKind
	}
}

func truncateHistoryText(text string) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= 500 {
		return text
	}
	runes := []rune(text)
	return string(runes[:500]) + "…"
}

func decodeTranscriptText(payload []byte) string {
	var msg schema.AgenticMessage
	if err := sonic.Unmarshal(payload, &msg); err != nil {
		return ""
	}
	var parts []string
	for _, block := range msg.ContentBlocks {
		if block == nil {
			continue
		}
		if block.UserInputText != nil && block.UserInputText.Text != "" {
			parts = append(parts, block.UserInputText.Text)
		}
		if block.AssistantGenText != nil && block.AssistantGenText.Text != "" {
			parts = append(parts, block.AssistantGenText.Text)
		}
		if block.FunctionToolResult != nil {
			for _, content := range block.FunctionToolResult.Content {
				if content != nil && content.Text != nil && content.Text.Text != "" {
					parts = append(parts, content.Text.Text)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

func NewReadSessionHistoryTool() (tool.InvokableTool, error) {
	return utils.InferTool("readSessionHistory", "读取指定会话最近的 transcript 记录；pending_entries 是全部已经入队但尚未处理的消息，失败记录也会返回。", readSessionHistory)
}

type UpdateWorkerTitleInput struct {
	Title string `json:"title" jsonschema:"description=当前正在执行的任务标题，最多30个字"`
}

type UpdateWorkerTitleOutput struct {
	Success bool   `json:"success"`
	Title   string `json:"title"`
}

func updateWorkerTitle(ctx context.Context, input *UpdateWorkerTitleInput) (*UpdateWorkerTitleOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.UpdateWorkerTitleCallback == nil {
		return nil, NewTerminalToolError(fmt.Errorf("worker 标题能力未初始化"))
	}
	if tc.ConversationKind != memory.ConversationKindWorker {
		return nil, fmt.Errorf("只有 worker 可以更新标题")
	}
	if input == nil || strings.TrimSpace(input.Title) == "" {
		return nil, fmt.Errorf("标题不能为空")
	}
	title := strings.TrimSpace(input.Title)
	if len([]rune(title)) > memory.MaxWorkerTitleRunes {
		return nil, fmt.Errorf("标题不能超过 %d 个字", memory.MaxWorkerTitleRunes)
	}
	if err := tc.UpdateWorkerTitleCallback(ctx, title); err != nil {
		return nil, err
	}
	return &UpdateWorkerTitleOutput{Success: true, Title: title}, nil
}

func NewUpdateWorkerTitleTool() (tool.InvokableTool, error) {
	return utils.InferTool("updateWorkerTitle", "更新 worker 当前任务的标题。", updateWorkerTitle)
}
