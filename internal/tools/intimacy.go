package tools

import (
	"context"
	"fmt"
	"strconv"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// UpdateMemberIntimacyInput 群聊调整成员好感度的输入参数
type UpdateMemberIntimacyInput struct {
	UserID int64  `json:"user_id" jsonschema:"description=要调整好感度的群友 QQ 号"`
	Change string `json:"change" jsonschema:"enum=strong_negative,enum=negative,enum=positive,enum=strong_positive,description=好感度变化档位"`
	Reason string `json:"reason,omitempty" jsonschema:"description=触发这次变化的具体原因，一句短语，例如“耐心解答了他的问题”"`
}

// UpdatePrivateIntimacyInput 私聊调整当前好友好感度的输入参数
type UpdatePrivateIntimacyInput struct {
	Change string `json:"change" jsonschema:"enum=strong_negative,enum=negative,enum=positive,enum=strong_positive,description=好感度变化档位"`
	Reason string `json:"reason,omitempty" jsonschema:"description=触发这次变化的具体原因，一句短语"`
}

type UpdateMemberIntimacyOutput struct {
	Success   bool    `json:"success"`
	Message   string  `json:"message"`
	Intimacy  float64 `json:"intimacy,omitempty"`
	Level     int     `json:"level,omitempty"`
	LevelName string  `json:"level_name,omitempty"`
}

var intimacyDeltas = map[string]float64{
	"strong_negative": -0.15,
	"negative":        -0.05,
	"positive":        0.05,
	"strong_positive": 0.15,
}

func updateMemberIntimacy(ctx context.Context, userID int64, change, reason string) (*UpdateMemberIntimacyOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if userID <= 0 {
		return nil, fmt.Errorf("成员 QQ 号无效")
	}
	if tc.Bot != nil && userID == tc.Bot.GetSelfID() {
		return nil, fmt.Errorf("不能调整自己的好感度")
	}

	delta, ok := intimacyDeltas[change]
	if !ok {
		return nil, fmt.Errorf("好感度变化档位无效")
	}
	dedupKey := strconv.FormatInt(userID, 10)
	if tc.IsToolCallSeen("updateMemberIntimacy:user", dedupKey) {
		return &UpdateMemberIntimacyOutput{Success: true, Message: "本轮已经调整过该成员，未重复调整"}, nil
	}

	profile, err := tc.MemoryMgr.UpdateMemberIntimacy(userID, delta, reason)
	if err != nil {
		return nil, NewTerminalToolError(fmt.Errorf("更新好感度失败: %w", err))
	}
	tc.MarkToolCallSucceeded("updateMemberIntimacy:user", dedupKey)
	tc.MarkActionSucceeded()
	return &UpdateMemberIntimacyOutput{Success: true, Message: "好感度已更新", Intimacy: profile.Intimacy, Level: memory.IntimacyLevel(profile.Intimacy), LevelName: memory.IntimacyLevelName(profile.Intimacy)}, nil
}

func updateMemberIntimacyFunc(ctx context.Context, input *UpdateMemberIntimacyInput) (*UpdateMemberIntimacyOutput, error) {
	if input == nil {
		return nil, fmt.Errorf("好感度参数不能为空")
	}
	return updateMemberIntimacy(ctx, input.UserID, input.Change, input.Reason)
}

func updatePrivateIntimacyFunc(ctx context.Context, input *UpdatePrivateIntimacyInput) (*UpdateMemberIntimacyOutput, error) {
	if input == nil {
		return nil, fmt.Errorf("好感度参数不能为空")
	}
	tc := GetToolContext(ctx)
	if tc == nil || tc.ConversationKind != memory.ConversationKindPrivate {
		return nil, fmt.Errorf("该工具只能在私聊中使用")
	}
	return updateMemberIntimacy(ctx, tc.TargetID, input.Change, input.Reason)
}

func NewUpdateMemberIntimacyTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"updateMemberIntimacy",
		`根据当前聊天调整你对某个群友的好感度。变化档位固定为：strong_negative=-0.15、negative=-0.05、positive=+0.05、strong_positive=+0.15。每轮同一成员最多调整一次。reason 用一句短语说明这次变化的直接原因，会展示在后台。`,
		updateMemberIntimacyFunc,
	)
}

// NewPrivateUpdateMemberIntimacyTool 创建私聊好感度工具
func NewPrivateUpdateMemberIntimacyTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"updateMemberIntimacy",
		`根据当前私聊好友的表现调整你对他的好感度，不需要填写 QQ 号。变化档位固定为：strong_negative=-0.15、negative=-0.05、positive=+0.05、strong_positive=+0.15。每轮最多调整一次。`,
		updatePrivateIntimacyFunc,
	)
}
