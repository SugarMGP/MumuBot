package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"go.uber.org/zap"
)

// SpeakCallback 发言回调函数类型
type SpeakCallback func(ctx context.Context, targetID int64, content string, replyTo int64, mentions []int64) error

// SendStickerCallback 发送表情包回调函数类型
type SendStickerCallback func(ctx context.Context, targetID int64, fileName string, description string) error
type SendImageURLCallback func(ctx context.Context, imageURL string) error
type GetNewMessagesCallback func(ctx context.Context) (any, error)
type SessionMessageCallback func(ctx context.Context, fromKind string, fromTargetID int64, toKind string, toTargetID int64, text string) (*memory.SessionMessage, error)

// ToolContext 工具执行上下文
type ToolContext struct {
	ConversationKind        string
	TargetID                int64
	MemoryMgr               *memory.Manager
	Bot                     *onebot.Client
	SnapshotLogID           uint
	RunID                   string
	VisibleTranscriptIDs    []uint
	InjectedArrivalSeq      uint64
	CompactionAttempts      int
	CurrentConversationOnly bool
	RequestTokenEstimate    int
	MemoryReadVersions      map[uint]uint64
	ReferenceSeenPending    map[string]string
	SpeakCallback           SpeakCallback       // 发言回调
	SendStickerCallback     SendStickerCallback // 发送表情包回调
	SendImageURLCallback    SendImageURLCallback
	GetNewMessagesCallback  GetNewMessagesCallback
	SessionMessageCallback  SessionMessageCallback
	ReadForwardCallback     func(ctx context.Context, id string, index int) (any, error)
	GroupInfoCallback       func(ctx context.Context) string
	StickerDir              string
	Session                 SessionContext
	ThinkStartArrivalSeq    uint64
	ObservedThroughSeq      uint64
	RecallCallback          func(messageID int64, arrivalSeq uint64)

	memoryMu    sync.Mutex
	referenceMu sync.Mutex
}

// ctxKey 上下文键类型
type ctxKey string

const toolContextKey ctxKey = "tool_context"
const toolLogMaxLen = 1000

// WithToolContext 将工具上下文放入 context
func WithToolContext(ctx context.Context, tc *ToolContext) context.Context {
	return context.WithValue(ctx, toolContextKey, tc)
}

// GetToolContext 从 context 获取工具上下文
func GetToolContext(ctx context.Context) *ToolContext {
	if tc, ok := ctx.Value(toolContextKey).(*ToolContext); ok {
		return tc
	}
	return nil
}

func truncateToolLogString(raw string, max int) string {
	if max <= 0 {
		return ""
	}

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	runes := []rune(trimmed)
	if len(runes) <= max {
		return trimmed
	}
	return string(runes[:max]) + "...(truncated)"
}

// MarkMemoryRead 记录本轮实际读取过的记忆版本，供维护工具做并发校验
func (tc *ToolContext) MarkMemoryRead(id uint, version uint64) {
	if tc == nil || id == 0 || version == 0 {
		return
	}
	tc.memoryMu.Lock()
	defer tc.memoryMu.Unlock()
	if tc.MemoryReadVersions == nil {
		tc.MemoryReadVersions = map[uint]uint64{}
	}
	tc.MemoryReadVersions[id] = version
}

// MarkReferenceSeen 记录本轮工具实际展示过的参考项；模型请求成功后才由会话落库。
func (tc *ToolContext) MarkReferenceSeen(key, version string) {
	if tc == nil || key == "" || version == "" {
		return
	}
	tc.referenceMu.Lock()
	defer tc.referenceMu.Unlock()
	if tc.ReferenceSeenPending == nil {
		tc.ReferenceSeenPending = map[string]string{}
	}
	tc.ReferenceSeenPending[key] = version
}

// ReferenceSeenSnapshot 返回本轮待提交的参考回执。
func (tc *ToolContext) ReferenceSeenSnapshot() map[string]string {
	if tc == nil {
		return nil
	}
	tc.referenceMu.Lock()
	defer tc.referenceMu.Unlock()
	return tc.ReferenceSeenPending
}

// ClearReferenceSeen 在 receipt 成功落库后清空。
func (tc *ToolContext) ClearReferenceSeen() {
	if tc == nil {
		return
	}
	tc.referenceMu.Lock()
	defer tc.referenceMu.Unlock()
	tc.ReferenceSeenPending = nil
}

// MemoryVersion 读取本轮已经读取过的记忆版本
func (tc *ToolContext) MemoryVersion(id uint) (uint64, bool) {
	if tc == nil || id == 0 {
		return 0, false
	}
	tc.memoryMu.Lock()
	defer tc.memoryMu.Unlock()
	version, ok := tc.MemoryReadVersions[id]
	return version, ok
}

