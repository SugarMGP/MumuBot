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

// GetCurrentStatusOutput 当前状态查询结果
type GetCurrentStatusOutput struct {
	Success bool   `json:"success"`
	Time    string `json:"time"`
	Group   string `json:"group,omitempty"`
	Peer    string `json:"peer,omitempty"`
	Mood    string `json:"mood,omitempty"`
	Note    string `json:"note,omitempty"`
}

// NewGetCurrentStatusTool 返回读取当前时间、群信息、情绪与工作便签的工具
func NewGetCurrentStatusTool() (tool.InvokableTool, error) {
	return utils.InferTool("getCurrentStatus", "读取当前时间、所在群或对方的基本信息、你此刻的情绪状态和上一轮的工作便签。需要知道现在几点、群里什么情况、你上次答应过什么时调用。", getCurrentStatusFunc)
}

// getCurrentStatusFunc 汇总当前状态
func getCurrentStatusFunc(ctx context.Context, _ *struct{}) (*GetCurrentStatusOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("状态读取能力未初始化"))
	}
	now := time.Now()
	output := &GetCurrentStatusOutput{
		Success: true,
		Time:    now.Format("2006-01-02 15:04:05") + " " + weekdayText(now.Weekday()),
	}
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
		output.Mood = fmt.Sprintf("心情 %.2f、精力 %.2f、社交意愿 %.2f", mood.Valence, mood.Energy, mood.Sociability)
	}
	if note, err := tc.MemoryMgr.GetWorkingNoteScope(ctx, tc.ConversationKind, tc.TargetID); err == nil && note != nil {
		output.Note = strings.TrimSpace(note.Note)
	}
	return output, nil
}

// weekdayText 星期中文
func weekdayText(weekday time.Weekday) string {
	return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[weekday]
}
