package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrScheduleNotDue 表示日程还没到期或已经被处理过
var ErrScheduleNotDue = errors.New("日程未到期或已处理")

// Schedule 是通用的一性次或重复时间输入；next_run_at 是程序判断到期的确定事实
type Schedule struct {
	ID          uint       `gorm:"column:id;primaryKey" json:"id"`
	TargetKind  string     `gorm:"column:target_kind;type:text;not null" json:"target_kind"`
	TargetID    int64      `gorm:"column:target_id;not null" json:"target_id"`
	ScheduleKey string     `gorm:"column:schedule_key;type:text;not null;default:''" json:"schedule_key,omitempty"`
	RunAt       *time.Time `gorm:"column:run_at" json:"run_at,omitempty"`
	CronExpr    string     `gorm:"column:cron_expr;type:text;not null;default:''" json:"cron_expr,omitempty"`
	NextRunAt   time.Time  `gorm:"column:next_run_at;not null" json:"next_run_at"`
	Text        string     `gorm:"column:text;type:text;not null" json:"text"`
	Enabled     bool       `gorm:"column:enabled;not null" json:"enabled"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null;default:now()" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;not null;default:now()" json:"updated_at"`
}

func (Schedule) TableName() string { return "schedules" }

// ScheduleUpdate 是日程修改的最小补丁；SetTiming 表示整组替换时间表达
type ScheduleUpdate struct {
	Text      *string
	Enabled   *bool
	SetTiming bool
	RunAt     *time.Time
	CronExpr  string
	NextRunAt time.Time
}

// CreateSchedule 保存一条新日程
func (m *Manager) CreateSchedule(ctx context.Context, row *Schedule) error {
	if err := normalizeSchedule(row); err != nil {
		return err
	}
	return m.db.WithContext(ctx).Create(row).Error
}

// ListSchedules 返回目标会话的全部日程，含已停用的历史记录
func (m *Manager) ListSchedules(ctx context.Context, kind string, targetID int64) ([]Schedule, error) {
	var rows []Schedule
	err := m.db.WithContext(ctx).Where("target_kind=? AND target_id=?", kind, targetID).
		Order("enabled DESC, next_run_at, id").Find(&rows).Error
	return rows, err
}

