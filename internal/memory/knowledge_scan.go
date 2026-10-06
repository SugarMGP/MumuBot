package memory

import (
	"context"
	"fmt"
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
