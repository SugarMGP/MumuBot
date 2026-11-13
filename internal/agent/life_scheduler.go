package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/schedule"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

const (
	lifeClockInterval = 15 * time.Second
	lifeClockBatch    = 20
)

// errScheduleRule 表示日程的时间规则无法计算下一次时点
var errScheduleRule = errors.New("日程时间规则无效")

// lifeClockLoop 只负责等时间和投递，不等待任何模型回合完成
func (a *Agent) lifeClockLoop() {
	defer a.wg.Done()
	timer := time.NewTimer(lifeClockInterval)
	defer timer.Stop()
	a.runLifeClock()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-timer.C:
			a.runLifeClock()
			timer.Reset(lifeClockInterval)
		}
	}
}

// runLifeClock 处理一批到期日程；投递成功即返回，不阻塞其他到期项
func (a *Agent) runLifeClock() {
	now := time.Now()
	a.deliverLifeRetry(now)
	if err := a.memory.CleanupExpiredSchedules(a.ctx, now); err != nil {
		zap.L().Warn("清理过期日程失败", zap.Error(err))
	}
	rows, err := a.memory.DueSchedules(a.ctx, now, lifeClockBatch)
	if err != nil {
		zap.L().Warn("读取到期日程失败", zap.Error(err))
		return
	}
	for _, row := range rows {
		if a.stopping.Load() || a.ctx.Err() != nil {
			return
		}
		a.deliverSchedule(row, now)
	}
}

func (a *Agent) deliverLifeRetry(now time.Time) {
	const text = "上一轮运行失败，现在按五分钟间隔进行有限补试。接续尚未完成的事，不重放已完成的工具；仍然失败时保留真实原因。"
	var message *schema.AgenticMessage
	build := func(row *memory.SessionMessage) (json.RawMessage, error) {
		message = schema.UserAgenticMessage(fmt.Sprintf("[系统消息：生活补试]\n内部消息编号：%d\n投递时间：%s\n%s", row.ID, now.Format("2006-01-02 15:04:05"), text))
		message.Extra = map[string]any{"kind": "agent_message", "source": "life_retry", "wake": true}
		return sonic.Marshal(message)
	}
	row, entry, err := a.memory.DeliverLifeRetry(a.ctx, now, text, build)
	if err != nil {
		zap.L().Warn("投递 life 补试失败", zap.Error(err))
		return
	}
	if entry != nil {
		a.sessions.get(memory.ConversationKindLife, memory.LifeTargetID).AppendAgentMessage(message, entry.ID, row.CreatedAt, true, false)
		a.scheduleLifeThink()
	}
}

