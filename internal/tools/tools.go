package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/refstore"
	"mumu-bot/internal/workerproto"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// SpeakCallback 发言回调函数类型
type SpeakCallback func(ctx context.Context, targetID int64, content string, replyTo int64, mentions []int64) (int64, error)

// SendStickerCallback 发送表情包回调函数类型
type SendStickerCallback func(ctx context.Context, targetID int64, fileName string, description string) error
type SendImageURLCallback func(ctx context.Context, imageURL string) error
type SessionMessageCallback func(ctx context.Context, fromKind string, fromTargetID int64, toKind string, toTargetID int64, text string, wake bool) (*memory.SessionMessage, error)
type SessionMessageContextCallback func(ctx context.Context, fromKind string, fromTargetID int64, toKind string, toTargetID int64, text string, wake bool, contextMode string) (*memory.SessionMessage, error)

// BashResult 是 worker executor 一次命令执行的结果
type BashResult struct {
	Success     bool   `json:"success"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	StdoutBytes int64  `json:"stdout_bytes"`
	StderrBytes int64  `json:"stderr_bytes"`
	StdoutLog   string `json:"stdout_log,omitempty"`
	StderrLog   string `json:"stderr_log,omitempty"`
	ExitCode    int    `json:"exit_code"`
	TimedOut    bool   `json:"timed_out"`
	Truncated   bool   `json:"truncated"`
}

// BashCallback 在 worker 沙箱里执行命令
type BashCallback func(ctx context.Context, command string, timeoutSeconds int) (*BashResult, error)

// UpdateWorkerTitleCallback 更新 worker 当前任务标题
type UpdateWorkerTitleCallback func(ctx context.Context, title string) error

// FileContent 是主服务从 worker home 读取的受限原始字节
type FileContent struct {
	Data        []byte
	ContentType string
}

// PersonalFileClient 访问 worker 的唯一 home，所有路径都由 worker 再校验
type PersonalFileClient interface {
	ListFiles(ctx context.Context, req workerproto.FileListRequest) (*workerproto.FileListResponse, error)
	ReadFile(ctx context.Context, req workerproto.FileReadRequest) (*workerproto.FileReadResponse, error)
	WriteFile(ctx context.Context, req workerproto.FileWriteRequest) (*workerproto.FileWriteResponse, error)
	SaveFile(ctx context.Context, req workerproto.FileSaveRequest) (*workerproto.FileSaveResponse, error)
	FileContent(ctx context.Context, path string) (*FileContent, error)
}

// SendFileResult 是一次文件或图片投递的真实回执
type SendFileResult struct {
	Kind      string `json:"kind"`
	FileID    string `json:"file_id,omitempty"`
	MessageID int64  `json:"message_id,omitempty"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
}

// SnapshotFileClient 在主服务快照与 worker home 之间流式互转
type SnapshotFileClient interface {
	CreateSnapshot(ctx context.Context, path string, maxBytes int64) (*refstore.Ref, error)
	ImportSnapshot(ctx context.Context, ref string, path string, overwrite bool) error
}

// SendFileCallback 向指定可用会话发送个人目录中的文件或图片
type SendFileCallback func(ctx context.Context, kind string, targetID int64, name string, data []byte, asImage bool) (*SendFileResult, error)

