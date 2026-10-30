package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"gorm.io/gorm"
)

// ==================== 发言工具 ====================

// SpeakInput 发言的输入参数
type SpeakInput struct {
	// Content 你想说的话
	Content string `json:"content" jsonschema:"description=说话内容"`
	// ReplyTo 要回复的 OneBot 消息 ID（可选）
	ReplyTo int64 `json:"reply_to,omitempty" jsonschema:"description=要回复的 OneBot 消息 ID"`
	// Mentions 要提及的用户 QQ 号列表（可选）
	Mentions []int64 `json:"mentions,omitempty" jsonschema:"description=要@的用户QQ号列表"`
}

// PrivateSpeakInput 私聊发言的输入参数
type PrivateSpeakInput struct {
	Content string `json:"content" jsonschema:"description=说话内容"`
	ReplyTo int64  `json:"reply_to,omitempty" jsonschema:"description=要回复的 OneBot 消息 ID"`
}

// SpeakOutput 发言的输出
type SpeakOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// speakFunc 发言的实际实现 - 会通过回调实际发送消息
func speakContent(ctx context.Context, content string, replyTo int64, inputMentions []int64) (*SpeakOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if tc.SpeakCallback == nil {
		return nil, NewTerminalToolError(fmt.Errorf("发言回调未初始化"))
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("说话内容不能为空")
	}

	mentions := make([]int64, 0, len(inputMentions))
	seenMentions := make(map[int64]struct{}, len(inputMentions))
	for _, userID := range inputMentions {
		if userID <= 0 {
			return nil, fmt.Errorf("包含无效的 mentions")
		}
		if _, seen := seenMentions[userID]; seen {
			continue
		}
		seenMentions[userID] = struct{}{}
		mentions = append(mentions, userID)
	}

	if err := tc.SpeakCallback(ctx, tc.TargetID, content, replyTo, mentions); err != nil {
		return nil, err
	}

	return &SpeakOutput{
		Success: true,
		Message: "发言成功",
	}, nil
}

func speakFunc(ctx context.Context, input *SpeakInput) (*SpeakOutput, error) {
	if input == nil {
		return nil, fmt.Errorf("说话参数不能为空")
	}
	return speakContent(ctx, input.Content, input.ReplyTo, input.Mentions)
}

func privateSpeakFunc(ctx context.Context, input *PrivateSpeakInput) (*SpeakOutput, error) {
	if input == nil {
		return nil, fmt.Errorf("说话参数不能为空")
	}
	return speakContent(ctx, input.Content, input.ReplyTo, nil)
}

// NewSpeakTool 创建发言工具
func NewSpeakTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"speak",
		`在群里说话。只有当你真的想说什么的时候才用，不用强迫自己每次都说话。
【重要】使用规则：
- speak每次只能发送**一条消息**，一条消息只承载一个可独立成立的意思；需要表达多个意思时，请调用多次speak
- “一条消息”按意思划分，不按句号数量判断；即使语法上是一句话，只要包含多个判断、原因、转折或建议，也要拆开
- 不要用逗号、空格、分号、句号或换行符串联多个本可独立发送的意思
- 要回复某条消息时填写 reply_to（OneBot 消息 ID）参数，只有当发言内容与该消息强相关时才用；不要回复自己的消息
- 要at群友时用mentions参数（可同时at多个人），不要在content里直接写"@"符号
- 想发表情时在content里写%qface:名称%或%qface:编号%（例如%qface:微笑%），可以夹在文字中间任意位置；名称和编号先用searchFaces查。图片表情包用sendSticker单独发，不要写进文字里`,
		speakFunc,
	)
}

// NewPrivateSpeakTool 创建私聊发言工具
func NewPrivateSpeakTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"speak",
		`在私聊里说话。每次只能发送一条消息；需要回复某条消息时填写 reply_to（OneBot 消息 ID），不需要填写 mentions。想发表情时在 content 里写 %qface:名称% 或 %qface:编号%，名称和编号先用 searchFaces 查；图片表情包用 sendSticker 单独发。`,
		privateSpeakFunc,
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
	// UserID 要戳的群成员 QQ 号
	UserID int64 `json:"user_id" jsonschema:"description=要戳的群成员QQ号"`
}

// PrivatePokeInput 私聊戳一戳的输入参数
type PrivatePokeInput struct{}

