package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ==================== 发言工具 ====================

// SpeakInput 发言的输入参数
type SpeakInput struct {
	// Content 你想说的话
	Content string `json:"content" jsonschema:"description=你想说的话，不要用markdown，说话要口语化"`
	// ReplyTo 要回复的消息ID（可选）
	ReplyTo int64 `json:"reply_to,omitempty" jsonschema:"description=要回复的消息ID"`
	// Mentions 要@的用户QQ号列表（可选）
	Mentions []int64 `json:"mentions,omitempty" jsonschema:"description=要@的用户QQ号列表"`
}

// SpeakOutput 发言的输出
type SpeakOutput struct {
	Success   bool   `json:"success"`
	MessageID int64  `json:"message_id,omitempty"` // 发送成功后的消息 ID
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
		msgID = tc.SpeakCallback(tc.GroupID, input.Content, input.ReplyTo, input.Mentions)
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
- 错误示例：speak("你好。今天天气真好") 或 speak("你好\n今天天气真好")
- 在明确需要回复某条消息时使用 reply_to 参数，但是不要回复自己说的话
- 如果想@某个群友，请使用 mentions 参数（可以同时@多个人），不要在内容里直接写 @XXX`,
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
		return &PokeOutput{Success: false, Message: "Bot 未连接"}, nil
	}
	if input.UserID == 0 {
		return &PokeOutput{Success: false, Message: "用户 ID 不能为空"}, nil
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
		return &ReactToMessageOutput{Success: false, Message: "Bot 未连接"}, nil
	}
	if input.MessageID == 0 {
		return &ReactToMessageOutput{Success: false, Message: "消息 ID 不能为空"}, nil
	}
	if input.EmojiID == 0 {
		return &ReactToMessageOutput{Success: false, Message: "表情 ID 不能为空"}, nil
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
		return &RecallMessageOutput{Success: false, Message: "Bot 未连接"}, nil
	}
	if input.MessageID == 0 {
		return &RecallMessageOutput{Success: false, Message: "消息 ID 不能为空"}, nil
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
