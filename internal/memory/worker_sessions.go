package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	MaxWorkerTitleRunes   = 30
	WorkerStatusIdle      = "idle"
	WorkerStatusRunning   = "running"
	WorkerStatusFailed    = "failed"
	WorkerStatusCancelled = "cancelled"
)

// WorkerSession 保存一个 worker 的元数据和 eino 上下文检查点
type WorkerSession struct {
	ID               uint            `gorm:"column:id;primaryKey" json:"id"`
	Title            string          `gorm:"column:title;type:text;not null" json:"title"`
	Status           string          `gorm:"column:status;type:text;not null" json:"status"`
	ModelName        string          `gorm:"column:model_name;type:text;not null;default:''" json:"model_name"`
	LastError        string          `gorm:"column:last_error;type:text;not null;default:''" json:"last_error"`
	ContextSummary   json.RawMessage `gorm:"column:context_summary;type:jsonb;not null;default:'[]'" json:"-"`
	ContextStartID   uint            `gorm:"column:context_start_id;not null;default:0" json:"context_start_id"`
	ContextThroughID uint            `gorm:"column:context_through_id;not null;default:0" json:"context_through_id"`
	CreatedAt        time.Time       `gorm:"column:created_at;not null;default:now()" json:"created_at"`
	UpdatedAt        time.Time       `gorm:"column:updated_at;not null;default:now()" json:"updated_at"`
}

func (WorkerSession) TableName() string { return "worker_sessions" }

// GetWorkerSessionByID 按 id 读取 worker
func (m *Manager) GetWorkerSessionByID(ctx context.Context, id uint) (*WorkerSession, error) {
	var row WorkerSession
	if err := m.db.WithContext(ctx).Where("id=?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// CreateWorkerSession 创建一个 worker 并同步 conversation_targets
func (m *Manager) CreateWorkerSession(ctx context.Context, title string) (*WorkerSession, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("worker 标题不能为空")
	}
	if len([]rune(title)) > MaxWorkerTitleRunes {
		return nil, fmt.Errorf("worker 标题不能超过 %d 个字", MaxWorkerTitleRunes)
	}
	row := &WorkerSession{
		Title: title, Status: WorkerStatusIdle,
		ContextSummary: json.RawMessage(`[]`),
	}
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		target := &ConversationTarget{
			ConversationKind: ConversationKindWorker, TargetID: int64(row.ID),
			Name: title, Active: true, UpdatedAt: time.Now(),
		}
		return tx.Create(target).Error
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// ListWorkerSessions 返回全部 worker，包含已停用的历史 worker
func (m *Manager) ListWorkerSessions(ctx context.Context) ([]WorkerSession, error) {
	var rows []WorkerSession
	if err := m.db.WithContext(ctx).Order("updated_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// StartWorkerContextTx 在派工事务内为新上下文设置持久化起点并清掉旧摘要
func (m *Manager) StartWorkerContextTx(tx *gorm.DB, id uint, startID uint) error {
	if id == 0 || startID == 0 {
		return fmt.Errorf("worker 或上下文起点无效")
	}
	result := tx.Model(&WorkerSession{}).Where("id=? AND status=?", id, WorkerStatusIdle).Updates(map[string]any{
		"context_start_id": startID, "context_summary": json.RawMessage(`[]`),
		"context_through_id": 0, "updated_at": time.Now(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("worker 不在空闲状态，不能重置上下文")
	}
	return nil
}

// SetWorkerStatus 更新运行状态和错误信息
func (m *Manager) SetWorkerStatus(ctx context.Context, id uint, status, lastError, modelName string) error {
	updates := map[string]any{
		"status":     status,
		"last_error": lastError,
		"updated_at": time.Now(),
	}
	if modelName != "" {
		updates["model_name"] = modelName
	}
	return m.db.WithContext(ctx).Model(&WorkerSession{}).Where("id=?", id).Updates(updates).Error
}

// SetWorkerTargetActive 控制 worker 是否出现在当前 worker 池里
func (m *Manager) SetWorkerTargetActive(ctx context.Context, id uint, active bool) error {
	return m.db.WithContext(ctx).Model(&ConversationTarget{}).
		Where("conversation_kind=? AND target_id=?", ConversationKindWorker, id).
		Updates(map[string]any{"active": active, "updated_at": time.Now()}).Error
}

// UpdateWorkerTitle 同步 worker 元数据和 session 目录名称
func (m *Manager) UpdateWorkerTitle(ctx context.Context, id uint, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("worker 标题不能为空")
	}
	if len([]rune(title)) > MaxWorkerTitleRunes {
		return fmt.Errorf("worker 标题不能超过 %d 个字", MaxWorkerTitleRunes)
	}
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&WorkerSession{}).Where("id=?", id).
			Updates(map[string]any{"title": title, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		return tx.Model(&ConversationTarget{}).
			Where("conversation_kind=? AND target_id=?", ConversationKindWorker, id).
			Updates(map[string]any{"name": title, "updated_at": time.Now()}).Error
	})
}

// CommitWorkerCheckpoint 在同一事务里给真实输入分配可见序号并保存检查点
func (m *Manager) CommitWorkerCheckpoint(ctx context.Context, id uint, summary json.RawMessage, throughID uint, visibleIDs []uint) (map[uint]uint64, error) {
	if len(summary) == 0 {
		summary = json.RawMessage(`[]`)
	}
	seqs := map[uint]uint64{}
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		seqs, err = markVisibleTx(tx, ConversationKindWorker, int64(id), visibleIDs)
		if err != nil {
			return err
		}
		return tx.Model(&WorkerSession{}).Where("id=?", id).Updates(map[string]any{
			"context_summary":    summary,
			"context_through_id": throughID,
			"updated_at":         time.Now(),
		}).Error
	})
	return seqs, err
}

// FailRunningWorkerSessions 在进程重启时把中断的 worker 标成失败
func (m *Manager) FailRunningWorkerSessions(ctx context.Context) error {
	return m.db.WithContext(ctx).Model(&WorkerSession{}).
		Where("status=?", WorkerStatusRunning).
		Updates(map[string]any{
			"status":     WorkerStatusFailed,
			"last_error": "进程重启，任务中断",
			"updated_at": time.Now(),
		}).Error
}