// ToolContext 工具执行上下文
type ToolContext struct {
	ConversationKind              string
	TargetID                      int64
	MemoryMgr                     *memory.Manager
	Bot                           *onebot.Client
	SnapshotLogID                 uint
	SnapshotLogIDSet              bool
	RunID                         string
	CompactionAttempts            int
	CurrentConversationOnly       bool
	MemoryReadVersions            map[uint]uint64
	MemoryMaintained              bool
	TopicMaintained               bool
	SpeakCallback                 SpeakCallback       // 发言回调
	SendStickerCallback           SendStickerCallback // 发送表情包回调
	SendImageURLCallback          SendImageURLCallback
	SessionMessageCallback        SessionMessageCallback
	SessionMessageContextCallback SessionMessageContextCallback
	SessionRunningCallback        func(kind string, targetID int64) bool
	SleepChangedCallback          func(ctx context.Context, awake bool)
	BashCallback                  BashCallback
	UpdateWorkerTitleCallback     UpdateWorkerTitleCallback
	PersonalFiles                 PersonalFileClient
	SnapshotFiles                 SnapshotFileClient
	SendFileCallback              SendFileCallback
	CancelWorkerCallback          func(ctx context.Context, workerID uint, reason string) (bool, error)
	ResolvePendingFile            func(ctx context.Context, locator refstore.Locator) (string, error)
	SendImageBytesCallback        func(ctx context.Context, imageRef, name string, data []byte) error
	ReadForwardCallback           func(ctx context.Context, id string, index int) (any, error)
	// EndAfterSleep 只在 sleepUntil 成功落库后置位，下一次模型边界正常收尾
	ModelRequested        bool
	EndAfterSleep         bool
	MemberRosterCallback  func(ctx context.Context, groupID int64) (*MemberRoster, error)
	LifeDailyRequestLimit *int
	GroupInfoCallback     func(ctx context.Context) string
	StickerDir            string
	Session               SessionContext
	ObservedThroughOrder  uint64
	RecallCallback        func(messageID int64, arrivalSeq uint64)

	memoryMu           sync.Mutex
	recallMu           sync.Mutex
	reinforcedMemories map[uint]bool
	reinforcedTopics   map[uint]bool
}

// MemoryRecallNeeded 判断本轮是否还需要强化该记忆
func (tc *ToolContext) MemoryRecallNeeded(id uint) bool {
	if tc == nil || id == 0 {
		return false
	}
	tc.recallMu.Lock()
	defer tc.recallMu.Unlock()
	return !tc.reinforcedMemories[id]
}

// MarkMemoryRecall 在数据库强化成功后记录本轮已强化
func (tc *ToolContext) MarkMemoryRecall(id uint) {
	if tc == nil || id == 0 {
		return
	}
	tc.recallMu.Lock()
	defer tc.recallMu.Unlock()
	if tc.reinforcedMemories == nil {
		tc.reinforcedMemories = map[uint]bool{}
	}
	tc.reinforcedMemories[id] = true
}

// TopicRecallNeeded 判断本轮是否还需要强化该话题
func (tc *ToolContext) TopicRecallNeeded(id uint) bool {
	if tc == nil || id == 0 {
		return false
	}
	tc.recallMu.Lock()
	defer tc.recallMu.Unlock()
	return !tc.reinforcedTopics[id]
}

// MarkTopicRecall 在数据库强化成功后记录本轮已强化
func (tc *ToolContext) MarkTopicRecall(id uint) {
	if tc == nil || id == 0 {
		return
	}
	tc.recallMu.Lock()
	defer tc.recallMu.Unlock()
	if tc.reinforcedTopics == nil {
		tc.reinforcedTopics = map[uint]bool{}
	}
	tc.reinforcedTopics[id] = true
}

// ctxKey 上下文键类型
type ctxKey string

const toolContextKey ctxKey = "tool_context"

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

// ==================== 获取群成员详情工具 ====================

// ==================== 获取短期记忆工具 ====================

func NewGetNewMessagesTool() (tool.EnhancedInvokableTool, error) {
	return utils.InferEnhancedTool("getNewMessages", "刷新本轮上下文；有新消息会作为随后的用户消息出现，不在工具结果里返回。", func(ctx context.Context, _ *struct{}) (*schema.ToolResult, error) {
		tc := GetToolContext(ctx)
		if tc == nil || tc.Session == nil {
			return nil, NewTerminalToolError(fmt.Errorf("新消息读取能力未初始化"))
		}
		count := len(tc.Session.PendingAfter(tc.ObservedThroughOrder))
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
