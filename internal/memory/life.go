package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// LifeTargetID 是唯一 life 会话的固定目标编号
	LifeTargetID = 1
	// LifeName 是 life 会话在目录和投信里显示的名称
	LifeName = "life"
	// LifeNextKey 是“下一次看看”的固定日程槽位
	LifeNextKey = "life_next"
)

// LifeState 是唯一 life 会话的持久状态，使用固定单例行
type LifeState struct {
	ID               uint            `gorm:"column:id;primaryKey" json:"id"`
	Awake            bool            `gorm:"column:awake;not null;default:true" json:"awake"`
	ActivityText     string          `gorm:"column:activity_text;type:text;not null;default:''" json:"activity_text"`
	ActivitySince    *time.Time      `gorm:"column:activity_since" json:"activity_since,omitempty"`
	SleepStartedAt   *time.Time      `gorm:"column:sleep_started_at" json:"sleep_started_at,omitempty"`
	WokeAt           *time.Time      `gorm:"column:woke_at" json:"woke_at,omitempty"`
	ContextSummary   json.RawMessage `gorm:"column:context_summary;type:jsonb;not null;default:'[]'" json:"-"`
	ContextThroughID uint            `gorm:"column:context_through_id;not null;default:0" json:"context_through_id"`
	LastErrorCode    string          `gorm:"column:last_error_code;type:text;not null;default:''" json:"last_error_code"`
	LastError        string          `gorm:"column:last_error;type:text;not null;default:''" json:"last_error"`
	RetryCount       int             `gorm:"column:retry_count;not null;default:0" json:"retry_count"`
	RetryAt          *time.Time      `gorm:"column:retry_at" json:"retry_at,omitempty"`
	RetryExhausted   bool            `gorm:"column:retry_exhausted;not null;default:false" json:"retry_exhausted"`
	UpdatedAt        time.Time       `gorm:"column:updated_at;not null;default:now()" json:"updated_at"`
}

func (LifeState) TableName() string { return "life_state" }

// EnsureLife 确保 life:1 目标与状态行存在；不覆盖已有的启用状态和名称
func (m *Manager) EnsureLife(ctx context.Context) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ConversationTarget{
			ConversationKind: ConversationKindLife, TargetID: LifeTargetID,
			Name: LifeName, Active: true, UpdatedAt: time.Now(),
		}).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&LifeState{
			ID: LifeTargetID, Awake: true,
		}).Error
	})
}

// GetLifeState 读取唯一 life 状态
func (m *Manager) GetLifeState(ctx context.Context) (*LifeState, error) {
	var row LifeState
	if err := m.db.WithContext(ctx).Where("id=?", LifeTargetID).Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// SetLifeError 记录可本地化的错误码和原始细节，空 code 表示清除
func (m *Manager) SetLifeError(ctx context.Context, code, detail string) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state LifeState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", LifeTargetID).Take(&state).Error; err != nil {
			return err
		}
		state.LastErrorCode, state.LastError = code, detail
		state.RetryAt = nil
		state.RetryExhausted = code != "" && state.RetryCount >= 2
		if code == "" {
			state.RetryCount = 0
		} else if state.Awake && state.RetryCount < 2 {
			state.RetryCount++
			next := time.Now().Add(5 * time.Minute)
			state.RetryAt = &next
		}
		state.UpdatedAt = time.Now()
		return tx.Save(&state).Error
	})
}

// ResetLifeRetryTx 在新的明确输入或主动睡眠时结算上一轮失败
func ResetLifeRetryTx(tx *gorm.DB) error {
	return tx.Model(&LifeState{}).Where("id=?", LifeTargetID).Updates(map[string]any{
		"retry_count": 0, "retry_at": nil, "retry_exhausted": false, "last_error_code": "", "last_error": "",
	}).Error
}

