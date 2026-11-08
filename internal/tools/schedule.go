package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/schedule"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// requireLifeContext 只有生活主线可以读写自己的生活状态
func requireLifeContext(ctx context.Context) (*ToolContext, *memory.Manager, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, nil, NewTerminalToolError(fmt.Errorf("生活能力未初始化"))
	}
	if tc.ConversationKind != memory.ConversationKindLife {
		return nil, nil, fmt.Errorf("只有生活会话可以安排自己的生活")
	}
	return tc, tc.MemoryMgr, nil
}

type PlanNextInput struct {
	At           string `json:"at,omitempty" jsonschema:"description=下一次回来看看的时间，RFC3339 或 2006-01-02 15:04:05；与 after_minutes 二选一"`
	AfterMinutes int    `json:"after_minutes,omitempty" jsonschema:"description=多少分钟后回来看看；与 at 二选一"`
	Activity     string `json:"activity" jsonschema:"description=接下来想做什么，一句话"`
	Note         string `json:"note,omitempty" jsonschema:"description=留给未来自己的话，说明为什么这时回来"`
}

type PlanNextOutput struct {
	Success    bool   `json:"success"`
	At         string `json:"at"`
	Activity   string `json:"activity"`
	ScheduleID uint   `json:"schedule_id"`
}

func planNext(ctx context.Context, input *PlanNextInput) (*PlanNextOutput, error) {
	_, mem, err := requireLifeContext(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil || strings.TrimSpace(input.Activity) == "" {
		return nil, fmt.Errorf("活动描述不能为空")
	}
	at, err := schedule.ResolveOnce(input.At, input.AfterMinutes)
	if err != nil {
		return nil, err
	}
	state, err := mem.GetLifeState(ctx)
	if err != nil {
		return nil, err
	}
	if !state.Awake {
		return nil, fmt.Errorf("现在在睡：改起床时间用 sleepUntil，其他提醒用 manageSchedule")
	}
	row, err := mem.PlanLife(ctx, input.Activity, at, input.Note)
	if err != nil {
		return nil, err
	}
	return &PlanNextOutput{Success: true, At: schedule.Format(at), Activity: input.Activity, ScheduleID: row.ID}, nil
}

type ManageScheduleInput struct {
	Action       string `json:"action" jsonschema:"description=list/create/update/cancel"`
	ScheduleID   uint   `json:"schedule_id,omitempty" jsonschema:"description=update 与 cancel 需要"`
	Text         string `json:"text,omitempty" jsonschema:"description=这条时间事项的说明，到点时给未来自己看"`
	At           string `json:"at,omitempty" jsonschema:"description=一次性时间，RFC3339 或 2006-01-02 15:04:05"`
	AfterMinutes int    `json:"after_minutes,omitempty" jsonschema:"description=多少分钟后，与 at 二选一"`
	Cron         string `json:"cron,omitempty" jsonschema:"description=五段重复规则：分 时 日 月 周；如每天 9 点=0 9 * * *，周一 9 点=0 9 * * 1。Asia/Shanghai，到期自动推进；与 at/after_minutes 二选一"`
	Enabled      *bool  `json:"enabled,omitempty" jsonschema:"description=update 时可选，false 表示停用"`
}

type ScheduleView struct {
	ScheduleID uint   `json:"schedule_id"`
	Target     string `json:"target"`
	Purpose    string `json:"purpose"`
	At         string `json:"at,omitempty"`
	Cron       string `json:"cron,omitempty"`
	NextRunAt  string `json:"next_run_at,omitempty"`
	Text       string `json:"text"`
	Enabled    bool   `json:"enabled"`
}

type ManageScheduleOutput struct {
	Success   bool           `json:"success"`
	Schedules []ScheduleView `json:"schedules,omitempty"`
	Schedule  *ScheduleView  `json:"schedule,omitempty"`
}

func scheduleView(row memory.Schedule, awake bool) ScheduleView {
	view := ScheduleView{
		ScheduleID: row.ID,
		Target:     fmt.Sprintf("%s:%d", row.TargetKind, row.TargetID),
		Purpose:    row.Purpose(awake),
		Cron:       row.CronExpr, Text: row.Text, Enabled: row.Enabled,
	}
	if row.RunAt != nil {
		view.At = schedule.Format(*row.RunAt)
	}
	view.NextRunAt = schedule.Format(row.NextRunAt)
	return view
}

func manageSchedule(ctx context.Context, input *ManageScheduleInput) (*ManageScheduleOutput, error) {
	_, mem, err := requireLifeContext(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, fmt.Errorf("需要 action")
	}
	state, err := mem.GetLifeState(ctx)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(input.Action)) {
	case "list":
		rows, err := mem.ListSchedules(ctx, memory.ConversationKindLife, memory.LifeTargetID)
		if err != nil {
			return nil, err
		}
		out := &ManageScheduleOutput{Success: true, Schedules: make([]ScheduleView, 0, len(rows))}
		for _, row := range rows {
			out.Schedules = append(out.Schedules, scheduleView(row, state.Awake))
		}
		return out, nil
	case "create":
		row, err := buildNewSchedule(input)
		if err != nil {
			return nil, err
		}
		if err := mem.CreateSchedule(ctx, row); err != nil {
			return nil, err
		}
		view := scheduleView(*row, state.Awake)
		return &ManageScheduleOutput{Success: true, Schedule: &view}, nil
	case "update":
		if input.ScheduleID == 0 {
			return nil, fmt.Errorf("update 需要 schedule_id")
		}
		if err := ensureLifeSchedule(ctx, mem, input.ScheduleID); err != nil {
			return nil, err
		}
		update, err := buildScheduleUpdate(input)
		if err != nil {
			return nil, err
		}
		row, err := mem.UpdateSchedule(ctx, input.ScheduleID, update)
		if err != nil {
			return nil, err
		}
		view := scheduleView(*row, state.Awake)
		return &ManageScheduleOutput{Success: true, Schedule: &view}, nil
	case "cancel":
		if input.ScheduleID == 0 {
			return nil, fmt.Errorf("cancel 需要 schedule_id")
		}
		if err := ensureLifeSchedule(ctx, mem, input.ScheduleID); err != nil {
			return nil, err
		}
		disabled := false
		row, err := mem.UpdateSchedule(ctx, input.ScheduleID, memory.ScheduleUpdate{Enabled: &disabled})
		if err != nil {
			return nil, err
		}
		view := scheduleView(*row, state.Awake)
		return &ManageScheduleOutput{Success: true, Schedule: &view}, nil
	default:
		return nil, fmt.Errorf("action 只能是 list、create、update 或 cancel")
	}
}

