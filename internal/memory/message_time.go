package memory

import (
	"context"
	"time"
)

// LatestMessageTime 返回会话最近一条消息时间
func (m *Manager) LatestMessageTime(ctx context.Context, kind string, targetID int64, excludeUserID int64) (time.Time, error) {
	var row struct {
		Latest *time.Time `gorm:"column:latest"`
	}
	err := m.db.WithContext(ctx).Model(&MessageLog{}).
		Where("conversation_kind=? AND target_id=? AND user_id<>?", kind, targetID, excludeUserID).
		Select("MAX(message_time) AS latest").Scan(&row).Error
	if err != nil {
		return time.Time{}, err
	}
	if row.Latest == nil {
		return time.Time{}, nil
	}
	return *row.Latest, nil
}
