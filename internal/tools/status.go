package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type GetCurrentStatusOutput struct {
	Success  bool                    `json:"success"`
	Time     string                  `json:"time"`
	Group    string                  `json:"group,omitempty"`
	Peer     string                  `json:"peer,omitempty"`
	Mood     string                  `json:"mood,omitempty"`
	Emotions []memory.CurrentEmotion `json:"emotions,omitempty"`
}

func NewGetCurrentStatusTool() (tool.InvokableTool, error) {
	return utils.InferTool("getCurrentStatus", "读取当前时间、会话对象、具体情感、精力和交流意愿。", getCurrentStatusFunc)
}

func getCurrentStatusFunc(ctx context.Context, _ *struct{}) (*GetCurrentStatusOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("状态读取能力未初始化"))
	}
	now := time.Now()
	output := &GetCurrentStatusOutput{Success: true, Time: now.Format("2006-01-02 15:04:05") + " " + weekdayText(now.Weekday())}
	if tc.GroupInfoCallback != nil {
		output.Group = strings.TrimSpace(tc.GroupInfoCallback(ctx))
	}
	if tc.ConversationKind == memory.ConversationKindPrivate {
		if profile, err := tc.MemoryMgr.GetMemberProfile(tc.TargetID); err == nil && profile != nil {
			if nickname := strings.TrimSpace(profile.Nickname); nickname != "" {
				output.Peer = fmt.Sprintf("%s(%d)", nickname, tc.TargetID)
			}
		}
	}
	if mood, err := tc.MemoryMgr.GetMoodState(); err == nil && mood != nil {
		output.Mood = fmt.Sprintf("精力：%s；交流意愿：%s", memory.AffectLevelText(mood.Energy), memory.AffectLevelText(mood.Sociability))
		tc.MarkReferenceSeen("state:affect", mood.UpdatedAt.UTC().Format("20060102150405"))
	}
	output.Emotions, _ = tc.MemoryMgr.CurrentEmotions(ctx, -1, 3)
	for _, emotion := range output.Emotions {
		tc.MarkReferenceSeen(fmt.Sprintf("emotion:%d", emotion.ID), fmt.Sprintf("v%d:%s", emotion.Version, memory.EmotionStrengthTier(emotion.Strength)))
	}
	return output, nil
}

func weekdayText(weekday time.Weekday) string {
	return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[weekday]
}
