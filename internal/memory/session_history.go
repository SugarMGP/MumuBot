package memory

import (
	"context"
)

// RecentVisibleTranscript 读取最近记录；失败记录 system_note 即使未进入模型上下文也要能查到
func (m *Manager) RecentVisibleTranscript(ctx context.Context, kind string, targetID int64, beforeID uint, limit int) ([]TranscriptEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	q := m.db.WithContext(ctx).
		Where("conversation_kind=? AND target_id=? AND (visible_seq IS NOT NULL OR entry_kind='system_note')", kind, targetID).
		Where("entry_kind IN ('user','assistant','tool','observation','agent_message','system_note')")
	if beforeID > 0 {
		q = q.Where("id < ?", beforeID)
	}
	var rows []TranscriptEntry
	if err := q.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