// buildNewSchedule 校验一条新日程的时间表达与正文
func buildNewSchedule(input *ManageScheduleInput) (*memory.Schedule, error) {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return nil, fmt.Errorf("日程说明不能为空")
	}
	row := &memory.Schedule{
		TargetKind: memory.ConversationKindLife, TargetID: memory.LifeTargetID, Text: text, Enabled: true,
	}
	cronExpr := strings.TrimSpace(input.Cron)
	switch {
	case cronExpr != "" && (strings.TrimSpace(input.At) != "" || input.AfterMinutes != 0):
		return nil, fmt.Errorf("重复规则与一次性时间只能二选一")
	case cronExpr != "":
		next, err := schedule.NextRun(cronExpr, time.Now())
		if err != nil {
			return nil, err
		}
		row.CronExpr, row.NextRunAt = cronExpr, next
	default:
		at, err := schedule.ResolveOnce(input.At, input.AfterMinutes)
		if err != nil {
			return nil, err
		}
		row.RunAt, row.NextRunAt = &at, at
	}
	return row, nil
}

// buildScheduleUpdate 只把实际给出的字段写进补丁；给时间就整组替换时间表达
func buildScheduleUpdate(input *ManageScheduleInput) (memory.ScheduleUpdate, error) {
	update := memory.ScheduleUpdate{}
	if text := strings.TrimSpace(input.Text); text != "" {
		update.Text = &text
	}
	if input.Enabled != nil {
		update.Enabled = input.Enabled
	}
	cronExpr := strings.TrimSpace(input.Cron)
	hasOnce := strings.TrimSpace(input.At) != "" || input.AfterMinutes != 0
	switch {
	case cronExpr != "" && hasOnce:
		return update, fmt.Errorf("重复规则与一次性时间只能二选一")
	case cronExpr != "":
		next, err := schedule.NextRun(cronExpr, time.Now())
		if err != nil {
			return update, err
		}
		update.SetTiming, update.CronExpr, update.NextRunAt = true, cronExpr, next
	case hasOnce:
		at, err := schedule.ResolveOnce(input.At, input.AfterMinutes)
		if err != nil {
			return update, err
		}
		update.SetTiming, update.RunAt, update.NextRunAt = true, &at, at
	}
	if update.Text == nil && update.Enabled == nil && !update.SetTiming {
		return update, fmt.Errorf("没有需要更新的内容")
	}
	return update, nil
}

