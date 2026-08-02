package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"go.uber.org/zap"
)

// ==================== 发言工具 ====================

const maxSpeakMessages = 5

// SpeakMessage 单条发言及其独立的回复、提及信息。
type SpeakMessage struct {
	Content  string  `json:"content" jsonschema:"description=要发送的口语化纯文本，不使用 markdown"`
	ReplyTo  int64   `json:"reply_to,omitempty" jsonschema:"description=这条消息要回复的消息 ID"`
	Mentions []int64 `json:"mentions,omitempty" jsonschema:"description=这条消息要 @ 的用户 QQ 号列表"`
}

// SpeakInput 发言的输入参数
type SpeakInput struct {
	Messages []SpeakMessage `json:"messages" jsonschema:"description=按顺序发送的 1 至 5 条消息"`
}

// SpeakOutput 发言的输出
type SpeakOutput struct {
	Success     bool    `json:"success"`
	Complete    bool    `json:"complete"`
	MessageIDs  []int64 `json:"message_ids,omitempty"`
	FailedIndex int     `json:"failed_index,omitempty"`
	Message     string  `json:"message,omitempty"`
}

// speakFunc 发言的实际实现 - 会通过回调实际发送消息
func speakFunc(ctx context.Context, input *SpeakInput) (*SpeakOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &SpeakOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	if tc.SpeakCallback == nil {
		return &SpeakOutput{Success: false, Message: "发言回调未初始化"}, nil
	}
	if input == nil || len(input.Messages) == 0 || len(input.Messages) > maxSpeakMessages {
		return &SpeakOutput{Success: false, Message: "每次发言必须包含 1 至 5 条消息"}, nil
	}

	normalized := make([]SpeakMessage, len(input.Messages))
	for i, message := range input.Messages {
		message.Content = strings.TrimSpace(message.Content)
		if message.Content == "" {
			return &SpeakOutput{Success: false, FailedIndex: i + 1, Message: fmt.Sprintf("第 %d 条消息内容不能为空", i+1)}, nil
		}
		if message.ReplyTo < 0 {
			return &SpeakOutput{Success: false, FailedIndex: i + 1, Message: fmt.Sprintf("第 %d 条消息的 reply_to 不能为负数", i+1)}, nil
		}

		mentions := make([]int64, 0, len(message.Mentions))
		seenMentions := make(map[int64]struct{}, len(message.Mentions))
		for _, userID := range message.Mentions {
			if userID <= 0 {
				return &SpeakOutput{Success: false, FailedIndex: i + 1, Message: fmt.Sprintf("第 %d 条消息包含无效的 mentions", i+1)}, nil
			}
			if _, seen := seenMentions[userID]; seen {
				continue
			}
			seenMentions[userID] = struct{}{}
			mentions = append(mentions, userID)
		}
		message.Mentions = mentions
		normalized[i] = message
	}

	messageIDs := make([]int64, 0, len(normalized))
	for i, message := range normalized {
		messageID, err := tc.SpeakCallback(ctx, tc.GroupID, message.Content, message.ReplyTo, message.Mentions)
		if err != nil {
			return &SpeakOutput{
				Success:     len(messageIDs) > 0,
				Complete:    false,
				MessageIDs:  messageIDs,
				FailedIndex: i + 1,
				Message:     fmt.Sprintf("第 %d 条消息发送失败：%v，已发送 %d 条", i+1, err, len(messageIDs)),
			}, nil
		}
		tc.MarkActed()
		messageIDs = append(messageIDs, messageID)
	}

	return &SpeakOutput{
		Success:    true,
		Complete:   true,
		MessageIDs: messageIDs,
		Message:    fmt.Sprintf("发言成功，共发送 %d 条消息", len(messageIDs)),
	}, nil
}

// NewSpeakTool 创建发言工具
func NewSpeakTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"speak",
		`在群里发送一轮文字消息。只有真的想说什么时才使用，不用强迫自己每次都说话。

【重要】使用规则：
- 一般发送一至三条消息，只有多个相对独立的内容都需要立刻表达时才增加，最多五条；同一个意思不要硬拆
- 每条消息都应自然完整，前后顺序符合正常聊天节奏
- 每条可独立设置 reply_to 和 mentions；不要回复自己的消息，也不要在 content 里直接写 @ 符号
- content 只写群友实际会看到的口语化纯文本，不使用 markdown，不要夹带分析过程、说明、工具调用理由或“回复：”等标签`,
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
	return &StayQuietOutput{
		Success: true,
		Message: "保持沉默",
	}, nil
}

