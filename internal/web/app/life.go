package app

import (
	"net/http"
	"strings"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelstats"
	"mumu-bot/internal/schedule"

	"github.com/go-chi/chi/v5"
)

const (
	lifeScheduleTimeInvalid = "life_schedule_time_invalid"
	lifeScheduleTextInvalid = "life_schedule_text_invalid"
)

type lifeStateDTO struct {
	Awake          bool       `json:"awake"`
	Activity       string     `json:"activity"`
	ActivitySince  *time.Time `json:"activity_since,omitempty"`
	SleepStartedAt *time.Time `json:"sleep_started_at,omitempty"`
	WokeAt         *time.Time `json:"woke_at,omitempty"`
	LastError      string     `json:"last_error"`
	LastErrorCode  string     `json:"last_error_code"`
	Running        bool       `json:"running"`
	UpdatedAt      time.Time  `json:"updated_at"`
	RetryCount     int        `json:"retry_count"`
	RetryAt        *time.Time `json:"retry_at,omitempty"`
	RetryExhausted bool       `json:"retry_exhausted"`
}

type lifeScheduleDTO struct {
	ID        uint       `json:"id"`
	At        *time.Time `json:"at,omitempty"`
	Cron      string     `json:"cron,omitempty"`
	NextRunAt time.Time  `json:"next_run_at"`
	Text      string     `json:"text"`
	Enabled   bool       `json:"enabled"`
	Purpose   string     `json:"purpose"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type lifeResponse struct {
	DailyUsage            *modelstats.DailyUsage `json:"daily_usage"`
	DailyUsageUnavailable bool                   `json:"daily_usage_unavailable"`
	State                 lifeStateDTO           `json:"state"`
	Schedules             []lifeScheduleDTO      `json:"schedules"`
	Workers               []lifeWorkerDTO        `json:"workers"`
}

type lifeWorkerDTO struct {
	ID        uint      `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Active    bool      `json:"active"`
	LastError string    `json:"last_error"`
	UpdatedAt time.Time `json:"updated_at"`
}

