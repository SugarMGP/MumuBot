package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type ListSessionsInput struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=默认12，最大99"`
}

type SessionSummary struct {
	SessionID string `json:"session_id"`
	Kind      string `json:"kind"`
	TargetID  int64  `json:"target_id"`
	Name      string `json:"name"`
	Active    bool   `json:"active"`
	Blocked   bool   `json:"blocked"`
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
	filter := "all"
	cursor, err := memory.DecodeCursor(input.Cursor)
	if err != nil {
		return nil, err
	}
	if cursor.FilterHash == "" {
		cursor.FilterHash = memory.CursorFilterHash(filter)
	}
	if err := memory.ValidateCursorFilter(cursor, filter); err != nil {
		return nil, err
	}
	kinds := []string{memory.ConversationKindGroup, memory.ConversationKindPrivate}
	start := 0
	switch cursor.Sort {
	case "", memory.ConversationKindGroup:
	case memory.ConversationKindPrivate:
		start = 1
	default:
		return nil, memory.ErrInvalidCursor
	}
	snapshot := memory.CursorTimeOrNow(cursor)
	lastActive := cursor.LastKey > 0
	lastTarget := int64(cursor.LastID)
	sessions := make([]SessionSummary, 0, limit+1)
	hasMore := false
	for index := start; index < len(kinds); index++ {
		remaining := limit + 1 - len(sessions)
		if remaining <= 0 {
			break
		}
		kind := kinds[index]
		applyCursor := index == start && cursor.LastID > 0
		if !applyCursor {
			lastActive, lastTarget = false, 0
		}
		rows, err := tc.MemoryMgr.ListConversationTargetsAfter(ctx, kind, false, snapshot, lastActive, lastTarget, remaining)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			sessions = append(sessions, SessionSummary{
				SessionID: kind + ":" + strconv.FormatInt(row.TargetID, 10), Kind: kind,
				TargetID: row.TargetID, Name: row.Name, Active: row.Active, Blocked: row.Blocked,
			})
		}
		if len(sessions) > limit {
			hasMore = true
			sessions = sessions[:limit]
			break
		}
	}

	out := &ListSessionsOutput{Success: true, Sessions: sessions, HasMore: hasMore}
	if hasMore && len(sessions) > 0 {
		last := sessions[len(sessions)-1]
		active := 0.0
		if last.Active {
			active = 1
		}
		out.NextCursor = memory.NewPageCursor(last.Kind, active, time.Time{}, uint(last.TargetID), snapshot, filter)
	}
	return out, nil
}

func NewListSessionsTool() (tool.InvokableTool, error) {
	return utils.InferTool("listSessions", "列出群聊和私聊会话的元数据；只返回名称和状态，不返回聊天正文。", listSessions)
}

type SendSessionMessageInput struct {
	TargetSessionID string `json:"target_session_id" jsonschema:"description=例如 group:123 或 private:456"`
	Text            string `json:"text" jsonschema:"description=自然语言消息"`
}

type SendSessionMessageOutput struct {
	Success   bool `json:"success"`
	MessageID uint `json:"message_id"`
}

func parseSessionID(raw string) (string, int64, error) {
	kind, rawTarget, ok := strings.Cut(strings.TrimSpace(raw), ":")
	kind = strings.TrimSpace(kind)
	if !ok || (kind != memory.ConversationKindGroup && kind != memory.ConversationKindPrivate) {
		return "", 0, fmt.Errorf("目标会话格式应为 group:ID 或 private:ID")
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
	return utils.InferTool("sendSessionMessage", "向另一个 active 且未 blocked 的群聊或私聊会话投递一条自然语言消息；只发送必要信息，不读取目标会话的聊天正文。", sendSessionMessage)
}
