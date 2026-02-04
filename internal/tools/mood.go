package tools

import (
	"context"

	mutils "mumu-bot/internal/utils"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ==================== 情绪更新工具 ====================

// UpdateMoodInput 更新情绪的输入参数
type UpdateMoodInput struct {
	// ValenceDelta 心情变化量，正数变好，负数变差
	ValenceDelta float64 `json:"valence_delta" jsonschema:"description=心情变化量：正数心情变好，负数心情变差。范围-0.5~0.5"`
	// EnergyDelta 精力变化量，正数更活跃，负数更疲惫
	EnergyDelta float64 `json:"energy_delta" jsonschema:"description=精力变化量：正数更有活力，负数更疲惫。范围-0.3~0.3"`
	// SociabilityDelta 社交意愿变化量，正数更想聊，负数更想安静
	SociabilityDelta float64 `json:"sociability_delta" jsonschema:"description=社交意愿变化量：正数更想聊天，负数更想安静。范围-0.3~0.3"`
	// Reason 情绪变化的原因（可选）
	Reason string `json:"reason,omitempty" jsonschema:"description=情绪变化的原因（给自己看的笔记，可选）"`
}

// UpdateMoodOutput 更新情绪的输出
type UpdateMoodOutput struct {
	Success     bool    `json:"success"`
	Message     string  `json:"message,omitempty"`
	Valence     float64 `json:"valence"`     // 更新后的心情值
	Energy      float64 `json:"energy"`      // 更新后的精力值
	Sociability float64 `json:"sociability"` // 更新后的社交意愿值
}

// updateMoodFunc 更新情绪的实际实现
func updateMoodFunc(ctx context.Context, input *UpdateMoodInput) (*UpdateMoodOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &UpdateMoodOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	if tc.MemoryMgr == nil {
		return &UpdateMoodOutput{Success: false, Message: "记忆管理器未初始化"}, nil
	}

	// 限制单次变化量，防止极端变化
	valenceDelta := mutils.ClampFloat64(input.ValenceDelta, -0.5, 0.5)
	energyDelta := mutils.ClampFloat64(input.EnergyDelta, -0.3, 0.3)
	sociabilityDelta := mutils.ClampFloat64(input.SociabilityDelta, -0.3, 0.3)

	mood, err := tc.MemoryMgr.UpdateMoodState(valenceDelta, energyDelta, sociabilityDelta, input.Reason)
	if err != nil {
		output := &UpdateMoodOutput{Success: false, Message: "更新情绪失败: " + err.Error()}
		LogToolCall("updateMood", input, output, err)
		return output, nil
	}

	output := &UpdateMoodOutput{
		Success:     true,
		Message:     "情绪已更新",
		Valence:     mood.Valence,
		Energy:      mood.Energy,
		Sociability: mood.Sociability,
	}
	LogToolCall("updateMood", input, output, nil)
	return output, nil
}

// NewUpdateMoodTool 创建更新情绪工具
func NewUpdateMoodTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"updateMood",
		`调整你的情绪状态。情绪会自然衰减回归平静，但你可以根据对话内容主动调整。

【使用建议】
- 不需要每次都调整，只有明确感受到情绪变化时才调用
- 变化量建议小幅度（±0.1~0.2），除非发生重大事件
- 例如：被夸了: valence +0.2；聊太久了: energy -0.1；话题无聊: sociability -0.15`,
		updateMoodFunc,
	)
}
