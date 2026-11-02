package memory

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// LatestMessageTime 返回会话最近一条消息时间
func (m *Manager) LatestMessageTime(ctx context.Context, kind string, targetID int64, excludeUserID int64) (time.Time, error) {
	var value *time.Time
	err := m.db.WithContext(ctx).Model(&MessageLog{}).
		Where("conversation_kind=? AND target_id=? AND user_id<>?", kind, targetID, excludeUserID).
		Select("MAX(message_time)").Scan(&value).Error
	if err == gorm.ErrRecordNotFound || value == nil {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return *value, nil
}
