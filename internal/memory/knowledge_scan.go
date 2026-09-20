package memory

import (
	"context"
	"fmt"
)

func (m *Manager) KnowledgeScanCursor(ctx context.Context, groupID int64) (uint, error) {
	var id uint
	err := m.db.WithContext(ctx).Raw("SELECT COALESCE((SELECT last_message_log_id FROM learning_states WHERE group_id = ?),0)", groupID).Scan(&id).Error
	return id, err
}

func (m *Manager) KnowledgeScanBatch(ctx context.Context, groupID int64, after uint, limit int) ([]MessageLog, error) {
	if groupID <= 0 || limit <= 0 || limit > 100 {
		return nil, fmt.Errorf("无效调查批次范围")
	}
	var rows []MessageLog
	err := m.db.WithContext(ctx).Where("group_id=? AND id>?", groupID, after).Order("id").Limit(limit).Find(&rows).Error
	return rows, err
}
