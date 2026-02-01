package tools

import (
	"amu-bot/internal/config"
	"amu-bot/internal/memory"
	"amu-bot/internal/onebot"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"go.uber.org/zap"
)

// SpeakCallback 发言回调函数类型，返回消息ID
type SpeakCallback func(groupID int64, content string, replyTo int64) int64

// ToolContext 工具执行上下文
type ToolContext struct {
	GroupID       int64
	MemoryMgr     *memory.Manager
	Bot           *onebot.Client
	SpeakCallback SpeakCallback // 发言回调
	StopThinking  func()        // 停止思考回调（用于 stayQuiet 强制停止）
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

// LogToolCall 记录工具调用
func LogToolCall(toolName string, input interface{}, output interface{}, err error) {
	cfg := config.Get()
	if cfg != nil && cfg.Debug.ShowToolCalls {
		inputJSON, _ := json.Marshal(input)
		outputJSON, _ := json.Marshal(output)
		if err != nil {
			zap.L().Debug("工具调用", zap.String("tool", toolName), zap.String("input", string(inputJSON)), zap.String("output", string(outputJSON)), zap.Error(err))
		} else {
			zap.L().Debug("工具调用", zap.String("tool", toolName), zap.String("input", string(inputJSON)), zap.String("output", string(outputJSON)))
		}
	}
}

// ==================== 保存记忆工具 ====================

// SaveMemoryInput 保存记忆的输入参数
type SaveMemoryInput struct {
	// Type 记忆类型：group_fact(群事实)、self_experience(自身经历)、conversation(对话)
	Type string `json:"type" jsonschema:"enum=group_fact,enum=self_experience,enum=conversation,description=记忆类型：group_fact=群相关的长期事实、self_experience=你自己的经历和感受、conversation=对话中的重要信息"`
	// Content 要记住的内容，用自然语言描述
	Content string `json:"content" jsonschema:"description=要记住的内容，用自然语言描述清楚"`
	// Importance 重要性评分，0-1之间
	Importance float64 `json:"importance,omitempty" jsonschema:"description=重要性评分(0-1)，越重要越高"`
	// RelatedUserID 相关的用户ID（可选）
	RelatedUserID int64 `json:"related_user_id,omitempty" jsonschema:"description=如果这条记忆与某个群友相关，填写其QQ号"`
}

// SaveMemoryOutput 保存记忆的输出
type SaveMemoryOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// saveMemoryFunc 保存记忆的实际实现
func saveMemoryFunc(ctx context.Context, input *SaveMemoryInput) (*SaveMemoryOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &SaveMemoryOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.Content == "" {
		return &SaveMemoryOutput{Success: false, Message: "内容不能为空"}, nil
	}

	// 验证记忆类型
	validTypes := map[string]bool{
		string(memory.MemoryTypeGroupFact):      true,
		string(memory.MemoryTypeSelfExperience): true,
		string(memory.MemoryTypeConversation):   true,
	}
	if !validTypes[input.Type] {
		return &SaveMemoryOutput{Success: false, Message: "无效的记忆类型，可选: group_fact, self_experience, conversation"}, nil
	}

	importance := input.Importance
	if importance <= 0 || importance > 1 {
		importance = 0.5
	}

	mem := &memory.Memory{
		Type:       memory.MemoryType(input.Type),
		GroupID:    tc.GroupID,
		UserID:     input.RelatedUserID,
		Content:    input.Content,
		Importance: importance,
	}

	if err := tc.MemoryMgr.SaveMemory(ctx, mem); err != nil {
		output := &SaveMemoryOutput{Success: false, Message: err.Error()}
		LogToolCall("saveMemory", input, output, err)
		return output, nil
	}

	output := &SaveMemoryOutput{Success: true, Message: "已记住"}
	LogToolCall("saveMemory", input, output, nil)
	return output, nil
}

// NewSaveMemoryTool 创建保存记忆工具
func NewSaveMemoryTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"saveMemory",
		`保存重要信息到长期记忆。当你发现以下情况时应该使用：
- group_fact：群规、群特色、群里的重要事件、某个话题的结论等
- self_experience：你参与的有趣对话、被@的经历、你的主观感受和想法
- conversation：群友说的重要事情、有价值的信息、值得记住的对话内容
注意：普通闲聊不需要保存，只保存真正有价值的信息。`,
		saveMemoryFunc,
	)
}

// ==================== 查询记忆工具 ====================

// QueryMemoryInput 查询记忆的输入参数
type QueryMemoryInput struct {
	// Query 搜索关键词或描述
	Query string `json:"query" jsonschema:"description=搜索关键词或描述"`
	// Type 限定记忆类型（可选）
	Type string `json:"type,omitempty" jsonschema:"enum=group_fact,enum=self_experience,enum=conversation,description=限定记忆类型"`
}

