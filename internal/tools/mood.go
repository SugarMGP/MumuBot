package tools

import (
	"context"
	"fmt"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type EmotionChangeInput struct {
	EmotionID   uint   `json:"emotion_id,omitempty"`
	Kind        string `json:"kind" jsonschema:"description=喜悦/好奇/亲近/感动/思念/吃醋/担忧/安心/委屈/失落/不满/愧疚"`
	AboutUserID int64  `json:"about_user_id,omitempty" jsonschema:"description=0 表示无特定对象，正数表示人物 QQ"`
	Intensity   string `json:"intensity" jsonschema:"description=轻微/明显/强烈/已平复"`
	DecayClass  string `json:"decay_class,omitempty" jsonschema:"description=brief 或 lingering"`
	Reason      string `json:"reason,omitempty"`
}

type UpdateMoodInput struct {
	Changes     []EmotionChangeInput `json:"changes,omitempty"`
	Energy      *string              `json:"energy,omitempty" jsonschema:"description=low/normal/high"`
	Sociability *string              `json:"sociability,omitempty" jsonschema:"description=low/normal/high"`
	Reason      string               `json:"reason,omitempty"`
}

func updateMoodFunc(ctx context.Context, input *UpdateMoodInput) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("情绪能力未初始化"))
	}
	if input == nil {
		input = &UpdateMoodInput{}
	}
	changes := make([]memory.EmotionChange, 0, len(input.Changes))
	for _, raw := range input.Changes {
		about := raw.AboutUserID
		if about == 0 && tc.ConversationKind == memory.ConversationKindPrivate {
			about = tc.TargetID
		}
		source, err := sourceMetaJSON(tc, nil)
		if err != nil {
			return nil, err
		}
		changes = append(changes, memory.EmotionChange{
			EmotionID: raw.EmotionID, Kind: raw.Kind, AboutUserID: about,
			Intensity: raw.Intensity, DecayClass: raw.DecayClass, Reason: raw.Reason, SourceMeta: source,
		})
	}
	var emotions []memory.CurrentEmotion
	var err error
	if len(changes) > 0 {
		emotions, err = tc.MemoryMgr.UpdateEmotions(ctx, changes)
		if err != nil {
			return nil, err
		}
	}
	if input.Energy != nil || input.Sociability != nil {
		current, getErr := tc.MemoryMgr.GetMoodState()
		if getErr != nil {
			return nil, getErr
		}
		energy, sociability := current.Energy, current.Sociability
		if input.Energy != nil {
			energy = memory.AffectLevel(*input.Energy, energy)
		}
		if input.Sociability != nil {
			sociability = memory.AffectLevel(*input.Sociability, sociability)
		}
		if _, err := tc.MemoryMgr.UpdateAffectState(ctx, energy, sociability, input.Reason); err != nil {
			return nil, err
		}
	}
	all, _ := tc.MemoryMgr.CurrentEmotions(ctx, -1, 3)
	return map[string]any{"success": true, "emotions": emotions, "current": all}, nil
}

func NewUpdateMoodTool() (tool.InvokableTool, error) {
	return utils.InferTool("updateMood", "更新具体情感、精力和社交意愿。情感必须说明对象、原因和强度；不确定时不要更新。", updateMoodFunc)
}