// GetSchedule 按编号读取日程
func (m *Manager) GetSchedule(ctx context.Context, id uint) (*Schedule, error) {
	var row Schedule
	if err := m.db.WithContext(ctx).Where("id=?", id).Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// UpdateSchedule 在事务内修改日程文本、启停或整组时间表达
func (m *Manager) UpdateSchedule(ctx context.Context, id uint, update ScheduleUpdate) (*Schedule, error) {
	var row Schedule
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&row).Error; err != nil {
			return err
		}
		if update.Text != nil {
			text := strings.TrimSpace(*update.Text)
			if text == "" {
				return fmt.Errorf("日程说明不能为空")
			}
			row.Text = text
		}
		if update.Enabled != nil {
			row.Enabled = *update.Enabled
		}
		if update.SetTiming {
			row.RunAt = update.RunAt
			row.CronExpr = strings.TrimSpace(update.CronExpr)
			row.NextRunAt = update.NextRunAt
		}
		if err := normalizeSchedule(&row); err != nil {
			return err
		}
		return tx.Model(&Schedule{}).Where("id=?", row.ID).Updates(map[string]any{
			"run_at":      row.RunAt,
			"cron_expr":   row.CronExpr,
			"next_run_at": row.NextRunAt,
			"text":        row.Text,
			"enabled":     row.Enabled,
			"updated_at":  time.Now(),
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// DueSchedules 读取已到期且启用的日程
func (m *Manager) DueSchedules(ctx context.Context, now time.Time, limit int) ([]Schedule, error) {
	if limit <= 0 {
		return nil, nil
	}
	var rows []Schedule
	err := m.db.WithContext(ctx).Where("enabled=true AND next_run_at<=?", now).
		Order("next_run_at, id").Limit(limit).Find(&rows).Error
	return rows, err
}

// ScheduleDelivery 是一次到期投递的持久结果
type ScheduleDelivery struct {
	Schedule *Schedule
	Message  *SessionMessage
	Entry    *TranscriptEntry
	WokeLife bool
}

// DeliverDueSchedule 在事务内重读并锁定日程、推进时间、投递内部信并应用起床状态；
// advance 由调用方按一次性或重复规则计算下一次时点
func (m *Manager) DeliverDueSchedule(ctx context.Context, id uint, now time.Time, text string, build func(*SessionMessage) (json.RawMessage, error), advance func(*Schedule, time.Time) (time.Time, bool, error), wakeLife bool) (*ScheduleDelivery, error) {
	delivery := &ScheduleDelivery{}
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row Schedule
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&row).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return ErrScheduleNotDue
			}
			return err
		}
		if !row.Enabled || row.NextRunAt.After(now) {
			return ErrScheduleNotDue
		}
		nextAt, enabled, err := advance(&row, now)
		if err != nil {
			return err
		}
		if wakeLife {
			woke, wakeErr := wakeLifeFromSchedule(tx, now)
			if wakeErr != nil {
				return wakeErr
			}
			delivery.WokeLife = woke
		}
		msg, entry, err := m.enqueueSessionMessageTx(tx, ConversationKindLife, int64(LifeTargetID), row.TargetKind, row.TargetID, text, build, nil)
		if err != nil {
			return err
		}
		if err := tx.Model(&Schedule{}).Where("id=?", row.ID).Updates(map[string]any{
			"next_run_at": nextAt, "enabled": enabled, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		row.NextRunAt, row.Enabled = nextAt, enabled
		delivery.Schedule, delivery.Message, delivery.Entry = &row, msg, entry
		return nil
	})
	if err != nil {
		return nil, err
	}
	return delivery, nil
}

// ErrScheduleTextInvalid 让后台能把说明校验失败映射成稳定错误码
var ErrScheduleTextInvalid = errors.New("日程说明无效")

// validateScheduleText 与内部投信正文共用同一条上限，避免保存成功却永远投递失败
func validateScheduleText(text string) error {
	if text == "" {
		return fmt.Errorf("%w：不能为空", ErrScheduleTextInvalid)
	}
	if len([]rune(text)) > MaxSessionMessageRunes {
		return fmt.Errorf("%w：最多 %d 字", ErrScheduleTextInvalid, MaxSessionMessageRunes)
	}
	return nil
}

// Purpose 由固定槽位和醒睡状态派生，避免再存一份会漂移的日程类型
func (s Schedule) Purpose(awake bool) string {
	if s.ScheduleKey != LifeNextKey {
		return "commitment"
	}
	if awake {
		return "next"
	}
	return "wake"
}

// normalizeSchedule 校验目标、正文和一次性/重复表达二选一
func normalizeSchedule(row *Schedule) error {
	if row == nil {
		return fmt.Errorf("日程不能为空")
	}
	switch row.TargetKind {
	case ConversationKindGroup, ConversationKindPrivate, ConversationKindWorker, ConversationKindLife:
	default:
		return fmt.Errorf("日程目标类型无效")
	}
	if row.TargetID <= 0 {
		return fmt.Errorf("日程目标无效")
	}
	row.Text = strings.TrimSpace(row.Text)
	if err := validateScheduleText(row.Text); err != nil {
		return err
	}
	row.CronExpr = strings.TrimSpace(row.CronExpr)
	if row.RunAt != nil {
		if row.CronExpr != "" {
			return fmt.Errorf("一次性时间与重复规则只能二选一")
		}
		row.NextRunAt = *row.RunAt
		return nil
	}
	if row.CronExpr == "" {
		return fmt.Errorf("日程需要一次性时间或重复规则")
	}
	if row.NextRunAt.IsZero() {
		return fmt.Errorf("重复日程缺少下一次执行时间")
	}
	return nil
}
