package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/refstore"
	"mumu-bot/internal/schedule"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

type ReadSessionHistoryInput struct {
	SessionID string `json:"session_id" jsonschema:"description=例如 group:123、private:456、worker:1 或 life:1"`
	BeforeID  uint   `json:"before_id,omitempty" jsonschema:"description=向前翻页的 transcript 条目 ID，0 表示最新"`
	From      string `json:"from,omitempty" jsonschema:"description=条目时间下限，格式 2006-01-02 或 RFC3339"`
	To        string `json:"to,omitempty" jsonschema:"description=条目时间上限，纯日期包含当天，格式 2006-01-02 或 RFC3339"`
	Limit     int    `json:"limit,omitempty" jsonschema:"description=默认20，最大50"`
}

type SessionHistoryEntry struct {
	ID          uint                   `json:"id"`
	At          time.Time              `json:"at"`
	StartedAt   time.Time              `json:"started_at"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	Role        string                 `json:"role"`
	RunID       string                 `json:"run_id,omitempty"`
	Text        string                 `json:"text"`
	TextTail    string                 `json:"text_tail,omitempty"`
	TextLength  int                    `json:"text_length"`
	Truncated   bool                   `json:"truncated"`
	ToolName    string                 `json:"tool_name,omitempty"`
	ToolStatus  string                 `json:"tool_status,omitempty"`
	CallID      string                 `json:"call_id,omitempty"`
	Arguments   string                 `json:"arguments,omitempty"`
	ToolCalls   []SessionEntryToolCall `json:"tool_calls,omitempty"`
	CallCount   int                    `json:"call_count,omitempty"`
	ImageCount  int                    `json:"image_count,omitempty"`
	FileCount   int                    `json:"file_count,omitempty"`
	ResultRef   string                 `json:"result_ref,omitempty"`
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
	RunningTools   []SessionHistoryEntry        `json:"running_tools"`
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
	return ReadSessionHistory(ctx, tc.MemoryMgr, input)
}

// ReadSessionHistory 共用会话工具与后台的只读历史投影
func ReadSessionHistory(ctx context.Context, mem *memory.Manager, input *ReadSessionHistoryInput) (*ReadSessionHistoryOutput, error) {
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
	target, err := mem.GetConversationTarget(ctx, kind, targetID)
	if err != nil {
		return nil, fmt.Errorf("读取会话失败: %w", err)
	}
	name := strings.TrimSpace(target.Name)
	if name == "" {
		name = fmt.Sprintf("%s:%d", kind, targetID)
	}
	pending, err := mem.PendingInputs(ctx, kind, targetID)
	if err != nil {
		return nil, err
	}
	pendingCount := len(pending)
	pendingEntries := make([]SessionHistoryPendingEntry, 0, len(pending))
	pendingIDs := make(map[uint]bool, len(pending))
	for _, row := range pending {
		pendingIDs[row.ID] = true
		pendingEntries = append(pendingEntries, SessionHistoryPendingEntry{
			ID: row.ID, At: row.CreatedAt, Role: row.EntryKind,
			Text: truncateHistoryText(decodeTranscriptText(row.Payload)), InputSeq: row.InputSeq,
		})
	}
	from, until, err := schedule.ParseRange(input.From, input.To)
	if err != nil {
		return nil, err
	}
	recent, err := mem.RecentVisibleTranscript(ctx, kind, targetID, input.BeforeID, from, until, limit+1)
	if err != nil {
		return nil, err
	}
	hasMore := len(recent) > limit
	if hasMore {
		recent = recent[:limit]
	}
	entries := make([]SessionHistoryEntry, 0, len(recent))
	seenEntries := make(map[uint]bool, len(recent))
	for _, row := range recent {
		if pendingIDs[row.ID] {
			continue
		}
		seenEntries[row.ID] = true
		entries = append(entries, historyEntryFor(row))
	}
	runningRows, err := mem.RunningToolTranscripts(ctx, kind, targetID, 20)
	if err != nil {
		return nil, err
	}
	running := make([]SessionHistoryEntry, 0, len(runningRows))
	for _, row := range runningRows {
		if seenEntries[row.ID] {
			continue
		}
		seenEntries[row.ID] = true
		running = append(running, historyEntryFor(row))
	}
	out := &ReadSessionHistoryOutput{
		Success: true, SessionID: input.SessionID, Kind: kind, Name: name,
		Entries: entries, RunningTools: running, PendingEntries: pendingEntries,
		PendingCount: pendingCount, HasMore: hasMore,
	}
	if len(entries) > 0 {
		out.NextBeforeID = entries[len(entries)-1].ID
	}
	return out, nil
}

func truncateHistoryText(text string) string {
	preview, _, _, _ := historyText(text)
	return preview
}

// historyText 同时保留预览头部、原文长度与长输出尾部错误
func historyText(text string) (string, string, int, bool) {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	total := len(runes)
	if total <= 500 {
		return text, "", total, false
	}
	tailStart := total - 300
	if tailStart < 500 {
		tailStart = 500
	}
	return string(runes[:500]) + "…", string(runes[tailStart:]), total, true
}

// historyEntryFor 把 transcript 行投影成可供 life 判断进度的摘要
func historyEntryFor(row memory.TranscriptEntry) SessionHistoryEntry {
	files, images := refCounts(row.Payload)
	preview, tail, total, truncated := historyText(decodeTranscriptText(row.Payload))
	entry := SessionHistoryEntry{
		ID: row.ID, At: row.CreatedAt, StartedAt: row.CreatedAt,
		Role: row.EntryKind, RunID: row.RunID,
		Text: preview, TextTail: tail, TextLength: total, Truncated: truncated,
		ToolName: row.ToolName, ToolStatus: row.ToolStatus,
		CallID: row.ToolCallID, Arguments: truncateHistoryArgs(transcriptToolArguments(row.Payload)),
		ImageCount: images, FileCount: files, ResultRef: row.ResultRef,
	}
	if row.EntryKind == "assistant" {
		entry.ToolCalls = transcriptToolCalls(row.Payload)
		for index := range entry.ToolCalls {
			entry.ToolCalls[index].Arguments = truncateHistoryArgs(entry.ToolCalls[index].Arguments)
		}
		entry.CallCount = len(entry.ToolCalls)
	}
	if completed := transcriptCompletedAt(row.Payload); completed != nil {
		entry.CompletedAt = completed
		if row.EntryKind == "tool" {
			entry.At = *completed
		}
	}
	return entry
}

func truncateHistoryArgs(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= 300 {
		return text
	}
	return string(runes[:300]) + "…"
}

func transcriptToolArguments(payload []byte) string {
	var message struct {
		Extra map[string]any `json:"extra"`
	}
	if err := sonic.Unmarshal(payload, &message); err != nil {
		return ""
	}
	value, _ := message.Extra["tool_arguments"].(string)
	return strings.TrimSpace(value)
}

func transcriptCompletedAt(payload []byte) *time.Time {
	var message struct {
		Extra map[string]any `json:"extra"`
	}
	if err := sonic.Unmarshal(payload, &message); err != nil {
		return nil
	}
	raw, _ := message.Extra["completed_at"].(string)
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	completed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return &completed
}

func refCounts(payload []byte) (int, int) {
	files := map[string]bool{}
	images := map[string]bool{}
	for _, token := range refstore.ExtractRefs(string(payload)) {
		if strings.HasPrefix(token, "image_") {
			images[token] = true
		} else if strings.HasPrefix(token, "file_") {
			files[token] = true
		}
	}
	return len(files), len(images)
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

type ReadSessionEntryInput struct {
	SessionID string `json:"session_id" jsonschema:"description=例如 group:123、private:456、worker:1 或 life:1"`
	EntryID   uint   `json:"entry_id" jsonschema:"description=readSessionHistory 返回的条目 ID"`
	Offset    int    `json:"offset,omitempty" jsonschema:"description=按字符读取的起点，默认 0"`
	Length    int    `json:"length,omitempty" jsonschema:"description=读取字符数，默认 4000，最大 8000"`
}

type SessionEntryToolCall struct {
	Name      string `json:"name"`
	CallID    string `json:"call_id,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type ReadSessionEntryOutput struct {
	Success     bool                   `json:"success"`
	SessionID   string                 `json:"session_id"`
	EntryID     uint                   `json:"entry_id"`
	Kind        string                 `json:"kind"`
	At          time.Time              `json:"at"`
	StartedAt   time.Time              `json:"started_at"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	RunID       string                 `json:"run_id,omitempty"`
	ToolName    string                 `json:"tool_name,omitempty"`
	ToolStatus  string                 `json:"tool_status,omitempty"`
	CallID      string                 `json:"call_id,omitempty"`
	Arguments   string                 `json:"arguments,omitempty"`
	ImageCount  int                    `json:"image_count,omitempty"`
	FileCount   int                    `json:"file_count,omitempty"`
	ResultRef   string                 `json:"result_ref,omitempty"`
	ToolCalls   []SessionEntryToolCall `json:"tool_calls,omitempty"`
	Text        string                 `json:"text"`
	Total       int                    `json:"total"`
	Complete    bool                   `json:"complete"`
	NextOffset  int                    `json:"next_offset,omitempty"`
}

func readSessionEntry(ctx context.Context, input *ReadSessionEntryInput) (*ReadSessionEntryOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("会话历史能力未初始化"))
	}
	return ReadSessionEntry(ctx, tc.MemoryMgr, input)
}

// ReadSessionEntry 共用会话工具与后台的只读正文窗口
func ReadSessionEntry(ctx context.Context, mem *memory.Manager, input *ReadSessionEntryInput) (*ReadSessionEntryOutput, error) {
	if input == nil || input.EntryID == 0 {
		return nil, fmt.Errorf("需要 entry_id")
	}
	kind, targetID, err := parseSessionID(input.SessionID)
	if err != nil {
		return nil, err
	}
	row, err := mem.GetTranscript(ctx, input.EntryID)
	if err != nil {
		return nil, fmt.Errorf("读取条目失败: %w", err)
	}
	if row.ConversationKind != kind || row.TargetID != targetID {
		return nil, fmt.Errorf("条目不属于指定会话")
	}
	text := decodeTranscriptText(row.Payload)
	runes := []rune(text)
	total := len(runes)
	offset := input.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	length := input.Length
	if length <= 0 {
		length = 4000
	}
	if length > 8000 {
		length = 8000
	}
	end := offset + length
	if end > total {
		end = total
	}
	at := row.CreatedAt
	completed := transcriptCompletedAt(row.Payload)
	if completed != nil && row.EntryKind == "tool" {
		at = *completed
	}
	files, images := refCounts(row.Payload)
	out := &ReadSessionEntryOutput{
		Success: true, SessionID: input.SessionID, EntryID: row.ID, Kind: row.EntryKind,
		At: at, StartedAt: row.CreatedAt, CompletedAt: completed, RunID: row.RunID,
		ToolName: row.ToolName, ToolStatus: row.ToolStatus, CallID: row.ToolCallID,
		Arguments:  transcriptToolArguments(row.Payload),
		ImageCount: images, FileCount: files, ResultRef: row.ResultRef,
		Text: string(runes[offset:end]), Total: total, Complete: end >= total,
	}
	if !out.Complete {
		out.NextOffset = end
	}
	out.ToolCalls = transcriptToolCalls(row.Payload)
	return out, nil
}

// transcriptToolCalls 提取 assistant 轨迹里的工具调用名称与参数，不倾倒媒体内容
func transcriptToolCalls(payload []byte) []SessionEntryToolCall {
	var msg schema.AgenticMessage
	if err := sonic.Unmarshal(payload, &msg); err != nil {
		return nil
	}
	calls := make([]SessionEntryToolCall, 0)
	for _, block := range msg.ContentBlocks {
		if block == nil || block.FunctionToolCall == nil {
			continue
		}
		calls = append(calls, SessionEntryToolCall{
			Name: block.FunctionToolCall.Name, CallID: block.FunctionToolCall.CallID,
			Arguments: block.FunctionToolCall.Arguments,
		})
	}
	return calls
}

func NewReadSessionEntryTool() (tool.InvokableTool, error) {
	return utils.InferTool("readSessionEntry", "读取指定会话里单条 transcript 的完整正文，按字符窗口返回；readSessionHistory 的预览不完整时用它。result_ref 指向的大工具结果仍用 readToolResult。", readSessionEntry)
}

func NewReadSessionHistoryTool() (tool.InvokableTool, error) {
	return utils.InferTool("readSessionHistory", "读取指定会话的 transcript 记录，可用 from/to 限定条目时间；pending_entries 是当前全部已入队但尚未处理的消息，running_tools 单独列出尚未结束的长任务；entries 返回 assistant 工具调用、CallID、run_id、正文长度/截断状态、图片数量和 started/completed；终态工具按 completed_at 计活动时间，长输出同时给 text_tail 保留尾部错误；工具参数在列表里截断，完整参数和全文用 readSessionEntry；大工具结果的 result_ref 可直接交给 readToolResult。", readSessionHistory)
}