// PokeOutput 戳一戳的输出
type PokeOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// pokeFunc 戳一戳的实际实现
func pokeFunc(ctx context.Context, input *PokeInput) (*PokeOutput, error) {
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
	if err := tc.Bot.GroupPoke(ctx, tc.TargetID, input.UserID); err != nil {
		return nil, err
	}

	return &PokeOutput{Success: true, Message: "已戳一戳"}, nil
}

func privatePokeFunc(ctx context.Context, _ *PrivatePokeInput) (*PokeOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if tc.ConversationKind != memory.ConversationKindPrivate {
		return nil, fmt.Errorf("该工具只能在私聊中使用")
	}
	if tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("机器人未连接"))
	}
	if err := tc.Bot.FriendPoke(ctx, tc.TargetID); err != nil {
		return nil, err
	}
	return &PokeOutput{Success: true, Message: "已戳一戳"}, nil
}

// NewPokeTool 创建戳一戳工具
func NewPokeTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"poke",
		"戳一戳当前群里的群友。可以用来打招呼、吸引注意力或逗逗对方，不要频繁使用。",
		pokeFunc,
	)
}

// NewPrivatePokeTool 创建私聊戳一戳工具
func NewPrivatePokeTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"poke",
		"戳一戳当前私聊好友。可以用来打招呼、吸引注意力或逗逗对方，不需要填写 QQ 号。不要频繁使用。",
		privatePokeFunc,
	)
}

// ==================== 消息贴表情工具 ====================

// ReactToMessageInput 对消息贴表情的输入参数
type ReactToMessageInput struct {
	MessageID int64 `json:"message_id" jsonschema:"description=OneBot 消息 ID"`
	// Reaction 要使用的语义表情
	Reaction string `json:"reaction" jsonschema:"enum=thumbs_up,enum=heart,enum=clap,enum=laugh,enum=hug,enum=ok,enum=question,enum=no,enum=cry,enum=facepalm,enum=cheer,enum=victory,enum=salute,enum=doge,description=要使用的表情回应"`
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
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("机器人未连接"))
	}
	if input == nil {
		return nil, fmt.Errorf("消息回应参数不能为空")
	}
	messageID := input.MessageID
	if messageID == 0 {
		return nil, fmt.Errorf("消息 ID 不能为空")
	}
	emojiID, ok := messageReactionEmojiIDs[input.Reaction]
	if !ok {
		return nil, fmt.Errorf("不支持该表情回应")
	}

	if err := tc.Bot.SetMsgEmojiLike(ctx, messageID, emojiID); err != nil {
		return nil, err
	}

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
	MessageID int64 `json:"message_id" jsonschema:"description=OneBot 消息 ID"`
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
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("机器人未连接"))
	}
	if tc.RecallCallback == nil {
		return nil, NewTerminalToolError(fmt.Errorf("撤回提交能力未初始化"))
	}
	if input == nil {
		return nil, fmt.Errorf("撤回参数不能为空")
	}
	messageID := input.MessageID
	if messageID == 0 {
		return nil, fmt.Errorf("消息 ID 不能为空")
	}
	if tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("记忆管理器未初始化"))
	}

	log, err := tc.MemoryMgr.WithContext(ctx).GetMessageLogByScope(tc.ConversationKind, tc.TargetID, messageID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err == nil && log != nil {
		if log.TargetID != tc.TargetID || (tc.ConversationKind != "" && log.ConversationKind != tc.ConversationKind) {
			return nil, fmt.Errorf("该消息不属于当前会话")
		}
		if selfID := tc.Bot.GetSelfID(); selfID > 0 && log.UserID != 0 && log.UserID != selfID {
			return nil, fmt.Errorf("只能撤回你自己发的消息")
		}
		if log.RecalledAt != nil {
			return &RecallMessageOutput{Success: true, Message: "消息已撤回"}, nil
		}
		if time.Since(log.MessageTime) > 2*time.Minute {
			return nil, fmt.Errorf("消息已超过两分钟，无法撤回")
		}
	}

	arrivalSeq, err := tc.Bot.DeleteMsg(ctx, tc.ConversationKind, tc.TargetID, messageID)
	if err != nil {
		return nil, err
	}
	tc.RecallCallback(messageID, arrivalSeq)

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