// QueryMemoryOutput 查询记忆的输出
type QueryMemoryOutput struct {
	Success  bool                     `json:"success"`
	Count    int                      `json:"count"`
	Memories []map[string]interface{} `json:"memories,omitempty"`
	Message  string                   `json:"message,omitempty"`
}

// queryMemoryFunc 查询记忆的实际实现
func queryMemoryFunc(ctx context.Context, input *QueryMemoryInput) (*QueryMemoryOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &QueryMemoryOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.Query == "" {
		return &QueryMemoryOutput{Success: false, Message: "查询内容不能为空"}, nil
	}

	memories, err := tc.MemoryMgr.QueryMemory(ctx, input.Query, tc.GroupID, memory.MemoryType(input.Type), 10)
	if err != nil {
		output := &QueryMemoryOutput{Success: false, Message: err.Error()}
		LogToolCall("queryMemory", input, output, err)
		return output, nil
	}

	results := make([]map[string]interface{}, 0, len(memories))
	for _, m := range memories {
		results = append(results, map[string]interface{}{
			"type":       m.Type,
			"content":    m.Content,
			"importance": m.Importance,
			"created_at": m.CreatedAt.Format("2006-01-02 15:04"),
		})
	}

	output := &QueryMemoryOutput{
		Success:  true,
		Count:    len(results),
		Memories: results,
	}
	LogToolCall("queryMemory", input, output, nil)
	return output, nil
}

// NewQueryMemoryTool 创建查询记忆工具
func NewQueryMemoryTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"queryMemory",
		"搜索你的记忆，找到相关的信息。可以查询关于某个话题、某个人、或者某次经历的记忆。",
		queryMemoryFunc,
	)
}

// ==================== 保存黑话工具 ====================

// SaveJargonInput 保存黑话的输入参数
type SaveJargonInput struct {
	// Content 黑话/术语/梗的内容
	Content string `json:"content" jsonschema:"description=黑话、术语或梗的原文"`
	// Meaning 含义解释
	Meaning string `json:"meaning" jsonschema:"description=这个黑话/术语的含义或解释"`
	// Context 使用场景或上下文
	Context string `json:"context,omitempty" jsonschema:"description=在什么情况下使用，或者来源背景"`
}

// SaveJargonOutput 保存黑话的输出
type SaveJargonOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// saveJargonFunc 保存黑话的实际实现
func saveJargonFunc(ctx context.Context, input *SaveJargonInput) (*SaveJargonOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &SaveJargonOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.Content == "" {
		return &SaveJargonOutput{Success: false, Message: "黑话内容不能为空"}, nil
	}
	if input.Meaning == "" {
		return &SaveJargonOutput{Success: false, Message: "黑话含义不能为空"}, nil
	}

	jargon := &memory.Jargon{
		GroupID: tc.GroupID,
		Content: input.Content,
		Meaning: input.Meaning,
		Context: input.Context,
	}

	if err := tc.MemoryMgr.SaveJargon(jargon); err != nil {
		output := &SaveJargonOutput{Success: false, Message: err.Error()}
		LogToolCall("saveJargon", input, output, err)
		return output, nil
	}

	output := &SaveJargonOutput{Success: true, Message: "已记住这个黑话"}
	LogToolCall("saveJargon", input, output, nil)
	return output, nil
}

// NewSaveJargonTool 创建保存黑话工具
func NewSaveJargonTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"saveJargon",
		`保存群里的黑话、术语或梗。当你发现群友使用了你不懂的词汇，并且从上下文理解了它的含义时，可以保存下来。
例如：群里有人说"触摸"然后大家都笑了，你从对话中理解这是一个内部梗。`,
		saveJargonFunc,
	)
}

// ==================== 更新成员画像工具 ====================

// UpdateMemberProfileInput 更新成员画像的输入参数
type UpdateMemberProfileInput struct {
	// UserID 群友的QQ号
	UserID int64 `json:"user_id" jsonschema:"description=群友的QQ号"`
	// Nickname 群友的昵称
	Nickname string `json:"nickname,omitempty" jsonschema:"description=群友的昵称"`
	// SpeakStyle 说话风格描述
	SpeakStyle string `json:"speak_style,omitempty" jsonschema:"description=说话风格描述"`
	// Interests 兴趣爱好列表
	Interests []string `json:"interests,omitempty" jsonschema:"description=兴趣爱好列表"`
	// CommonWords 常用词汇或口头禅
	CommonWords []string `json:"common_words,omitempty" jsonschema:"description=常用词汇或口头禅"`
}