// loadLifeData 读取唯一 life 的当前状态和全部日程
func (a *App) loadLifeData(r *http.Request) (lifeResponse, error) {
	ctx := r.Context()
	state, err := a.memMgr.GetLifeState(ctx)
	if err != nil {
		return lifeResponse{}, err
	}
	rows, err := a.memMgr.ListSchedules(ctx, memory.ConversationKindLife, memory.LifeTargetID)
	if err != nil {
		return lifeResponse{}, err
	}
	workers, err := a.memMgr.ListWorkerSessions(ctx)
	if err != nil {
		return lifeResponse{}, err
	}
	targets, err := a.memMgr.ListConversationTargets(ctx, memory.ConversationKindWorker, false)
	if err != nil {
		return lifeResponse{}, err
	}
	active := map[int64]bool{}
	for _, target := range targets {
		active[target.TargetID] = target.Active
	}
	running := false
	if agent := a.currentAgent(); agent != nil {
		running = agent.Running(memory.ConversationKindLife, memory.LifeTargetID)
	}
	data := lifeResponse{
		State: lifeStateDTO{
			Awake: state.Awake, Activity: state.ActivityText,
			ActivitySince: state.ActivitySince, SleepStartedAt: state.SleepStartedAt,
			WokeAt: state.WokeAt, LastError: state.LastError, LastErrorCode: state.LastErrorCode,
			UpdatedAt: state.UpdatedAt, Running: running,
			RetryCount: state.RetryCount, RetryAt: state.RetryAt, RetryExhausted: state.RetryExhausted,
		},
		Schedules: make([]lifeScheduleDTO, 0, len(rows)),
		Workers:   make([]lifeWorkerDTO, 0, len(workers)),
	}
	for _, worker := range workers {
		data.Workers = append(data.Workers, lifeWorkerDTO{
			ID: worker.ID, Title: worker.Title, Status: worker.Status,
			Active: active[int64(worker.ID)], LastError: worker.LastError, UpdatedAt: worker.UpdatedAt,
		})
	}
	for _, row := range rows {
		data.Schedules = append(data.Schedules, lifeScheduleDTO{
			ID: row.ID, At: row.RunAt, Cron: row.CronExpr, NextRunAt: row.NextRunAt,
			Text: row.Text, Enabled: row.Enabled, Purpose: row.Purpose(state.Awake),
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
	}
	data.DailyUsage, err = modelstats.QueryDailyUsage(ctx, a.memMgr.GetDB(), time.Now(), config.Snapshot().Agent.LifeDailyRequestLimit)
	data.DailyUsageUnavailable = err != nil
	return data, nil
}

func (a *App) handleLife(w http.ResponseWriter, r *http.Request) {
	data, err := a.loadLifeData(r)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, data, nil)
}

type lifeScheduleInput struct {
	Text         string `json:"text"`
	At           string `json:"at,omitempty"`
	AfterMinutes int    `json:"after_minutes,omitempty"`
	Cron         string `json:"cron,omitempty"`
	Enabled      *bool  `json:"enabled,omitempty"`
}

func (a *App) handleLifeScheduleCreate(w http.ResponseWriter, r *http.Request) {
	var body lifeScheduleInput
	if err := decodeJSON(r, &body); err != nil {
		a.failLifeSchedule(w, "validation_failed")
		return
	}
	text := strings.TrimSpace(body.Text)
	row := &memory.Schedule{
		TargetKind: memory.ConversationKindLife, TargetID: memory.LifeTargetID,
		Text: text, Enabled: true,
	}
	if body.Enabled != nil {
		row.Enabled = *body.Enabled
	}
	cronExpr := strings.TrimSpace(body.Cron)
	hasOnce := strings.TrimSpace(body.At) != "" || body.AfterMinutes != 0
	switch {
	case cronExpr != "" && hasOnce:
		a.failLifeSchedule(w, lifeScheduleTimeInvalid)
		return
	case cronExpr != "":
		next, err := schedule.NextRun(cronExpr, time.Now())
		if err != nil {
			a.failLifeSchedule(w, lifeScheduleTimeInvalid)
			return
		}
		row.CronExpr, row.NextRunAt = cronExpr, next
	default:
		at, err := schedule.ResolveOnce(body.At, body.AfterMinutes)
		if err != nil {
			a.failLifeSchedule(w, lifeScheduleTimeInvalid)
			return
		}
		row.RunAt, row.NextRunAt = &at, at
	}
	if err := a.memMgr.CreateSchedule(r.Context(), row); err != nil {
		a.failFromError(w, r, err)
		return
	}
	a.respondLifeData(w, r)
}

type lifeScheduleUpdateInput struct {
	Text         *string `json:"text,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
	At           *string `json:"at,omitempty"`
	AfterMinutes *int    `json:"after_minutes,omitempty"`
	Cron         *string `json:"cron,omitempty"`
}

func (a *App) handleLifeScheduleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil || id == 0 {
		a.failLifeSchedule(w, "validation_failed")
		return
	}
	row, err := a.memMgr.GetSchedule(r.Context(), id)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	if row.TargetKind != memory.ConversationKindLife || row.TargetID != memory.LifeTargetID {
		a.failLifeSchedule(w, "validation_failed")
		return
	}
	var body lifeScheduleUpdateInput
	if err := decodeJSON(r, &body); err != nil {
		a.failLifeSchedule(w, "validation_failed")
		return
	}
	update := memory.ScheduleUpdate{}
	if body.Text != nil {
		text := strings.TrimSpace(*body.Text)
		update.Text = &text
	}
	if body.Enabled != nil {
		update.Enabled = body.Enabled
	}
	cronExpr := ""
	if body.Cron != nil {
		cronExpr = strings.TrimSpace(*body.Cron)
	}
	hasOnce := body.At != nil || body.AfterMinutes != nil
	switch {
	case cronExpr != "" && hasOnce:
		a.failLifeSchedule(w, lifeScheduleTimeInvalid)
		return
	case cronExpr != "":
		next, err := schedule.NextRun(cronExpr, time.Now())
		if err != nil {
			a.failLifeSchedule(w, lifeScheduleTimeInvalid)
			return
		}
		update.SetTiming, update.CronExpr, update.NextRunAt = true, cronExpr, next
	case hasOnce:
		rawAt := ""
		if body.At != nil {
			rawAt = *body.At
		}
		after := 0
		if body.AfterMinutes != nil {
			after = *body.AfterMinutes
		}
		at, err := schedule.ResolveOnce(rawAt, after)
		if err != nil {
			a.failLifeSchedule(w, lifeScheduleTimeInvalid)
			return
		}
		update.SetTiming, update.RunAt, update.NextRunAt = true, &at, at
	}
	if update.Text == nil && update.Enabled == nil && !update.SetTiming {
		a.failLifeSchedule(w, "validation_failed")
		return
	}
	if _, err := a.memMgr.UpdateSchedule(r.Context(), id, update); err != nil {
		a.failFromError(w, r, err)
		return
	}
	a.respondLifeData(w, r)
}

func (a *App) handleLifeWake(w http.ResponseWriter, r *http.Request) {
	agent := a.currentAgent()
	if agent == nil {
		a.failLifeSchedule(w, "agent_not_ready")
		return
	}
	if err := agent.WakeLifeNow(r.Context()); err != nil {
		a.failFromError(w, r, err)
		return
	}
	a.respondLifeData(w, r)
}

func (a *App) handleLifeRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Note string `json:"note,omitempty"`
	}
	if r.Body != nil {
		_ = decodeJSON(r, &body)
	}
	agent := a.currentAgent()
	if agent == nil {
		a.failLifeSchedule(w, "agent_not_ready")
		return
	}
	if err := agent.RequestLifeRun(r.Context(), body.Note); err != nil {
		a.failFromError(w, r, err)
		return
	}
	a.respondLifeData(w, r)
}

// respondLifeData 写操作成功后返回最新生活状态，前端无需再单独刷新
func (a *App) respondLifeData(w http.ResponseWriter, r *http.Request) {
	data, err := a.loadLifeData(r)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, data, nil)
}

// failLifeSchedule 只返回稳定错误码，具体文案由前端目录提供
func (a *App) failLifeSchedule(w http.ResponseWriter, code string) {
	respondError(w, http.StatusBadRequest, code, "", nil)
}
