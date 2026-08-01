package tools

import (
	"context"
	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	getreq "github.com/cloudwego/eino-ext/components/tool/httprequest/get"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"go.uber.org/zap"
)

// SpeakCallback 发言回调函数类型，返回消息ID
type SpeakCallback func(ctx context.Context, groupID int64, content string, replyTo int64, mentions []int64) (int64, error)

// SendStickerCallback 发送表情包回调函数类型
type SendStickerCallback func(ctx context.Context, groupID int64, filePath string, description string) (int64, error)

// ToolContext 工具执行上下文
type ToolContext struct {
	GroupID             int64
	MemoryMgr           *memory.Manager
	Bot                 *onebot.Client
	SnapshotMessageID   int64
	EvidenceMessageID   int64
	SpeakCallback       SpeakCallback       // 发言回调
	SendStickerCallback SendStickerCallback // 发送表情包回调

	seenMu        sync.Mutex
	seenToolCalls map[string]struct{}
	acted         atomic.Bool
}

func (tc *ToolContext) MarkActed() {
	tc.acted.Store(true)
}

func (tc *ToolContext) Acted() bool {
	return tc.acted.Load()
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

// IsToolCallSeen 判断本轮是否已经成功执行过相同工具调用。
func (tc *ToolContext) IsToolCallSeen(toolName string, arguments string) bool {
	key := toolName + "-" + arguments

	tc.seenMu.Lock()
	defer tc.seenMu.Unlock()

	_, ok := tc.seenToolCalls[key]
	return ok
}

// MarkToolCallSucceeded 记录已经成功执行的工具调用。
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
	// UserID 要查询的群成员QQ号
	UserID int64 `json:"user_id" jsonschema:"description=要查询的群成员QQ号"`
}

// GetGroupMemberDetailOutput 获取群成员详情的输出
type GetGroupMemberDetailOutput struct {
	Success       bool   `json:"success"`
	Message       string `json:"message,omitempty"`
	UserID        int64  `json:"user_id,omitempty"`
	Nickname      string `json:"nickname,omitempty"`
	GroupNickname string `json:"group_nickname,omitempty"` // 群昵称
	Role          string `json:"role,omitempty"`           // owner/admin/member
	Title         string `json:"title,omitempty"`          // 专属头衔
	Level         string `json:"level,omitempty"`          // 群等级
	JoinTime      string `json:"join_time,omitempty"`      // 入群时间
	LastSentTime  string `json:"last_sent_time,omitempty"` // 最后发言时间
}