// UpdateMemberProfileOutput 更新成员画像的输出
type UpdateMemberProfileOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// updateMemberProfileFunc 更新成员画像的实际实现
func updateMemberProfileFunc(ctx context.Context, input *UpdateMemberProfileInput) (*UpdateMemberProfileOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &UpdateMemberProfileOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.UserID == 0 {
		return &UpdateMemberProfileOutput{Success: false, Message: "用户ID不能为空"}, nil
	}

	profile, err := tc.MemoryMgr.GetOrCreateMemberProfile(tc.GroupID, input.UserID, input.Nickname)
	if err != nil {
		return &UpdateMemberProfileOutput{Success: false, Message: err.Error()}, nil
	}

	if input.Nickname != "" {
		profile.Nickname = input.Nickname
	}
	if input.SpeakStyle != "" {
		profile.SpeakStyle = input.SpeakStyle
	}
	if len(input.Interests) > 0 {
		b, _ := json.Marshal(input.Interests)
		profile.Interests = string(b)
	}
	if len(input.CommonWords) > 0 {
		b, _ := json.Marshal(input.CommonWords)
		profile.CommonWords = string(b)
	}

	if err := tc.MemoryMgr.UpdateMemberProfile(profile); err != nil {
		output := &UpdateMemberProfileOutput{Success: false, Message: err.Error()}
		LogToolCall("updateMemberProfile", input, output, err)
		return output, nil
	}

	output := &UpdateMemberProfileOutput{Success: true, Message: "已更新对该群友的了解"}
	LogToolCall("updateMemberProfile", input, output, nil)
	return output, nil
}

// NewUpdateMemberProfileTool 创建更新成员画像工具
func NewUpdateMemberProfileTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"updateMemberProfile",
		"更新你对某个群友的了解。当你发现群友的新特点、说话风格、兴趣爱好时使用。",
		updateMemberProfileFunc,
	)
}

// ==================== 获取成员信息工具 ====================

// GetMemberInfoInput 获取成员信息的输入参数
type GetMemberInfoInput struct {
	// UserID 群友的QQ号
	UserID int64 `json:"user_id" jsonschema:"description=群友的QQ号"`
}

// GetMemberInfoOutput 获取成员信息的输出
type GetMemberInfoOutput struct {
	Success     bool     `json:"success"`
	Message     string   `json:"message,omitempty"`
	Nickname    string   `json:"nickname,omitempty"`
	SpeakStyle  string   `json:"speak_style,omitempty"`
	Interests   []string `json:"interests,omitempty"`
	CommonWords []string `json:"common_words,omitempty"`
	Activity    float64  `json:"activity,omitempty"` // 活跃度 0-1
	Intimacy    float64  `json:"intimacy,omitempty"` // 亲密度 0-1
	MsgCount    int      `json:"msg_count,omitempty"`
}

// getMemberInfoFunc 获取成员信息的实际实现
func getMemberInfoFunc(ctx context.Context, input *GetMemberInfoInput) (*GetMemberInfoOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &GetMemberInfoOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.UserID == 0 {
		return &GetMemberInfoOutput{Success: false, Message: "用户ID不能为空"}, nil
	}

	profile, err := tc.MemoryMgr.GetMemberProfile(tc.GroupID, input.UserID)
	if err != nil {
		output := &GetMemberInfoOutput{
			Success: false,
			Message: "不太了解这个人",
		}
		LogToolCall("getMemberInfo", input, output, err)
		return output, nil
	}

	var interests, commonWords []string
	if profile.Interests != "" {
		if err := json.Unmarshal([]byte(profile.Interests), &interests); err != nil {
			zap.L().Warn("反序列化 interests 失败", zap.Error(err))
		}
	}
	if profile.CommonWords != "" {
		if err := json.Unmarshal([]byte(profile.CommonWords), &commonWords); err != nil {
			zap.L().Warn("反序列化 commonWords 失败", zap.Error(err))
		}
	}

	output := &GetMemberInfoOutput{
		Success:     true,
		Nickname:    profile.Nickname,
		SpeakStyle:  profile.SpeakStyle,
		Interests:   interests,
		CommonWords: commonWords,
		Activity:    profile.Activity,
		Intimacy:    profile.Intimacy,
		MsgCount:    profile.MsgCount,
	}
	LogToolCall("getMemberInfo", input, output, nil)
	return output, nil
}

// NewGetMemberInfoTool 创建获取成员信息工具
func NewGetMemberInfoTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getMemberInfo",
		"查看你对某个群友的了解。",
		getMemberInfoFunc,
	)
}

// ==================== 发言工具 ====================

// SpeakInput 发言的输入参数
type SpeakInput struct {
	// Content 你想说的话
	Content string `json:"content" jsonschema:"description=你想说的话，不要用markdown，说话要口语化"`
	// ReplyTo 要回复的消息ID（可选）
	ReplyTo int64 `json:"reply_to,omitempty" jsonschema:"description=要回复的消息ID"`
}

// SpeakOutput 发言的输出
type SpeakOutput struct {
	Success   bool   `json:"success"`
	MessageID int64  `json:"message_id,omitempty"` // 发送成功后的消息ID
	Message   string `json:"message,omitempty"`
}