// ensureLifeSchedule 防止生活主线改到别的目标的日程
func ensureLifeSchedule(ctx context.Context, mem *memory.Manager, id uint) error {
	row, err := mem.GetSchedule(ctx, id)
	if err != nil {
		return err
	}
	if row.TargetKind != memory.ConversationKindLife || row.TargetID != memory.LifeTargetID {
		return fmt.Errorf("只能修改生活自己的日程")
	}
	return nil
}

type SleepUntilInput struct {
	Until        string `json:"until,omitempty" jsonschema:"description=睡到什么时候，RFC3339 或 2006-01-02 15:04:05；与 after_minutes 二选一"`
	AfterMinutes int    `json:"after_minutes,omitempty" jsonschema:"description=睡多少分钟；与 until 二选一"`
	Note         string `json:"note,omitempty" jsonschema:"description=醒来时留给自己的话"`
}

type SleepUntilOutput struct {
	Success bool   `json:"success"`
	Until   string `json:"until"`
	Note    string `json:"note,omitempty"`
}

func sleepUntil(ctx context.Context, input *SleepUntilInput) (*SleepUntilOutput, error) {
	tc, mem, err := requireLifeContext(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, fmt.Errorf("需要 until 或 after_minutes")
	}
	until, err := schedule.ResolveOnce(input.Until, input.AfterMinutes)
	if err != nil {
		return nil, err
	}
	if err := mem.SleepLife(ctx, until, input.Note); err != nil {
		return nil, err
	}
	if tc.SleepChangedCallback != nil {
		tc.SleepChangedCallback(ctx, false)
	}
	return &SleepUntilOutput{Success: true, Until: schedule.Format(until), Note: strings.TrimSpace(input.Note)}, nil
}

type WakeUpInput struct{}

type WakeUpOutput struct {
	Success      bool   `json:"success"`
	WokeAt       string `json:"woke_at"`
	SleepSummary string `json:"sleep_summary,omitempty"`
}

func wakeUp(ctx context.Context, _ *WakeUpInput) (*WakeUpOutput, error) {
	tc, mem, err := requireLifeContext(ctx)
	if err != nil {
		return nil, err
	}
	state, err := mem.GetLifeState(ctx)
	if err != nil {
		return nil, err
	}
	if state.Awake {
		return nil, fmt.Errorf("现在是醒着的；改下一次安排用 planNext")
	}
	now := time.Now()
	summary := ""
	if state.SleepStartedAt != nil {
		selfID := int64(0)
		if tc.Bot != nil {
			selfID = tc.Bot.GetSelfID()
		}
		if items, total, err := mem.SummarizeSleepMessages(ctx, *state.SleepStartedAt, now, selfID); err == nil {
			summary = memory.FormatSleepSummary(items, total, *state.SleepStartedAt, now)
		}
	}
	if err := mem.WakeLife(ctx, now); err != nil {
		return nil, err
	}
	if tc.SleepChangedCallback != nil {
		tc.SleepChangedCallback(ctx, true)
	}
	return &WakeUpOutput{Success: true, WokeAt: schedule.Format(now), SleepSummary: summary}, nil
}

func NewPlanNextTool() (tool.InvokableTool, error) {
	return utils.InferTool("planNext", "更新当前活动，并设置或替换下一次回来看的单次时间；只管下一次，不会自动重复。固定重复事项用 manageSchedule 的 cron。", planNext)
}

func NewManageScheduleTool() (tool.InvokableTool, error) {
	return utils.InferTool("manageSchedule", "管理独立约定或重复事项：一次性用 at/after_minutes，每天或每周等固定事项用 cron，一次建立后自动推进，不必每回再建。列出、创建、修改或取消日程。purpose 为 next 的是下一次查看（planNext 的固定槽位），wake 是睡眠中的起床安排，commitment 是独立事项；允许统一修改固定槽位，修改或取消后用途会随醒睡状态重新派生。", manageSchedule)
}

func NewSleepUntilTool() (tool.InvokableTool, error) {
	return utils.InferTool("sleepUntil", "准备睡眠并保存起床时间；到点由程序恢复醒着的状态并提醒你。", sleepUntil)
}

func NewWakeUpTool() (tool.InvokableTool, error) {
	return utils.InferTool("wakeUp", "提前结束睡眠，停用起床安排并记录真实醒来时间。", wakeUp)
}
