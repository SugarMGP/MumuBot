package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type ListSessionsInput struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=默认12，最大99"`
}

type SessionSummary struct {
	SessionID     string     `json:"session_id"`
	Kind          string     `json:"kind"`
	TargetID      int64      `json:"target_id"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	LastError     string     `json:"last_error,omitempty"`
	PendingCount  int        `json:"pending_count"`
	Running       bool       `json:"running"`
	LastMessageAt *time.Time `json:"last_message_at,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
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
	viewerKind := tc.ConversationKind
	if viewerKind == "" {
		return nil, fmt.Errorf("当前会话类型未初始化")
	}
	filter := memory.SessionDirectoryFilterValue{
		Scope: memory.SessionDirectoryFilter, ViewerKind: viewerKind, ViewerTargetID: tc.TargetID,
	}
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
	selfID := int64(0)
	if tc.Bot != nil {
		selfID = tc.Bot.GetSelfID()
	}
	snapshot := memory.CursorTimeOrNow(cursor)
	rows, err := tc.MemoryMgr.ListSessionDirectory(ctx, memory.SessionDirectoryQuery{
		ViewerKind: viewerKind, ViewerTargetID: tc.TargetID, SelfUserID: selfID,
		Snapshot: snapshot, LastTime: cursor.LastTime, LastRank: int(cursor.LastKey),
		LastID: cursor.LastID, Limit: limit + 1,
	})
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
		running := false
		if tc.SessionRunningCallback != nil {
			running = tc.SessionRunningCallback(row.Kind, row.TargetID)
		}
		sessions = append(sessions, SessionSummary{
			SessionID: row.Kind + ":" + strconv.FormatInt(row.TargetID, 10),
			Kind:      row.Kind, TargetID: row.TargetID, Name: name,
			Status: status, LastError: row.WorkerLastError,
			PendingCount: row.PendingCount, Running: running,
			LastMessageAt: row.LastMessageAt, UpdatedAt: row.UpdatedAt,
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
	case memory.ConversationKindLife:
		return 2
	default:
		return 3
	}
}

func NewListSessionsTool() (tool.InvokableTool, error) {
	return utils.InferTool("listSessions", "按当前会话角色列出可见会话的元数据：类型、名称、状态、失败原因、待处理数量、是否正在运行和最近真人消息时间；不返回聊天正文。", listSessions)
}

type SendSessionMessageInput struct {
	TargetSessionID string `json:"target_session_id" jsonschema:"description=例如 life:1、group:123、private:456 或 worker:1"`
	Text            string `json:"text" jsonschema:"description=自然语言消息，最多2000字"`
	Wake            *bool  `json:"wake,omitempty" jsonschema:"description=是否请求目标立即处理，默认 true；false 表示只保存，等目标下次正常运行时再读"`
	ContextMode     string `json:"context_mode,omitempty" jsonschema:"description=仅 life 派 worker 时使用：continue（默认）或 new（从这条派工开始新上下文）"`
}

type SendSessionMessageOutput struct {
	Success   bool `json:"success"`
	MessageID uint `json:"message_id"` // 目标 transcript 条目编号，不是 QQ 消息 ID
}

func parseSessionID(raw string) (string, int64, error) {
	kind, rawTarget, ok := strings.Cut(strings.TrimSpace(raw), ":")
	kind = strings.TrimSpace(kind)
	if !ok || (kind != memory.ConversationKindGroup && kind != memory.ConversationKindPrivate && kind != memory.ConversationKindWorker && kind != memory.ConversationKindLife) {
		return "", 0, fmt.Errorf("目标会话格式应为 life:1、group:ID、private:ID 或 worker:ID")
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
	wake := true
	if input.Wake != nil {
		wake = *input.Wake
	}
	mode := strings.ToLower(strings.TrimSpace(input.ContextMode))
	if mode == "" {
		mode = "continue"
	}
	if mode != "continue" && mode != "new" {
		return nil, fmt.Errorf("context_mode 只能是 continue 或 new")
	}
	if mode == "new" {
		if tc.ConversationKind != memory.ConversationKindLife || kind != memory.ConversationKindWorker {
			return nil, fmt.Errorf("只有 life 向 worker 派工时可以选择新上下文")
		}
		if tc.SessionMessageContextCallback == nil {
			return nil, NewTerminalToolError(fmt.Errorf("worker 新上下文能力未初始化"))
		}
		row, err := tc.SessionMessageContextCallback(ctx, tc.ConversationKind, tc.TargetID, kind, targetID, input.Text, wake, mode)
		if err != nil {
			return nil, err
		}
		return &SendSessionMessageOutput{Success: true, MessageID: row.ID}, nil
	}
	if tc.SessionMessageCallback == nil {
		return nil, NewTerminalToolError(fmt.Errorf("会话消息投递能力未初始化"))
	}
	row, err := tc.SessionMessageCallback(ctx, tc.ConversationKind, tc.TargetID, kind, targetID, input.Text, wake)
	if err != nil {
		return nil, err
	}
	return &SendSessionMessageOutput{Success: true, MessageID: row.ID}, nil
}

func NewSendSessionMessageTool() (tool.InvokableTool, error) {
	return utils.InferTool("sendSessionMessage", "向另一个会话持久投信；成功只代表入队，不代表已读、做完或发给真人。QQ 可以向 life 交工作、向其它 QQ 会话转必要消息，不能直接派 worker；worker 只回 life，life 可投任意可用会话。交事带目标、来源、材料、完成条件，回报带结果、验证、限制和产物 ref，别来回发送无新内容的确认。wake=false 只保存，不唤醒；wake=true 也不打断睡眠或绕过群门控。只有 life 派空闲且无待处理输入的 worker 可用 context_mode=new，默认 continue。回执 message_id 是内部条目号，不是 QQ 消息 ID。", sendSessionMessage)
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