// speakFunc 发言的实际实现 - 会通过回调实际发送消息
func speakFunc(ctx context.Context, input *SpeakInput) (*SpeakOutput, error) {
	if input.Content == "" {
		return &SpeakOutput{Success: false, Message: "说话内容不能为空"}, nil
	}

	var msgID int64
	// 获取工具上下文
	tc := GetToolContext(ctx)
	if tc != nil && tc.SpeakCallback != nil {
		// 通过回调发送消息，获取返回的消息ID
		msgID = tc.SpeakCallback(tc.GroupID, input.Content, input.ReplyTo)
	}

	output := &SpeakOutput{
		Success:   true,
		MessageID: msgID,
		Message:   fmt.Sprintf("发言成功，消息ID: %d", msgID),
	}
	LogToolCall("speak", input, output, nil)
	return output, nil
}

// NewSpeakTool 创建发言工具
func NewSpeakTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"speak",
		`在群里说话。只有当你真的想说什么的时候才用，不用强迫自己每次都说话。说话要自然，像个真人群友。

【重要】使用规则：
- 每次调用speak只能发送**一条**消息，不要把多句话合并在一起
- 如果你想说多句话，请多次调用speak
- 不要用分号、句号或换行符连接多个独立的句子
- 正确示例：先调用speak("你好")，再调用speak("今天天气真好")
- 错误示例：speak("你好。今天天气真好") 或 speak("你好\n今天天气真好")`,
		speakFunc,
	)
}

// ==================== 保持沉默工具 ====================

// StayQuietInput 保持沉默的输入参数
type StayQuietInput struct {
	// Reason 不说话的原因
	Reason string `json:"reason,omitempty" jsonschema:"description=不说话的原因（给自己看的笔记）"`
}

// StayQuietOutput 保持沉默的输出
type StayQuietOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// stayQuietFunc 保持沉默的实际实现
func stayQuietFunc(ctx context.Context, input *StayQuietInput) (*StayQuietOutput, error) {
	output := &StayQuietOutput{
		Success: true,
		Message: "保持沉默",
	}
	LogToolCall("stayQuiet", input, output, nil)

	// 调用 StopThinking 强制停止思考
	tc := GetToolContext(ctx)
	if tc != nil && tc.StopThinking != nil {
		tc.StopThinking()
	}

	return output, nil
}

// NewStayQuietTool 创建保持沉默工具
func NewStayQuietTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"stayQuiet",
		`选择不说话，保持沉默。当话题你不熟悉、不感兴趣、或者觉得没必要插嘴时使用。

【重要】使用规则：
- stayQuiet 应该在你决定不发言时**最后调用**
- 调用 stayQuiet 后必须立刻停止，不要再调用任何工具
- 如果你想说话，请用 speak，不要在 stayQuiet 之后再 speak`,
		stayQuietFunc,
	)
}

// ==================== 获取当前时间工具 ====================

// GetCurrentTimeInput 获取当前时间的输入参数（无参数）
type GetCurrentTimeInput struct{}

// GetCurrentTimeOutput 获取当前时间的输出
type GetCurrentTimeOutput struct {
	Time      string `json:"time"`
	Weekday   string `json:"weekday"`
	Period    string `json:"period"`
	IsLate    bool   `json:"is_late"`
	IsWeekend bool   `json:"is_weekend"`
}

// getCurrentTimeFunc 获取当前时间的实际实现
func getCurrentTimeFunc(ctx context.Context, input *GetCurrentTimeInput) (*GetCurrentTimeOutput, error) {
	now := time.Now()
	hour := now.Hour()

	var period string
	switch {
	case hour >= 6 && hour < 9:
		period = "早上"
	case hour >= 9 && hour < 12:
		period = "上午"
	case hour >= 12 && hour < 14:
		period = "中午"
	case hour >= 14 && hour < 18:
		period = "下午"
	case hour >= 18 && hour < 22:
		period = "晚上"
	default:
		period = "深夜"
	}

	output := &GetCurrentTimeOutput{
		Time:      now.Format("2006-01-02 15:04:05"),
		Weekday:   now.Weekday().String(),
		Period:    period,
		IsLate:    hour >= 23 || hour < 6,
		IsWeekend: now.Weekday() == time.Saturday || now.Weekday() == time.Sunday,
	}
	LogToolCall("getCurrentTime", input, output, nil)
	return output, nil
}

// NewGetCurrentTimeTool 创建获取当前时间工具
func NewGetCurrentTimeTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getCurrentTime",
		"获取当前时间，可以用来判断是白天还是晚上，是否该睡觉了等。",
		getCurrentTimeFunc,
	)
}

// ==================== 获取群信息工具 ====================

