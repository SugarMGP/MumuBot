package tools

import (
	"context"
	"fmt"
	"strconv"
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
type SendStickerCallback func(ctx context.Context, targetID int64, filePath string, description string) error
type InspectImageCallback func(ctx context.Context, imageURL string) (string, error)
type SendImageURLCallback func(ctx context.Context, imageURL string) error
type GetNewMessagesCallback func(ctx context.Context) (any, error)

// ToolContext 工具执行上下文
type ToolContext struct {
	ConversationKind        string
	TargetID                int64
	MemoryMgr               *memory.Manager
	Bot                     *onebot.Client
	SnapshotMessageID       int64
	SpeakCallback           SpeakCallback       // 发言回调
	SendStickerCallback     SendStickerCallback // 发送表情包回调
	InspectImageCallback    InspectImageCallback
	SendImageURLCallback    SendImageURLCallback
	GetNewMessagesCallback  GetNewMessagesCallback
	ThinkStartArrivalSeq    uint64
	ObservedThroughSeq      uint64
	ObservationIncomplete   bool
	MessageRecalledCallback func(*memory.MessageLog)
	actionSucceeded         bool

	messageRefs map[int64]string
	messageIDs  map[string]int64
	nextMessage int

	seenMu        sync.Mutex
	seenToolCalls map[string]struct{}
}

// MarkActionSucceeded 只在已确认远程操作或本地状态变更成功后记录会话行动
func (tc *ToolContext) MarkActionSucceeded() {
	if tc != nil {
		tc.actionSucceeded = true
	}
}

// ReadThroughSeq 失败轮次仅在已执行行动时消费快照，观察缺口不能推进观察水位
func (tc *ToolContext) ReadThroughSeq(thinkSucceeded bool) uint64 {
	if tc == nil || (!thinkSucceeded && !tc.actionSucceeded) {
		return 0
	}
	seq := tc.ThinkStartArrivalSeq
	if !tc.ObservationIncomplete {
		seq = max(seq, tc.ObservedThroughSeq)
	}
	return seq
}

func (tc *ToolContext) RegisterMessage(messageID int64) string {
	if tc == nil || messageID == 0 {
		return ""
	}
	if ref := tc.messageRefs[messageID]; ref != "" {
		return ref
	}
	if tc.messageRefs == nil {
		tc.messageRefs = make(map[int64]string)
		tc.messageIDs = make(map[string]int64)
	}
	tc.nextMessage++
	ref := "m" + strconv.Itoa(tc.nextMessage)
	tc.messageRefs[messageID] = ref
	tc.messageIDs[ref] = messageID
	return ref
}

func (tc *ToolContext) MessageRef(messageID int64) string {
	if tc == nil || messageID == 0 {
		return ""
	}
	return tc.messageRefs[messageID]
}

func (tc *ToolContext) ResolveMessageRef(ref string) (int64, bool) {
	if tc == nil {
		return 0, false
	}
	messageID, ok := tc.messageIDs[strings.TrimSpace(ref)]
	return messageID, ok
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

// IsToolCallSeen 判断本轮是否已经成功执行过相同工具调用
func (tc *ToolContext) IsToolCallSeen(toolName string, arguments string) bool {
	key := toolName + "-" + arguments

	tc.seenMu.Lock()
	defer tc.seenMu.Unlock()

	_, ok := tc.seenToolCalls[key]
	return ok
}

// MarkToolCallSucceeded 记录已经成功执行的工具调用
func (tc *ToolContext) MarkToolCallSucceeded(toolName string, arguments string) {
	key := toolName + "-" + arguments
	tc.seenMu.Lock()
	defer tc.seenMu.Unlock()
	if tc.seenToolCalls == nil {
		tc.seenToolCalls = make(map[string]struct{}, 8)
	}
	tc.seenToolCalls[key] = struct{}{}
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

// LogToolCall 记录工具调用
func LogToolCall(toolName string, inputJSON string, outputJSON string, err error) {
	cfg := config.Get()
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

type GetRecentMessagesInput struct {
	Limit  int `json:"limit,omitempty" jsonschema:"description=返回消息条数，默认40"`
	Offset int `json:"offset,omitempty" jsonschema:"description=偏移量，用于跳过近期的记录。例如 offset=10 表示跳过最近的10条消息"`
}

type GetRecentMessagesOutput struct {
	Success  bool                     `json:"success"`
	Messages []map[string]interface{} `json:"messages,omitempty"`
	Message  string                   `json:"message,omitempty"`
}

func getRecentMessagesFunc(ctx context.Context, input *GetRecentMessagesInput) (*GetRecentMessagesOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if input == nil {
		return nil, fmt.Errorf("读取参数不能为空")
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 40
	}
	if limit > 100 {
		limit = 100
	}

	messages, err := tc.MemoryMgr.GetRecentMessagesScope(ctx, tc.ConversationKind, tc.TargetID, tc.SnapshotMessageID, limit, input.Offset)
	if err != nil {
		return nil, fmt.Errorf("读取最近消息失败：%w", err)
	}
	results := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		messageRef := tc.RegisterMessage(m.OneBotMessageID)
		replyRef := ""
		if m.ReplyToMessageID != nil {
			replyRef = tc.RegisterMessage(*m.ReplyToMessageID)
		}
		content := m.DisplayContent
		if m.RecalledAt != nil {
			content = memory.RecalledMessageDisplayContent
		}
		results = append(results, map[string]interface{}{
			"message_ref":  messageRef,
			"reply_ref":    replyRef,
			"user_id":      m.UserID,
			"nickname":     m.Nickname,
			"content":      content,
			"time":         m.MessageTime.Format("15:04:05"),
			"is_mentioned": m.IsMentioned,
		})
	}

	return &GetRecentMessagesOutput{
		Success:  true,
		Messages: results,
	}, nil
}

func NewGetRecentMessagesTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getRecentMessages",
		"获取最近的聊天记录。当你需要了解更早之前的对话时使用。",
		getRecentMessagesFunc,
	)
}

func NewGetNewMessagesTool() (tool.InvokableTool, error) {
	return utils.InferTool("getNewMessages", "读取本轮思考开始后已经收到的新消息；不会等待。", func(ctx context.Context, _ *struct{}) (any, error) {
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
	MessageRef   string `json:"message_ref"`
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
	if len(messages) > limit {
		messages = messages[:limit]
	}

	results := make([]EssenceMessageSummary, 0, len(messages))
	for _, m := range messages {
		results = append(results, EssenceMessageSummary{
			MessageRef:   tc.RegisterMessage(m.MessageID),
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
	MessageRef string `json:"message_ref" jsonschema:"description=聊天记录中的消息编号，例如 m3"`
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
	messageID, ok := tc.ResolveMessageRef(input.MessageRef)
	if !ok {
		return nil, fmt.Errorf("消息编号不是当前对话中的消息")
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