// LogToolCall 记录工具调用
func LogToolCall(toolName string, inputJSON string, outputJSON string, err error) {
	cfg := config.Snapshot()
	if cfg != nil && cfg.Debug.ShowToolCalls {
		inputJSON = truncateToolLogString(inputJSON, toolLogMaxLen)
		outputJSON = truncateToolLogString(outputJSON, toolLogMaxLen)
		if err != nil {
			zap.L().Debug("工具调用", zap.String("tool", toolName), zap.String("input", inputJSON), zap.String("output", outputJSON), zap.Error(err))
		} else {
			zap.L().Debug("工具调用", zap.String("tool", toolName), zap.String("input", inputJSON), zap.String("output", outputJSON))
		}
	}
}

// ==================== 获取群成员详情工具 ====================

// GetGroupMemberDetailInput 获取群成员详情的输入参数
type GetGroupMemberDetailInput struct {
	// UserID 要查询的群成员 QQ 号
	UserID int64 `json:"user_id" jsonschema:"description=要查询的群成员QQ号"`
}

// GetGroupMemberDetailOutput 获取群成员详情的输出
type GetGroupMemberDetailOutput struct {
	Success       bool   `json:"success"`
	Message       string `json:"message,omitempty"`
	UserID        int64  `json:"user_id,omitempty"`
	Nickname      string `json:"nickname,omitempty"`
	GroupNickname string `json:"group_nickname,omitempty"` // 群昵称
	Role          string `json:"role,omitempty"`           // 群主（owner）、管理员（admin）或成员（member）
	Title         string `json:"title,omitempty"`          // 专属头衔
	Level         string `json:"level,omitempty"`          // 群等级
	JoinTime      string `json:"join_time,omitempty"`      // 入群时间
	LastSentTime  string `json:"last_sent_time,omitempty"` // 最后发言时间
}

// getGroupMemberDetailFunc 获取群成员详情的实际实现
func getGroupMemberDetailFunc(ctx context.Context, input *GetGroupMemberDetailInput) (*GetGroupMemberDetailOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("机器人未连接"))
	}
	if input == nil || input.UserID == 0 {
		return nil, fmt.Errorf("用户 ID 不能为空")
	}

	info, err := tc.Bot.GetGroupMemberInfo(ctx, tc.TargetID, input.UserID, false)
	if err != nil {
		return nil, err
	}

	output := &GetGroupMemberDetailOutput{
		Success:       true,
		UserID:        info.UserID,
		Nickname:      info.Nickname,
		GroupNickname: info.Card,
		Role:          info.Role,
		Title:         info.Title,
		Level:         info.Level,
	}

	if info.JoinTime > 0 {
		output.JoinTime = time.Unix(info.JoinTime, 0).Format("2006-01-02 15:04:05")
	}
	if info.LastSentTime > 0 {
		output.LastSentTime = time.Unix(info.LastSentTime, 0).Format("2006-01-02 15:04:05")
	}

	return output, nil
}

// NewGetGroupMemberDetailTool 创建获取群成员详情工具
func NewGetGroupMemberDetailTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getGroupMemberDetail",
		"获取某个群成员的详细信息，包括群昵称、角色、头衔、等级、入群时间、最后发言时间等。",
		getGroupMemberDetailFunc,
	)
}

// ==================== 获取短期记忆工具 ====================

func NewGetNewMessagesTool() (tool.InvokableTool, error) {
	return utils.InferTool("getNewMessages", "读取上次读取之后新到的消息；不会等待。", func(ctx context.Context, _ *struct{}) (any, error) {
		tc := GetToolContext(ctx)
		if tc == nil || tc.GetNewMessagesCallback == nil {
			return nil, NewTerminalToolError(fmt.Errorf("新消息读取能力未初始化"))
		}
		return tc.GetNewMessagesCallback(ctx)
	})
}

// ==================== 获取群公告工具 ====================

type GetGroupNoticesInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=返回数量，默认5"`
}

type GroupNoticeSummary struct {
	NoticeID    string `json:"notice_id"`
	SenderID    int64  `json:"sender_id"`
	PublishTime string `json:"publish_time"`
	Content     string `json:"content"`
}

type GetGroupNoticesOutput struct {
	Success bool                 `json:"success"`
	Notices []GroupNoticeSummary `json:"notices,omitempty"`
	Message string               `json:"message,omitempty"`
}