// GetGroupInfoInput 获取群信息的输入参数
type GetGroupInfoInput struct{}

// GetGroupInfoOutput 获取群信息的输出
type GetGroupInfoOutput struct {
	Success        bool   `json:"success"`
	Message        string `json:"message,omitempty"`
	GroupID        int64  `json:"group_id,omitempty"`
	GroupName      string `json:"group_name,omitempty"`
	MemberCount    int    `json:"member_count,omitempty"`
	MaxMemberCount int    `json:"max_member_count,omitempty"`
}

// getGroupInfoFunc 获取群信息的实际实现
func getGroupInfoFunc(ctx context.Context, input *GetGroupInfoInput) (*GetGroupInfoOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		output := &GetGroupInfoOutput{Success: false, Message: "工具上下文未初始化"}
		LogToolCall("getGroupInfo", input, output, nil)
		return output, nil
	}
	if tc.Bot == nil {
		output := &GetGroupInfoOutput{Success: false, Message: "Bot未连接"}
		LogToolCall("getGroupInfo", input, output, nil)
		return output, nil
	}

	info, err := tc.Bot.GetGroupInfo(tc.GroupID, false)
	if err != nil {
		output := &GetGroupInfoOutput{Success: false, Message: err.Error()}
		LogToolCall("getGroupInfo", input, output, err)
		return output, nil
	}

	output := &GetGroupInfoOutput{
		Success:        true,
		GroupID:        info.GroupID,
		GroupName:      info.GroupName,
		MemberCount:    info.MemberCount,
		MaxMemberCount: info.MaxMemberCount,
	}
	LogToolCall("getGroupInfo", input, output, nil)
	return output, nil
}

// NewGetGroupInfoTool 创建获取群信息工具
func NewGetGroupInfoTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getGroupInfo",
		"获取当前群的基本信息，包括群名称、成员数量等。",
		getGroupInfoFunc,
	)
}

// ==================== 获取群成员详情工具 ====================

// GetGroupMemberDetailInput 获取群成员详情的输入参数
type GetGroupMemberDetailInput struct {
	// UserID 要查询的群成员QQ号
	UserID int64 `json:"user_id" jsonschema:"description=要查询的群成员QQ号"`
}

// GetGroupMemberDetailOutput 获取群成员详情的输出
type GetGroupMemberDetailOutput struct {
	Success      bool   `json:"success"`
	Message      string `json:"message,omitempty"`
	UserID       int64  `json:"user_id,omitempty"`
	Nickname     string `json:"nickname,omitempty"`
	Card         string `json:"card,omitempty"`           // 群名片
	Role         string `json:"role,omitempty"`           // owner/admin/member
	Title        string `json:"title,omitempty"`          // 专属头衔
	JoinTime     string `json:"join_time,omitempty"`      // 入群时间
	LastSentTime string `json:"last_sent_time,omitempty"` // 最后发言时间
}

// getGroupMemberDetailFunc 获取群成员详情的实际实现
func getGroupMemberDetailFunc(ctx context.Context, input *GetGroupMemberDetailInput) (*GetGroupMemberDetailOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &GetGroupMemberDetailOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	if tc.Bot == nil {
		return &GetGroupMemberDetailOutput{Success: false, Message: "Bot未连接"}, nil
	}
	if input.UserID == 0 {
		return &GetGroupMemberDetailOutput{Success: false, Message: "用户ID不能为空"}, nil
	}

	info, err := tc.Bot.GetGroupMemberInfo(tc.GroupID, input.UserID, false)
	if err != nil {
		output := &GetGroupMemberDetailOutput{Success: false, Message: err.Error()}
		LogToolCall("getGroupMemberDetail", input, output, err)
		return output, nil
	}

	output := &GetGroupMemberDetailOutput{
		Success:  true,
		UserID:   info.UserID,
		Nickname: info.Nickname,
		Card:     info.Card,
		Role:     info.Role,
		Title:    info.Title,
	}

	if info.JoinTime > 0 {
		output.JoinTime = time.Unix(info.JoinTime, 0).Format("2006-01-02 15:04:05")
	}
	if info.LastSentTime > 0 {
		output.LastSentTime = time.Unix(info.LastSentTime, 0).Format("2006-01-02 15:04:05")
	}

	LogToolCall("getGroupMemberDetail", input, output, nil)
	return output, nil
}

// NewGetGroupMemberDetailTool 创建获取群成员详情工具
func NewGetGroupMemberDetailTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getGroupMemberDetail",
		"获取某个群成员的详细信息，包括群名片、角色（群主/管理员/普通成员）、入群时间、最后发言时间等。",
		getGroupMemberDetailFunc,
	)
}

// ==================== 戳一戳工具 ====================

// PokeInput 戳一戳的输入参数
type PokeInput struct {
	// UserID 要戳的群成员QQ号
	UserID int64 `json:"user_id" jsonschema:"description=要戳的群成员QQ号"`
}

