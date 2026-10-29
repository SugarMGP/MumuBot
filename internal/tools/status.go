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
	Success      bool                    `json:"success"`
	Time         string                  `json:"time"`
	Group        string                  `json:"group,omitempty"`
	Peer         string                  `json:"peer,omitempty"`
	Mood         string                  `json:"mood,omitempty"`
	Emotions     []memory.CurrentEmotion `json:"emotions,omitempty"`
	Intimacy     float64                 `json:"intimacy,omitempty"`
	IntimacyName string                  `json:"intimacy_name,omitempty"`
}

func NewGetCurrentStatusTool() (tool.InvokableTool, error) {
	return utils.InferTool("getCurrentStatus", "读取当前时间、会话对象、具体情感、精力、社交意愿和好感。", getCurrentStatusFunc)
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
			output.Intimacy = profile.Intimacy
			output.IntimacyName = memory.IntimacyLevelName(profile.Intimacy)
		}
	}
	if mood, err := tc.MemoryMgr.GetMoodState(); err == nil && mood != nil {
		output.Mood = fmt.Sprintf("精力：%s；交流意愿：%s", affectText(mood.Energy), affectText(mood.Sociability))
	}
	output.Emotions, _ = tc.MemoryMgr.CurrentEmotions(ctx, -1, 3)
	return output, nil
}

func affectText(value float64) string {
	switch {
	case value >= 0.67:
		return "高"
	case value <= 0.33:
		return "低"
	default:
		return "一般"
	}
}

func weekdayText(weekday time.Weekday) string {
	return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[weekday]
}
