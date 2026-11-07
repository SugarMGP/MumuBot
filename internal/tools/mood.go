package tools

import (
	"context"
	"fmt"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type EmotionChangeInput struct {
	ID        uint    `json:"id,omitempty" jsonschema:"description=已有感受的条目 ID；新建时省略"`
	Version   uint64  `json:"version,omitempty" jsonschema:"description=已有感受的当前版本；新建时省略"`
	Kind      *string `json:"kind,omitempty" jsonschema:"description=自由名称或简短描述，最多100字；新建必填，修改时省略表示沿用"`
	Fact      *string `json:"fact,omitempty" jsonschema:"description=引发或改变感受的已知经过，最多2000字；新建必填，修改时省略表示沿用"`
	Reason    *string `json:"reason,omitempty" jsonschema:"description=这件事对你的意义、期待、归因、矛盾和不确定处，最多2000字；新建必填，修改时省略表示沿用"`
	Intensity string  `json:"intensity,omitempty" jsonschema:"description=轻微/明显/强烈；新建必填。修改时省略表示不重设强度。已平复表示明确结束这条感受，需同时给 reason"`
}

// UpdateMoodInput 是聊天场合的情感入参：精力与交流意愿由 life 统一维护，这里不暴露
type UpdateMoodInput struct {
	Changes []EmotionChangeInput `json:"changes,omitempty"`
}

// LifeUpdateMoodInput 是生活主线的情感入参，额外允许改写共享的精力与交流意愿
type LifeUpdateMoodInput struct {
	Changes     []EmotionChangeInput `json:"changes,omitempty"`
	Energy      *string              `json:"energy,omitempty" jsonschema:"description=low/normal/high"`
	Sociability *string              `json:"sociability,omitempty" jsonschema:"description=low/normal/high"`
	Reason      string               `json:"reason,omitempty"`
}

type moodUpdate struct {
	changes     []EmotionChangeInput
	energy      *string
	sociability *string
	reason      string
}

func applyMoodUpdate(ctx context.Context, update moodUpdate) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("情绪能力未初始化"))
	}
	hasAffect := update.energy != nil || update.sociability != nil
	// 防御：即使误把精力字段传到聊天工具，也不能改共享生活状态
	if hasAffect && tc.ConversationKind != memory.ConversationKindLife {
		return nil, fmt.Errorf("精力和交流意愿由 life 统一维护；这里只更新具体情感")
	}
	if len(update.changes) == 0 && !hasAffect {
		return nil, fmt.Errorf("需要至少一项情感或精力变化")
	}
	changes := make([]memory.EmotionChange, 0, len(update.changes))
	for _, raw := range update.changes {
		changes = append(changes, memory.EmotionChange{
			ID: raw.ID, Version: raw.Version,
			Kind: raw.Kind, Fact: raw.Fact, Reason: raw.Reason, Intensity: raw.Intensity,
		})
	}
	var emotions []memory.CurrentEmotion
	var mood *memory.MoodState
	var err error
	if hasAffect {
		emotions, mood, err = tc.MemoryMgr.UpdateEmotionsAndAffect(ctx, changes, memory.AffectUpdate{
			Energy: update.energy, Sociability: update.sociability, Reason: update.reason,
		})
	} else {
		emotions, err = tc.MemoryMgr.UpdateEmotions(ctx, changes)
	}
	if err != nil {
		return nil, err
	}
	result := map[string]any{"success": true, "emotions": emotions}
	if mood != nil {
		result["affect"] = map[string]any{
			"energy": mood.Energy, "sociability": mood.Sociability, "updated_at": mood.UpdatedAt,
		}
	}
	return result, nil
}

func updateMoodFunc(ctx context.Context, input *UpdateMoodInput) (any, error) {
	if input == nil {
		input = &UpdateMoodInput{}
	}
	return applyMoodUpdate(ctx, moodUpdate{changes: input.Changes})
}

func lifeUpdateMoodFunc(ctx context.Context, input *LifeUpdateMoodInput) (any, error) {
	if input == nil {
		input = &LifeUpdateMoodInput{}
	}
	return applyMoodUpdate(ctx, moodUpdate{
		changes: input.Changes, energy: input.Energy, sociability: input.Sociability, reason: input.Reason,
	})
}

func NewUpdateMoodTool() (tool.InvokableTool, error) {
	return utils.InferTool("updateMood", "更新具体情感。情感必须说明对象、原因和强度；不确定时不要更新。精力和交流意愿由 life 统一维护，这个场合不能修改。", updateMoodFunc)
}

func NewLifeUpdateMoodTool() (tool.InvokableTool, error) {
	return utils.InferTool("updateMood", "更新具体情感、精力和交流意愿。情感必须说明对象、原因和强度；精力和交流意愿是你统一维护的共享状态，不确定时不要更新。", lifeUpdateMoodFunc)
}