// PokeOutput 戳一戳的输出
type PokeOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// pokeFunc 戳一戳的实际实现
func pokeFunc(ctx context.Context, input *PokeInput) (*PokeOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &PokeOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	if tc.Bot == nil {
		return &PokeOutput{Success: false, Message: "Bot未连接"}, nil
	}
	if input.UserID == 0 {
		return &PokeOutput{Success: false, Message: "用户ID不能为空"}, nil
	}

	if err := tc.Bot.GroupPoke(tc.GroupID, input.UserID); err != nil {
		output := &PokeOutput{Success: false, Message: err.Error()}
		LogToolCall("poke", input, output, err)
		return output, nil
	}

	output := &PokeOutput{Success: true, Message: "已戳一戳"}
	LogToolCall("poke", input, output, nil)
	return output, nil
}

// NewPokeTool 创建戳一戳工具
func NewPokeTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"poke",
		"戳一戳某个群友。可以用来打招呼、吸引注意力、或者逗逗人玩。不要频繁使用。",
		pokeFunc,
	)
}

// ==================== 消息贴表情工具 ====================

// ReactToMessageInput 对消息贴表情的输入参数
type ReactToMessageInput struct {
	// MessageID 要回应的消息ID
	MessageID int64 `json:"message_id" jsonschema:"description=要回应的消息ID"`
	// EmojiID 表情ID，例如：76(赞)、77(踩)、66(爱心)、78(握手)等
	EmojiID int `json:"emoji_id" jsonschema:"description=表情ID。常用：76=赞、77=踩、66=爱心、78=握手、124=OK、179=doge"`
}

// ReactToMessageOutput 对消息贴表情的输出
type ReactToMessageOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// reactToMessageFunc 对消息贴表情的实际实现
func reactToMessageFunc(ctx context.Context, input *ReactToMessageInput) (*ReactToMessageOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &ReactToMessageOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	if tc.Bot == nil {
		return &ReactToMessageOutput{Success: false, Message: "Bot未连接"}, nil
	}
	if input.MessageID == 0 {
		return &ReactToMessageOutput{Success: false, Message: "消息ID不能为空"}, nil
	}
	if input.EmojiID == 0 {
		return &ReactToMessageOutput{Success: false, Message: "表情ID不能为空"}, nil
	}

	if err := tc.Bot.SetMsgEmojiLike(input.MessageID, input.EmojiID); err != nil {
		output := &ReactToMessageOutput{Success: false, Message: err.Error()}
		LogToolCall("reactToMessage", input, output, err)
		return output, nil
	}

	output := &ReactToMessageOutput{Success: true, Message: "已回应表情"}
	LogToolCall("reactToMessage", input, output, nil)
	return output, nil
}

// NewReactToMessageTool 创建对消息贴表情工具
func NewReactToMessageTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"reactToMessage",
		"对某条消息贴表情回应。可以表达认同、喜欢、疑问等情绪，比直接回复更轻量。",
		reactToMessageFunc,
	)
}

// ==================== 撤回消息工具 ====================

// RecallMessageInput 撤回消息的输入参数
type RecallMessageInput struct {
	// MessageID 要撤回的消息ID
	MessageID int64 `json:"message_id" jsonschema:"description=要撤回的消息ID"`
	// Reason 撤回原因
	Reason string `json:"reason,omitempty" jsonschema:"description=撤回原因（给自己看的笔记）"`
}

// RecallMessageOutput 撤回消息的输出
type RecallMessageOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// recallMessageFunc 撤回消息的实际实现
func recallMessageFunc(ctx context.Context, input *RecallMessageInput) (*RecallMessageOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &RecallMessageOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	if tc.Bot == nil {
		return &RecallMessageOutput{Success: false, Message: "Bot未连接"}, nil
	}
	if input.MessageID == 0 {
		return &RecallMessageOutput{Success: false, Message: "消息ID不能为空"}, nil
	}

	if err := tc.Bot.DeleteMsg(input.MessageID); err != nil {
		output := &RecallMessageOutput{Success: false, Message: err.Error()}
		LogToolCall("recallMessage", input, output, err)
		return output, nil
	}

	output := &RecallMessageOutput{Success: true, Message: "已撤回消息"}
	LogToolCall("recallMessage", input, output, nil)
	return output, nil
}

// NewRecallMessageTool 创建撤回消息工具
func NewRecallMessageTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"recallMessage",
		"撤回你自己发的消息。当你发错消息、说错话、或者想收回刚才的发言时使用。只能撤回自己两分钟内发的消息。",
		recallMessageFunc,
	)
}

// ==================== 保存表达方式工具 ====================