// deliverSchedule 在事务内推进日程并投递内部信，提交成功后才写内存并请求调度
func (a *Agent) deliverSchedule(row memory.Schedule, now time.Time) {
	wakeLife := row.ScheduleKey == memory.LifeNextKey
	extraSummary := ""
	if row.TargetKind == memory.ConversationKindLife {
		if state, err := a.memory.GetLifeState(a.ctx); err == nil && state != nil && wakeLife && !state.Awake && state.SleepStartedAt != nil {
			extraSummary = a.sleepSummaryText(a.ctx, *state.SleepStartedAt, now)
		} else {
			extraSummary = a.lifeActivitySummaryText(a.ctx, row.UpdatedAt, now)
		}
	}
	var message *schema.AgenticMessage
	build := func(msg *memory.SessionMessage) (json.RawMessage, error) {
		body := scheduleMessageBody(row, now)
		body += fmt.Sprintf("\n内部消息编号：%d", msg.ID)
		if extraSummary != "" {
			body += "\n\n" + extraSummary
		}
		message = schema.UserAgenticMessage(body)
		message.Extra = map[string]any{
			"source": "schedule", "kind": "schedule", "wake": true, "allow_asleep": true,
			"internal_id": msg.ID, "schedule_id": row.ID,
			"scheduled_at": row.NextRunAt, "delivered_at": now,
		}
		return sonic.Marshal(message)
	}
	delivery, err := a.memory.DeliverDueSchedule(a.ctx, row.ID, now, row.Text, build, a.advanceSchedule, wakeLife)
	if err != nil {
		switch {
		case errors.Is(err, memory.ErrScheduleNotDue):
			return
		case errors.Is(err, memory.ErrConversationUnavailable):
			zap.L().Warn("日程目标不可用，已停用该日程", zap.Uint("schedule_id", row.ID), zap.String("target", fmt.Sprintf("%s:%d", row.TargetKind, row.TargetID)))
			a.disableSchedule(row.ID)
		case errors.Is(err, errScheduleRule):
			zap.L().Warn("日程时间规则无效，已停用该日程", zap.Uint("schedule_id", row.ID), zap.Error(err))
			a.disableSchedule(row.ID)
		default:
			zap.L().Warn("投递到期日程失败", zap.Uint("schedule_id", row.ID), zap.Error(err))
		}
		return
	}
	if delivery.WokeLife {
		a.lifeAsleep.Store(false)
	}
	if message != nil && delivery.Entry != nil && delivery.Message != nil {
		a.sessions.get(row.TargetKind, row.TargetID).AppendAgentMessage(message, delivery.Entry.ID, delivery.Message.CreatedAt, true, true)
	}
	if delivery.Message != nil {
		a.scheduleSessionThink(row.TargetKind, row.TargetID, delivery.Message.CreatedAt)
	}
}

// advanceSchedule 计算下一次时点：一次性日程投递后结束，重复日程按 cron 推进
func (a *Agent) advanceSchedule(row *memory.Schedule, now time.Time) (time.Time, bool, error) {
	if row.RunAt != nil {
		return row.NextRunAt, false, nil
	}
	next, err := schedule.NextRun(row.CronExpr, now)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("%w: %v", errScheduleRule, err)
	}
	return next, true, nil
}

// disableSchedule 停用无法继续推进的日程
func (a *Agent) disableSchedule(id uint) {
	disabled := false
	if _, err := a.memory.UpdateSchedule(a.ctx, id, memory.ScheduleUpdate{Enabled: &disabled}); err != nil {
		zap.L().Warn("停用日程失败", zap.Uint("schedule_id", id), zap.Error(err))
	}
}

// scheduleMessageBody 说明计划时间、实际投递时间和原始说明，明确不是真人发言
func scheduleMessageBody(row memory.Schedule, now time.Time) string {
	lines := []string{
		"【定时提醒｜不是当前用户发言】",
		fmt.Sprintf("计划时间：%s", schedule.Format(row.NextRunAt)),
		fmt.Sprintf("实际投递：%s", schedule.Format(now)),
	}
	if row.ScheduleKey == memory.LifeNextKey {
		lines = append(lines, "这是你留给自己的下一次查看；如果它同时是起床时间，程序已经恢复醒着的状态。")
	}
	if strings.TrimSpace(row.Text) == "" {
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "", strings.TrimSpace(row.Text))
	return strings.Join(lines, "\n")
}

func (a *Agent) sleepSummaryText(ctx context.Context, from, to time.Time) string {
	items, total, err := a.memory.SummarizeSleepMessages(ctx, from, to, a.bot.GetSelfID())
	if err != nil {
		zap.L().Warn("聚合睡眠期间消息失败", zap.Error(err))
		return ""
	}
	return memory.FormatSleepSummary(items, total, from, to)
}

func (a *Agent) lifeActivitySummaryText(ctx context.Context, from, to time.Time) string {
	summary, err := a.memory.SummarizeLifeActivitySince(ctx, from, to, a.bot.GetSelfID())
	if err != nil {
		zap.L().Warn("聚合生活期间实际活动失败", zap.Error(err))
		return ""
	}
	return summary
}