// getGroupMemberDetailFunc 获取群成员详情的实际实现
func getGroupMemberDetailFunc(ctx context.Context, input *GetGroupMemberDetailInput) (*GetGroupMemberDetailOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &GetGroupMemberDetailOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	if tc.Bot == nil {
		return &GetGroupMemberDetailOutput{Success: false, Message: "Bot 未连接"}, nil
	}
	if input.UserID == 0 {
		return &GetGroupMemberDetailOutput{Success: false, Message: "用户 ID 不能为空"}, nil
	}

	info, err := tc.Bot.GetGroupMemberInfo(ctx, tc.GroupID, input.UserID, false)
	if err != nil {
		return &GetGroupMemberDetailOutput{Success: false, Message: err.Error()}, nil
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
	if tc == nil {
		return &GetRecentMessagesOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 40
	}

	messages := tc.MemoryMgr.GetRecentMessages(tc.GroupID, tc.SnapshotMessageID, limit, input.Offset)
	results := make([]map[string]interface{}, 0, len(messages))
	for _, m := range messages {
		results = append(results, map[string]interface{}{
			"message_id":   m.OneBotMessageID,
			"user_id":      m.UserID,
			"nickname":     m.Nickname,
			"content":      m.DisplayContent,
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
		return &GetGroupNoticesOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	if tc.Bot == nil {
		return &GetGroupNoticesOutput{Success: false, Message: "Bot 未连接"}, nil
	}

	notices, err := tc.Bot.GetGroupNotice(ctx, tc.GroupID)
	if err != nil {
		return &GetGroupNoticesOutput{Success: false, Message: "获取群公告失败: " + err.Error()}, nil
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
		return &GetEssenceMessagesOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	if tc.Bot == nil {
		return &GetEssenceMessagesOutput{Success: false, Message: "Bot 未连接"}, nil
	}

	messages, err := tc.Bot.GetEssenceMessages(ctx, tc.GroupID)
	if err != nil {
		return &GetEssenceMessagesOutput{Success: false, Message: "获取群精华消息失败: " + err.Error()}, nil
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
	MessageID int64 `json:"message_id" jsonschema:"description=消息ID"`
}

type ReactionSummary struct {
	EmojiID int `json:"emoji_id"`
	Count   int `json:"count"`
}

type GetMessageReactionsOutput struct {
	Success   bool              `json:"success"`
	Reactions []ReactionSummary `json:"reactions,omitempty"`
	Message   string            `json:"message,omitempty"`
}

func getMessageReactionsFunc(ctx context.Context, input *GetMessageReactionsInput) (*GetMessageReactionsOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &GetMessageReactionsOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	if tc.Bot == nil {
		return &GetMessageReactionsOutput{Success: false, Message: "Bot 未连接"}, nil
	}
	if input.MessageID == 0 {
		return &GetMessageReactionsOutput{Success: false, Message: "消息 ID 不能为空"}, nil
	}

	reactions, err := tc.Bot.GetMessageReactions(ctx, input.MessageID)
	if err != nil {
		return &GetMessageReactionsOutput{Success: false, Message: "获取表情回应失败: " + err.Error()}, nil
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

// ==================== 查看合并转发工具 ====================

// GetForwardMessageDetailInput 查看合并转发详情的输入参数
type GetForwardMessageDetailInput struct {
	// MessageID 包含合并转发内容的消息ID
	MessageID int64 `json:"message_id" jsonschema:"description=包含合并转发内容的消息ID"`
}

// GetForwardMessageDetailOutput 查看合并转发详情的输出
type GetForwardMessageDetailOutput struct {
	Success  bool                    `json:"success"`
	Message  string                  `json:"message,omitempty"`
	Forwards []onebot.ForwardMessage `json:"forwards,omitempty"`
}

// getForwardMessageDetailFunc 查看合并转发详情的实际实现
func getForwardMessageDetailFunc(ctx context.Context, input *GetForwardMessageDetailInput) (*GetForwardMessageDetailOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &GetForwardMessageDetailOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	if tc.MemoryMgr == nil {
		return &GetForwardMessageDetailOutput{Success: false, Message: "记忆管理器未初始化"}, nil
	}

	log, err := tc.MemoryMgr.GetMessageLogByID(input.MessageID)
	if err != nil {
		return &GetForwardMessageDetailOutput{Success: false, Message: "未找到该消息的记录"}, nil
	}

	if log.ForwardPayload == nil {
		return &GetForwardMessageDetailOutput{Success: false, Message: "该消息不包含合并转发内容"}, nil
	}

	var forwards []onebot.ForwardMessage
	if err := sonic.UnmarshalString(*log.ForwardPayload, &forwards); err != nil {
		return &GetForwardMessageDetailOutput{Success: false, Message: "解析合并转发内容失败"}, nil
	}

	return &GetForwardMessageDetailOutput{
		Success:  true,
		Forwards: forwards,
	}, nil
}

// NewGetForwardMessageDetailTool 创建查看合并转发工具
func NewGetForwardMessageDetailTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getForwardMessageDetail",
		"查看合并转发消息的具体内容。当你看到消息中包含[合并转发]字样时，使用此工具查看其中的详细对话。",
		getForwardMessageDetailFunc,
	)
}

func NewHttpRequestTool() (tool.BaseTool, error) {
	return getreq.NewTool(context.Background(), &getreq.Config{
		ToolName: "request_get",
		ToolDesc: "获取网页内容。当你需要查看某个网页的具体内容时使用，输入应为完整的URL（例如https://www.baidu.com）。",
		Headers: map[string]string{
			"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36 Edg/145.0.0.0",
		},
	})
}