// DeliverLifeRetry 在同一事务中消费重试时点并留下可恢复的内部输入
func (m *Manager) DeliverLifeRetry(ctx context.Context, now time.Time, text string, build func(*SessionMessage) (json.RawMessage, error)) (*SessionMessage, *TranscriptEntry, error) {
	var message *SessionMessage
	var entry *TranscriptEntry
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state LifeState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", LifeTargetID).Take(&state).Error; err != nil {
			return err
		}
		if !state.Awake || state.RetryAt == nil || state.RetryAt.After(now) {
			return nil
		}
		var err error
		message, entry, err = m.enqueueSessionMessageTx(tx, ConversationKindLife, LifeTargetID, ConversationKindLife, LifeTargetID, text, build, nil)
		if err != nil {
			return err
		}
		return tx.Model(&LifeState{}).Where("id=?", LifeTargetID).Update("retry_at", nil).Error
	})
	return message, entry, err
}

// PlanLife 在同一次事务里更新当前活动并替换 life_next 槽位
func (m *Manager) PlanLife(ctx context.Context, activity string, at time.Time, note string) (*Schedule, error) {
	activity = strings.TrimSpace(activity)
	if activity == "" {
		return nil, fmt.Errorf("活动描述不能为空")
	}
	text := strings.TrimSpace(note)
	if text == "" {
		text = activity
	}
	now := time.Now()
	var row *Schedule
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&LifeState{}).Where("id=?", LifeTargetID).Updates(map[string]any{
			"activity_text": activity, "activity_since": now, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		created, err := upsertLifeNext(tx, at, text)
		if err != nil {
			return err
		}
		row = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// SleepLife 保存睡眠状态和起床安排；已睡时只调整起床时间，不重写开始时间
func (m *Manager) SleepLife(ctx context.Context, until time.Time, note string) error {
	text := strings.TrimSpace(note)
	if text == "" {
		text = "睡到这个时候，醒来看看接下来想做什么"
	}
	now := time.Now()
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state LifeState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", LifeTargetID).Take(&state).Error; err != nil {
			return err
		}
		updates := map[string]any{"awake": false, "woke_at": nil, "activity_text": "", "activity_since": nil, "updated_at": now}
		if err := ResetLifeRetryTx(tx); err != nil {
			return err
		}
		if state.Awake || state.SleepStartedAt == nil {
			updates["sleep_started_at"] = now
		}
		if err := tx.Model(&LifeState{}).Where("id=?", LifeTargetID).Updates(updates).Error; err != nil {
			return err
		}
		_, err := upsertLifeNext(tx, until, text)
		return err
	})
}

// WakeLife 提前结束睡眠并停用起床槽位，真实起床时间单独记录
func (m *Manager) WakeLife(ctx context.Context, at time.Time) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if at.IsZero() {
			at = time.Now()
		}
		if err := tx.Model(&LifeState{}).Where("id=?", LifeTargetID).Updates(map[string]any{
			"awake": true, "woke_at": at, "updated_at": time.Now(),
		}).Error; err != nil {
			return err
		}
		return tx.Model(&Schedule{}).
			Where("target_kind=? AND target_id=? AND schedule_key=? AND enabled=true", ConversationKindLife, LifeTargetID, LifeNextKey).
			Updates(map[string]any{"enabled": false, "updated_at": time.Now()}).Error
	})
}

// HeadEntry 是一次换代要写入的代头行
type HeadEntry struct {
	Kind    string
	Payload json.RawMessage
}

