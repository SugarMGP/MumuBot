package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelstats"
	"mumu-bot/internal/schedule"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type GetCurrentStatusOutput struct {
	DailyUsage            *modelstats.DailyUsage  `json:"daily_usage,omitempty"`
	DailyUsageUnavailable bool                    `json:"daily_usage_unavailable,omitempty"`
	Success               bool                    `json:"success"`
	Time                  string                  `json:"time"`
	ConversationKind      string                  `json:"conversation_kind"`
	TargetID              int64                   `json:"target_id"`
	Group                 string                  `json:"group,omitempty"`
	Peer                  string                  `json:"peer,omitempty"`
	Mood                  string                  `json:"mood,omitempty"`
	EmotionCount          int                     `json:"emotion_count"`
	EmotionsIncluded      bool                    `json:"emotions_included"`
	Emotions              []memory.CurrentEmotion `json:"emotions,omitempty"`
	Life                  *LifeStatusView         `json:"life,omitempty"`
}

// LifeStatusView 是所有会话都能看到的共享生活状态
type LifeStatusView struct {
	Awake          bool   `json:"awake"`
	Activity       string `json:"activity,omitempty"`
	ActivitySince  string `json:"activity_since,omitempty"`
	NextRunAt      string `json:"next_run_at,omitempty"`
	NextCron       string `json:"next_cron,omitempty"`
	NextText       string `json:"next_text,omitempty"`
	NextPurpose    string `json:"next_purpose,omitempty"`
	ReminderAt     string `json:"reminder_at,omitempty"`
	ReminderText   string `json:"reminder_text,omitempty"`
	SleepStartedAt string `json:"sleep_started_at,omitempty"`
	WokeAt         string `json:"woke_at,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	LastErrorCode  string `json:"last_error_code,omitempty"`
}

type GetCurrentStatusInput struct {
	IncludeEmotions bool `json:"include_emotions,omitempty" jsonschema:"description=是否返回全部未平复感受正文，默认 false；数量和精力仍会返回"`
}

func NewGetCurrentStatusTool() (tool.InvokableTool, error) {
	return utils.InferTool("getCurrentStatus", "读取当前时间、会话、精力、交流意愿、情绪数量和生活状态；只有 include_emotions=true 才返回全部感受正文；life 另返回上海当天生活与工作请求量及软提醒状态，统计失败明确标记不可用。", getCurrentStatusFunc)
}

func getCurrentStatusFunc(ctx context.Context, input *GetCurrentStatusInput) (*GetCurrentStatusOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("状态读取能力未初始化"))
	}
	now := time.Now()
	output := &GetCurrentStatusOutput{
		Success: true, Time: now.Format("2006-01-02 15:04:05") + " " + weekdayText(now.Weekday()),
		ConversationKind: tc.ConversationKind, TargetID: tc.TargetID,
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
		output.Mood = fmt.Sprintf("精力：%s；交流意愿：%s", memory.AffectLevelText(mood.Energy), memory.AffectLevelText(mood.Sociability))
	}
	emotions, err := tc.MemoryMgr.CurrentEmotions(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取情感失败: %w", err)
	}
	output.EmotionCount = len(emotions)
	if input != nil && input.IncludeEmotions {
		output.EmotionsIncluded = true
		output.Emotions = emotions
	}
	if tc.ConversationKind == memory.ConversationKindLife {
		limit := tc.LifeDailyRequestLimit
		if limit == nil {
			value := config.Snapshot().Agent.LifeDailyRequestLimit
			limit = &value
		}
		usage, usageErr := modelstats.QueryDailyUsage(ctx, tc.MemoryMgr.GetDB(), now, *limit)
		output.DailyUsage = usage
		output.DailyUsageUnavailable = usageErr != nil
	}
	output.Life = loadLifeStatus(ctx, tc)
	return output, nil
}

// loadLifeStatus 读取唯一 life 的当前状态和最近一条启用日程
func loadLifeStatus(ctx context.Context, tc *ToolContext) *LifeStatusView {
	state, err := tc.MemoryMgr.GetLifeState(ctx)
	if err != nil || state == nil {
		return nil
	}
	view := &LifeStatusView{
		Awake: state.Awake, Activity: state.ActivityText,
		LastError: state.LastError, LastErrorCode: state.LastErrorCode,
	}
	if state.ActivitySince != nil {
		view.ActivitySince = schedule.Format(*state.ActivitySince)
	}
	if state.SleepStartedAt != nil {
		view.SleepStartedAt = schedule.Format(*state.SleepStartedAt)
	}
	if state.WokeAt != nil {
		view.WokeAt = schedule.Format(*state.WokeAt)
	}
	if rows, err := tc.MemoryMgr.ListSchedules(ctx, memory.ConversationKindLife, memory.LifeTargetID); err == nil {
		var fixed, reminder *memory.Schedule
		for i := range rows {
			row := &rows[i]
			if !row.Enabled {
				continue
			}
			if row.ScheduleKey == memory.LifeNextKey {
				if fixed == nil || row.NextRunAt.Before(fixed.NextRunAt) {
					fixed = row
				}
				continue
			}
			if reminder == nil || row.NextRunAt.Before(reminder.NextRunAt) {
				reminder = row
			}
		}
		if fixed != nil {
			view.NextRunAt = schedule.Format(fixed.NextRunAt)
			view.NextCron = fixed.CronExpr
			view.NextText = fixed.Text
			view.NextPurpose = fixed.Purpose(state.Awake)
		}
		if reminder != nil {
			view.ReminderAt = schedule.Format(reminder.NextRunAt)
			view.ReminderText = reminder.Text
		}
	}
	return view
}

func weekdayText(weekday time.Weekday) string {
	return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[weekday]
}
