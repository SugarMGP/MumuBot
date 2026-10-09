package memory

import (
	"context"
	"fmt"
	"time"
)

func (m *Manager) ConversationScanCursor(ctx context.Context, kind string, targetID int64) (uint, error) {
	var id uint
	err := m.db.WithContext(ctx).Raw("SELECT COALESCE((SELECT last_message_log_id FROM learning_states WHERE conversation_kind=? AND target_id=?),0)", kind, targetID).Scan(&id).Error
	return id, err
}

func (m *Manager) ConversationScanBatch(ctx context.Context, kind string, targetID int64, after uint, limit int) ([]MessageLog, error) {
	if targetID <= 0 || limit <= 0 || limit > 100 {
		return nil, fmt.Errorf("无效调查批次范围")
	}
	var rows []MessageLog
	err := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=? AND id>?", kind, targetID, after).Order("id").Limit(limit).Find(&rows).Error
	return rows, err
}

// SetLearningNextAttempt 记录会话整理冷却的完成时间点，重启后仍遵守剩余冷却
func (m *Manager) SetLearningNextAttempt(ctx context.Context, kind string, targetID int64, next time.Time) error {
	return m.db.WithContext(ctx).Exec(`INSERT INTO learning_states(conversation_kind,target_id,last_message_log_id,next_attempt_at) VALUES (?, ?, 0, ?)
		ON CONFLICT(conversation_kind,target_id) DO UPDATE SET next_attempt_at=EXCLUDED.next_attempt_at`, kind, targetID, next).Error
}

// ListLearningCooldowns 返回仍在冷却中的会话，供启动时恢复冷却水位
func (m *Manager) ListLearningCooldowns(ctx context.Context, now time.Time) ([]LearningState, error) {
	var rows []LearningState
	err := m.db.WithContext(ctx).Where("next_attempt_at > ?", now).Find(&rows).Error
	return rows, err
}