// CommitLifeCheckpoint 在同一事务里给真实输入分配可见序号、保存摘要边界、删除旧代头并写入新代头
func (m *Manager) CommitLifeCheckpoint(ctx context.Context, summary json.RawMessage, throughID uint, visibleIDs []uint, heads []HeadEntry) (map[uint]uint64, []uint, TranscriptCandidates, error) {
	seqs := map[uint]uint64{}
	headIDs := make([]uint, 0, len(heads))
	released := TranscriptCandidates{}
	if len(summary) == 0 {
		summary = json.RawMessage(`[]`)
	}
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		seqs, err = markVisibleTx(tx, ConversationKindLife, int64(LifeTargetID), visibleIDs)
		if err != nil {
			return err
		}
		if err := tx.Model(&LifeState{}).Where("id=?", LifeTargetID).Updates(map[string]any{
			"context_summary":    summary,
			"context_through_id": gorm.Expr("GREATEST(context_through_id, ?)", throughID),
			"updated_at":         time.Now(),
		}).Error; err != nil {
			return err
		}
		if throughID > 0 {
			condition := "conversation_kind=? AND target_id=? AND id<=? AND entry_kind IN ('system_prompt','group_note','context_digest','pinned_memory')"
			args := []any{ConversationKindLife, int64(LifeTargetID), throughID}
			candidates, err := transcriptCandidates(tx, condition, args...)
			if err != nil {
				return err
			}
			released.ToolRefs = append(released.ToolRefs, candidates.ToolRefs...)
			released.SnapshotRefs = append(released.SnapshotRefs, candidates.SnapshotRefs...)
			if err := tx.Where(condition, args...).Delete(&TranscriptEntry{}).Error; err != nil {
				return err
			}
		}
		for _, head := range heads {
			entry := &TranscriptEntry{
				ConversationKind: ConversationKindLife, TargetID: LifeTargetID,
				EntryKind: head.Kind, Payload: head.Payload,
			}
			if err := tx.Create(entry).Error; err != nil {
				return err
			}
			headIDs = append(headIDs, entry.ID)
		}
		return nil
	})
	return seqs, headIDs, released, err
}

// WakeLifeFromSchedule 在日程投递事务里机械恢复 awake，返回是否真的从睡眠中醒来
func wakeLifeFromSchedule(tx *gorm.DB, at time.Time) (bool, error) {
	result := tx.Model(&LifeState{}).Where("id=? AND awake=false", LifeTargetID).Updates(map[string]any{
		"awake": true, "woke_at": at, "updated_at": at,
	})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// LifeHasFutureSchedule 判断 life 是否还有任何启用的日程
func (m *Manager) LifeHasFutureSchedule(ctx context.Context) (bool, error) {
	var count int64
	err := m.db.WithContext(ctx).Model(&Schedule{}).
		Where("target_kind=? AND target_id=? AND enabled=true", ConversationKindLife, LifeTargetID).
		Count(&count).Error
	return count > 0, err
}

// CountTranscript 返回会话的工作集行数，用于判断 life 是否第一次启动
func (m *Manager) CountTranscript(ctx context.Context, kind string, targetID int64) (int64, error) {
	var count int64
	err := m.db.WithContext(ctx).Model(&TranscriptEntry{}).
		Where("conversation_kind=? AND target_id=?", kind, targetID).Count(&count).Error
	return count, err
}

// upsertLifeNext 更新固定 life_next 槽位；同一事务内复用，不做跨槽位合并
func upsertLifeNext(tx *gorm.DB, at time.Time, text string) (*Schedule, error) {
	text = strings.TrimSpace(text)
	if err := validateScheduleText(text); err != nil {
		return nil, err
	}
	now := time.Now()
	var row Schedule
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("target_kind=? AND target_id=? AND schedule_key=?", ConversationKindLife, int64(LifeTargetID), LifeNextKey).
		Take(&row).Error
	switch {
	case err == gorm.ErrRecordNotFound:
		row = Schedule{
			TargetKind: ConversationKindLife, TargetID: LifeTargetID, ScheduleKey: LifeNextKey,
			RunAt: &at, NextRunAt: at, Text: text, Enabled: true,
		}
		if err := tx.Create(&row).Error; err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if err := tx.Model(&Schedule{}).Where("id=?", row.ID).Updates(map[string]any{
			"run_at": at, "cron_expr": "", "next_run_at": at, "text": text, "enabled": true, "updated_at": now,
		}).Error; err != nil {
			return nil, err
		}
		row.RunAt, row.CronExpr, row.NextRunAt, row.Text, row.Enabled = &at, "", at, text, true
		row.UpdatedAt = now
	}
	return &row, nil
}