func getGroupNoticesFunc(ctx context.Context, input *GetGroupNoticesInput) (*GetGroupNoticesOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("机器人未连接"))
	}
	if input == nil {
		return nil, fmt.Errorf("公告查询参数不能为空")
	}

	notices, err := tc.Bot.GetGroupNotice(ctx, tc.TargetID)
	if err != nil {
		return nil, fmt.Errorf("获取群公告失败: %w", err)
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 99 {
		limit = 99
	}
	if len(notices) > limit {
		notices = notices[:limit]
	}

	results := make([]GroupNoticeSummary, 0, len(notices))
	for _, n := range notices {
		results = append(results, GroupNoticeSummary{
			NoticeID:    n.NoticeID,
			SenderID:    n.SenderID,
			PublishTime: time.Unix(n.PublishTime, 0).Format("2006-01-02 15:04:05"),
			Content:     n.Content,
		})
	}

	return &GetGroupNoticesOutput{Success: true, Notices: results}, nil
}

func NewGetGroupNoticesTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getGroupNotices",
		"获取当前群的公告列表。可以了解群规、重要通知等信息。",
		getGroupNoticesFunc,
	)
}

// ==================== 获取群精华消息工具 ====================

type GetEssenceMessagesInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=返回数量，默认8"`
}

type EssenceMessageSummary struct {
	MessageID    int64  `json:"message_id"`
	SenderNick   string `json:"sender_nick"`
	OperatorNick string `json:"operator_nick"`
	OperatorTime string `json:"operator_time"`
	Content      string `json:"content"`
}

type GetEssenceMessagesOutput struct {
	Success  bool                    `json:"success"`
	Messages []EssenceMessageSummary `json:"messages,omitempty"`
	Message  string                  `json:"message,omitempty"`
}

func getEssenceMessagesFunc(ctx context.Context, input *GetEssenceMessagesInput) (*GetEssenceMessagesOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("机器人未连接"))
	}
	if input == nil {
		return nil, fmt.Errorf("精华消息查询参数不能为空")
	}

	messages, err := tc.Bot.GetEssenceMessages(ctx, tc.TargetID)
	if err != nil {
		return nil, fmt.Errorf("获取群精华消息失败: %w", err)
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 99 {
		limit = 99
	}
	if len(messages) > limit {
		messages = messages[:limit]
	}

	results := make([]EssenceMessageSummary, 0, len(messages))
	for _, m := range messages {
		results = append(results, EssenceMessageSummary{
			MessageID:    m.MessageID,
			SenderNick:   m.SenderNick,
			OperatorNick: m.OperatorNick,
			OperatorTime: time.Unix(m.OperatorTime, 0).Format("2006-01-02 15:04:05"),
			Content:      m.Content,
		})
	}

	return &GetEssenceMessagesOutput{Success: true, Messages: results}, nil
}

func NewGetEssenceMessagesTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getEssenceMessages",
		"获取当前群的精华消息。精华消息是被管理员设为精华的重要或有趣的消息。",
		getEssenceMessagesFunc,
	)
}

// ==================== 获取消息表情回应工具 ====================

type GetMessageReactionsInput struct {
	MessageID int64 `json:"message_id" jsonschema:"description=OneBot 消息 ID"`
}

type ReactionSummary struct {
	EmojiID string `json:"emoji_id"`
	Count   int64  `json:"count"`
}

type GetMessageReactionsOutput struct {
	Success   bool              `json:"success"`
	Reactions []ReactionSummary `json:"reactions,omitempty"`
	Message   string            `json:"message,omitempty"`
}

func getMessageReactionsFunc(ctx context.Context, input *GetMessageReactionsInput) (*GetMessageReactionsOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("机器人未连接"))
	}
	if input == nil {
		return nil, fmt.Errorf("表情回应查询参数不能为空")
	}
	messageID := input.MessageID
	if messageID == 0 {
		return nil, fmt.Errorf("消息 ID 不能为空")
	}

	reactions, err := tc.Bot.GetMessageReactions(ctx, messageID)
	if err != nil {
		return nil, fmt.Errorf("获取表情回应失败: %w", err)
	}

	if len(reactions) == 0 {
		return &GetMessageReactionsOutput{Success: true, Message: "该消息暂无表情回应"}, nil
	}

	results := make([]ReactionSummary, 0, len(reactions))
	for _, r := range reactions {
		results = append(results, ReactionSummary{
			EmojiID: r.EmojiID,
			Count:   r.Count,
		})
	}

	return &GetMessageReactionsOutput{Success: true, Reactions: results}, nil
}

func NewGetMessageReactionsTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getMessageReactions",
		"获取某条消息的表情回应。可以看到大家对这条消息的反应。",
		getMessageReactionsFunc,
	)
}
