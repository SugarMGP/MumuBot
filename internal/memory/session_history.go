package memory

import (
	"context"
	"time"
)

// RecentVisibleTranscript 读取最近记录；失败记录 system_note 即使未进入模型上下文也要能查到
func (m *Manager) RecentVisibleTranscript(ctx context.Context, kind string, targetID int64, beforeID uint, from, until *time.Time, limit int) ([]TranscriptEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	q := m.db.WithContext(ctx).
		// assistant/tool 一旦持久化就是真实执行轨迹；user/observation/agent_message 只算实际进入过模型的输入
		Where("conversation_kind=? AND target_id=? AND (entry_kind IN ('assistant','tool','system_note') OR visible_seq IS NOT NULL)", kind, targetID).
		Where("entry_kind IN ('user','assistant','tool','observation','agent_message','system_note')")
	if beforeID > 0 {
		q = q.Where("id < ?", beforeID)
	}
	// 终态工具按 completed_at 计活动时间，其它轨迹仍用创建时间
	activityAt := `COALESCE((payload->'extra'->>'completed_at')::timestamptz, created_at)`
	if from != nil {
		q = q.Where(activityAt+">=?", *from)
	}
	if until != nil {
		q = q.Where(activityAt+"<?", *until)
	}
	var rows []TranscriptEntry
	if err := q.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// RunningToolTranscripts 单独列出尚未结束的工具轨迹，避免早开始的长任务被时间范围过滤
func (m *Manager) RunningToolTranscripts(ctx context.Context, kind string, targetID int64, limit int) ([]TranscriptEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	var rows []TranscriptEntry
	err := m.db.WithContext(ctx).
		Where("conversation_kind=? AND target_id=? AND entry_kind='tool' AND tool_status IN ('running','planned')", kind, targetID).
		Order("id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}
