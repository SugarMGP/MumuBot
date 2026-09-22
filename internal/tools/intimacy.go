package tools

import (
	"context"
	"fmt"
	"strconv"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type UpdateMemberIntimacyInput struct {
	UserID int64  `json:"user_id" jsonschema:"description=要调整好感度的群友 QQ 号"`
	Change string `json:"change" jsonschema:"enum=strong_negative,enum=negative,enum=positive,enum=strong_positive,description=好感度变化档位；分别对应 -0.15、-0.05、+0.05、+0.15"`
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

func updateMemberIntimacyFunc(ctx context.Context, input *UpdateMemberIntimacyInput) (*UpdateMemberIntimacyOutput, error) {
	if input == nil {
		return nil, fmt.Errorf("好感度参数不能为空")
	}
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if input.UserID <= 0 {
		return nil, fmt.Errorf("成员 QQ 号无效")
	}
	if tc.Bot != nil && input.UserID == tc.Bot.GetSelfID() {
		return nil, fmt.Errorf("不能调整自己的好感度")
	}

	delta := intimacyDeltas[input.Change]
	dedupKey := strconv.FormatInt(input.UserID, 10)
	if tc.IsToolCallSeen("updateMemberIntimacy:user", dedupKey) {
		return &UpdateMemberIntimacyOutput{Success: true, Message: "本轮已经调整过该成员，未重复调整"}, nil
	}

	profile, err := tc.MemoryMgr.UpdateMemberIntimacy(input.UserID, delta)
	if err != nil {
		return nil, NewTerminalToolError(fmt.Errorf("更新好感度失败: %w", err))
	}
	tc.MarkToolCallSucceeded("updateMemberIntimacy:user", dedupKey)
	tc.MarkActed()
	return &UpdateMemberIntimacyOutput{Success: true, Message: "好感度已更新", Intimacy: profile.Intimacy, Level: memory.IntimacyLevel(profile.Intimacy), LevelName: memory.IntimacyLevelName(profile.Intimacy)}, nil
}

func NewUpdateMemberIntimacyTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"updateMemberIntimacy",
		`根据当前聊天调整你对某个群友的好感度。只能在当前对话明确体现出关系变化时调用；普通聊天、意见不同、没有回应和不确定的语气都不要调整。
变化档位固定为：strong_negative=-0.15、negative=-0.05、positive=+0.05、strong_positive=+0.15。每轮同一成员最多调整一次。`,
		updateMemberIntimacyFunc,
	)
}
