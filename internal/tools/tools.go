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
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// SpeakCallback 发言回调函数类型
type SpeakCallback func(ctx context.Context, targetID int64, content string, replyTo int64, mentions []int64) error

// SendStickerCallback 发送表情包回调函数类型
type SendStickerCallback func(ctx context.Context, targetID int64, fileName string, description string) error
type SendImageURLCallback func(ctx context.Context, imageURL string) error
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
	MemoryReadVersions      map[uint]uint64
	MemoryMaintained        bool
	TopicMaintained         bool
	SpeakCallback           SpeakCallback       // 发言回调
	SendStickerCallback     SendStickerCallback // 发送表情包回调
	SendImageURLCallback    SendImageURLCallback
	SessionMessageCallback  SessionMessageCallback
	ReadForwardCallback     func(ctx context.Context, id string, index int) (any, error)
	GroupInfoCallback       func(ctx context.Context) string
	StickerDir              string
	Session                 SessionContext
	ThinkStartArrivalSeq    uint64
	ObservedThroughSeq      uint64
	RecallCallback          func(messageID int64, arrivalSeq uint64)

	memoryMu sync.Mutex
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

// ==================== 获取短期记忆工具 ====================

func NewGetNewMessagesTool() (tool.EnhancedInvokableTool, error) {
	return utils.InferEnhancedTool("getNewMessages", "刷新本轮上下文；有新消息会作为随后的用户消息出现，不在工具结果里返回。", func(ctx context.Context, _ *struct{}) (*schema.ToolResult, error) {
		tc := GetToolContext(ctx)
		if tc == nil || tc.Session == nil {
			return nil, NewTerminalToolError(fmt.Errorf("新消息读取能力未初始化"))
		}
		floor := max(tc.ThinkStartArrivalSeq, tc.ObservedThroughSeq)
		count := len(tc.Session.PendingAfter(floor)) + len(tc.Session.PendingAgentMessages())
		text := "没有新消息。"
		if count > 0 {
			text = "新消息已随上下文提供（见随后的用户消息）。"
		}
		return &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: text}}}, nil
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