// NewStayQuietTool 创建保持沉默工具
func NewStayQuietTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"stayQuiet",
		`保持沉默并结束思考。当话题你不熟悉、不感兴趣、或者觉得没必要插嘴时可直接使用。当你已经发言完毕且不再有新的内容要表达，使用本工具来结束思考。`,
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

	if err := tc.Bot.GroupPoke(ctx, tc.GroupID, input.UserID); err != nil {
		return &PokeOutput{Success: false, Message: err.Error()}, nil
	}
	tc.MarkActed()

	return &PokeOutput{Success: true, Message: "已戳一戳"}, nil
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
	// Reaction 要使用的语义表情
	Reaction string `json:"reaction" jsonschema:"enum=thumbs_up,enum=heart,enum=clap,enum=laugh,enum=hug,enum=ok,enum=question,enum=no,enum=surprised,enum=cry,enum=facepalm,enum=cheer,enum=victory,enum=salute,enum=doge,description=要使用的表情回应"`
}

var messageReactionEmojiIDs = map[string]int{
	"thumbs_up": 76,
	"heart":     66,
	"clap":      99,
	"laugh":     182,
	"hug":       49,
	"ok":        124,
	"question":  32,
	"no":        123,
	"surprised": 180,
	"cry":       5,
	"facepalm":  264,
	"cheer":     144,
	"victory":   79,
	"salute":    282,
	"doge":      179,
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
	emojiID, ok := messageReactionEmojiIDs[input.Reaction]
	if !ok {
		return &ReactToMessageOutput{Success: false, Message: "不支持该表情回应"}, nil
	}

	if err := tc.Bot.SetMsgEmojiLike(ctx, input.MessageID, emojiID); err != nil {
		return &ReactToMessageOutput{Success: false, Message: err.Error()}, nil
	}
	tc.MarkActed()

	return &ReactToMessageOutput{Success: true, Message: "已回应表情"}, nil
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
	if tc.MemoryMgr == nil {
		return &RecallMessageOutput{Success: false, Message: "记忆管理器未初始化"}, nil
	}

	log, err := tc.MemoryMgr.GetMessageLogByID(input.MessageID)
	if err != nil {
		return &RecallMessageOutput{Success: false, Message: err.Error()}, nil
	}
	if log == nil {
		return &RecallMessageOutput{Success: false, Message: "未找到该消息记录，无法确认是否还能撤回"}, nil
	}
	if log.GroupID != tc.GroupID {
		return &RecallMessageOutput{Success: false, Message: "该消息不属于当前群"}, nil
	}
	if selfID := tc.Bot.GetSelfID(); selfID > 0 && log.UserID != 0 && log.UserID != selfID {
		return &RecallMessageOutput{Success: false, Message: "只能撤回你自己发的消息"}, nil
	}
	if log.RecalledAt != nil {
		return &RecallMessageOutput{Success: true, Message: "消息已撤回"}, nil
	}
	if time.Since(log.MessageTime) > 2*time.Minute {
		return &RecallMessageOutput{Success: false, Message: "消息已超过两分钟，无法撤回"}, nil
	}

	if err := tc.Bot.DeleteMsg(ctx, input.MessageID); err != nil {
		return &RecallMessageOutput{Success: false, Message: err.Error()}, nil
	}
	tc.MarkActed()
	recalled, changed, syncErr := tc.MemoryMgr.MarkMessageRecalled(log.GroupID, input.MessageID)
	if syncErr != nil {
		zap.L().Error("主动撤回成功但同步本地状态失败", zap.Int64("group_id", log.GroupID), zap.Int64("message_id", input.MessageID), zap.Error(syncErr))
		return &RecallMessageOutput{Success: true, Message: "已撤回消息"}, nil
	}
	if changed && tc.MessageRecalledCallback != nil {
		tc.MessageRecalledCallback(recalled)
	}

	return &RecallMessageOutput{Success: true, Message: "已撤回消息"}, nil
}

// NewRecallMessageTool 创建撤回消息工具
func NewRecallMessageTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"recallMessage",
		"撤回你自己发的消息。当你发错消息、说错话、或者想收回刚才的发言时使用。",
		recallMessageFunc,
	)
}