type SaveExpressionInput struct {
	Situation string `json:"situation" jsonschema:"description=使用场景，例如：打招呼、吐槽、表达惊讶等"`
	Style     string `json:"style" jsonschema:"description=表达风格或具体的口头禅"`
	Example   string `json:"example,omitempty" jsonschema:"description=具体的例子"`
}

type SaveExpressionOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func saveExpressionFunc(ctx context.Context, input *SaveExpressionInput) (*SaveExpressionOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &SaveExpressionOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.Situation == "" {
		return &SaveExpressionOutput{Success: false, Message: "使用场景不能为空"}, nil
	}
	if input.Style == "" {
		return &SaveExpressionOutput{Success: false, Message: "表达风格不能为空"}, nil
	}

	exp := &memory.Expression{
		GroupID:   tc.GroupID,
		Situation: input.Situation,
		Style:     input.Style,
		Examples:  input.Example,
		LastUsed:  time.Now(),
	}

	if err := tc.MemoryMgr.SaveExpression(exp); err != nil {
		output := &SaveExpressionOutput{Success: false, Message: err.Error()}
		LogToolCall("saveExpression", input, output, err)
		return output, nil
	}

	output := &SaveExpressionOutput{Success: true, Message: "已记住这种表达方式"}
	LogToolCall("saveExpression", input, output, nil)
	return output, nil
}

func NewSaveExpressionTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"saveExpression",
		"保存你学到的群友表达方式或口头禅。当你发现群友在特定场景下有独特的说话习惯时，可以记录下来以便模仿。",
		saveExpressionFunc,
	)
}

// ==================== 获取表达方式工具 ====================

type GetExpressionsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=返回数量，默认5"`
}

type GetExpressionsOutput struct {
	Success     bool     `json:"success"`
	Expressions []string `json:"expressions,omitempty"`
	Message     string   `json:"message,omitempty"`
}

func getExpressionsFunc(ctx context.Context, input *GetExpressionsInput) (*GetExpressionsOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &GetExpressionsOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 5
	}

	exps, err := tc.MemoryMgr.GetExpressions(tc.GroupID, limit)
	if err != nil {
		output := &GetExpressionsOutput{Success: false, Message: err.Error()}
		LogToolCall("getExpressions", input, output, err)
		return output, nil
	}

	results := make([]string, 0, len(exps))
	for _, e := range exps {
		results = append(results, fmt.Sprintf("[%s]: %s (示例: %s)", e.Situation, e.Style, e.Examples))
	}

	output := &GetExpressionsOutput{
		Success:     true,
		Expressions: results,
	}
	LogToolCall("getExpressions", input, output, nil)
	return output, nil
}

func NewGetExpressionsTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getExpressions",
		"查看你学到的群友表达方式和口头禅。在你想模仿群友说话或者不知道该怎么表达时使用。",
		getExpressionsFunc,
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

	msgs := tc.MemoryMgr.GetRecentMessages(tc.GroupID, limit, input.Offset)
	results := make([]map[string]interface{}, 0, len(msgs))
	for _, m := range msgs {
		results = append(results, map[string]interface{}{
			"user_id":    m.UserID,
			"nickname":   m.Nickname,
			"content":    m.Content,
			"time":       m.CreatedAt.Format("15:04:05"),
			"is_mention": m.MentionAmu,
		})
	}

	output := &GetRecentMessagesOutput{
		Success:  true,
		Messages: results,
	}
	LogToolCall("getRecentMessages", input, output, nil)
	return output, nil
}

func NewGetRecentMessagesTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getRecentMessages",
		"获取最近的聊天记录。当你需要了解更早之前的对话时使用。",
		getRecentMessagesFunc,
	)
}

// ==================== 审核表达方式工具 ====================

type GetUncheckedExpressionsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=返回数量，默认5"`
}

type UncheckedExpressionItem struct {
	ID        uint   `json:"id"`
	Situation string `json:"situation"`
	Style     string `json:"style"`
	Examples  string `json:"examples"`
	Count     int    `json:"count"`
}

type GetUncheckedExpressionsOutput struct {
	Success     bool                      `json:"success"`
	Expressions []UncheckedExpressionItem `json:"expressions,omitempty"`
	Message     string                    `json:"message,omitempty"`
}

func getUncheckedExpressionsFunc(ctx context.Context, input *GetUncheckedExpressionsInput) (*GetUncheckedExpressionsOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &GetUncheckedExpressionsOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 5
	}

	exps, err := tc.MemoryMgr.GetUncheckedExpressions(tc.GroupID, limit)
	if err != nil {
		output := &GetUncheckedExpressionsOutput{Success: false, Message: err.Error()}
		LogToolCall("getUncheckedExpressions", input, output, err)
		return output, nil
	}

	results := make([]UncheckedExpressionItem, 0, len(exps))
	for _, e := range exps {
		results = append(results, UncheckedExpressionItem{
			ID:        e.ID,
			Situation: e.Situation,
			Style:     e.Style,
			Examples:  e.Examples,
			Count:     e.Count,
		})
	}

	output := &GetUncheckedExpressionsOutput{Success: true, Expressions: results}
	LogToolCall("getUncheckedExpressions", input, output, nil)
	return output, nil
}

func NewGetUncheckedExpressionsTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getUncheckedExpressions",
		"查看待审核的表达方式。你可以定期检查并审核这些学到的表达习惯是否准确。",
		getUncheckedExpressionsFunc,
	)
}

// ==================== 审核表达方式 ====================

type ReviewExpressionInput struct {
	ID      uint `json:"id" jsonschema:"description=表达方式ID"`
	Approve bool `json:"approve" jsonschema:"description=是否通过审核，true=通过，false=拒绝"`
}

type ReviewExpressionOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func reviewExpressionFunc(ctx context.Context, input *ReviewExpressionInput) (*ReviewExpressionOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &ReviewExpressionOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.ID == 0 {
		return &ReviewExpressionOutput{Success: false, Message: "表达方式ID不能为空"}, nil
	}

	err := tc.MemoryMgr.ReviewExpression(input.ID, input.Approve)
	if err != nil {
		output := &ReviewExpressionOutput{Success: false, Message: err.Error()}
		LogToolCall("reviewExpression", input, output, err)
		return output, nil
	}

	msg := "已拒绝该表达方式"
	if input.Approve {
		msg = "已通过该表达方式"
	}
	output := &ReviewExpressionOutput{Success: true, Message: msg}
	LogToolCall("reviewExpression", input, output, nil)
	return output, nil
}

func NewReviewExpressionTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"reviewExpression",
		"审核一条表达方式。如果你认为这个表达方式记录正确，可以通过；如果有误，可以拒绝。",
		reviewExpressionFunc,
	)
}

// ==================== 获取待审核黑话工具 ====================

type GetUnverifiedJargonsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=返回数量，默认5"`
}

type UnverifiedJargonItem struct {
	ID      uint   `json:"id"`
	Content string `json:"content"`
	Meaning string `json:"meaning"`
	Context string `json:"context"`
	Count   int    `json:"count"`
}

type GetUnverifiedJargonsOutput struct {
	Success bool                   `json:"success"`
	Jargons []UnverifiedJargonItem `json:"jargons,omitempty"`
	Message string                 `json:"message,omitempty"`
}

func getUnverifiedJargonsFunc(ctx context.Context, input *GetUnverifiedJargonsInput) (*GetUnverifiedJargonsOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &GetUnverifiedJargonsOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 5
	}

	jargons, err := tc.MemoryMgr.GetUnverifiedJargons(tc.GroupID, limit)
	if err != nil {
		output := &GetUnverifiedJargonsOutput{Success: false, Message: err.Error()}
		LogToolCall("getUnverifiedJargons", input, output, err)
		return output, nil
	}

	results := make([]UnverifiedJargonItem, 0, len(jargons))
	for _, j := range jargons {
		results = append(results, UnverifiedJargonItem{
			ID:      j.ID,
			Content: j.Content,
			Meaning: j.Meaning,
			Context: j.Context,
			Count:   j.Count,
		})
	}

	output := &GetUnverifiedJargonsOutput{Success: true, Jargons: results}
	LogToolCall("getUnverifiedJargons", input, output, nil)
	return output, nil
}

func NewGetUnverifiedJargonsTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getUnverifiedJargons",
		"查看待审核的黑话/术语。你可以检查这些黑话的含义是否准确。",
		getUnverifiedJargonsFunc,
	)
}

// ==================== 审核黑话工具 ====================

type ReviewJargonInput struct {
	ID      uint `json:"id" jsonschema:"description=黑话ID"`
	Approve bool `json:"approve" jsonschema:"description=是否通过审核，true=通过，false=拒绝"`
}

type ReviewJargonOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func reviewJargonFunc(ctx context.Context, input *ReviewJargonInput) (*ReviewJargonOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &ReviewJargonOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.ID == 0 {
		return &ReviewJargonOutput{Success: false, Message: "黑话ID不能为空"}, nil
	}

	err := tc.MemoryMgr.ReviewJargon(input.ID, input.Approve)
	if err != nil {
		output := &ReviewJargonOutput{Success: false, Message: err.Error()}
		LogToolCall("reviewJargon", input, output, err)
		return output, nil
	}

	msg := "已拒绝该黑话"
	if input.Approve {
		msg = "已验证该黑话"
	}
	output := &ReviewJargonOutput{Success: true, Message: msg}
	LogToolCall("reviewJargon", input, output, nil)
	return output, nil
}

func NewReviewJargonTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"reviewJargon",
		"审核一条黑话/术语。如果含义正确，可以通过验证；如果有误，可以拒绝。",
		reviewJargonFunc,
	)
}
