package memory

import (
	"time"

	"mumu-bot/internal/config"

	"go.uber.org/zap"
)

func (m *Manager) startMessageLogCleanup() {
	cfg := config.Get().Memory.MessageLogCleanup
	if cfg.Enabled != nil && !*cfg.Enabled {
		return
	}
	interval := time.Duration(cfg.IntervalHours) * time.Hour
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	keepLatest := cfg.KeepLatest
	if keepLatest <= 0 {
		keepLatest = 500
	}
	m.background.Add(1)
	go func() {
		defer m.background.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.cleanupMessageLogs(keepLatest)
			case <-m.cleanupStop:
				return
			}
		}
	}()
}

func (m *Manager) cleanupMessageLogs(keepLatest int) {
	var groups []int64
	if err := m.db.Model(&MessageLog{}).Distinct("group_id").Pluck("group_id", &groups).Error; err != nil {
		zap.L().Warn("读取消息清理群列表失败", zap.Error(err))
		return
	}
	for _, groupID := range groups {
		var states []LearningState
		if err := m.db.Where("group_id = ? AND kind IN ?", groupID, []LearningKind{LearningKindCulture, LearningKindMemberProfile}).Find(&states).Error; err != nil {
			zap.L().Warn("读取消息清理学习状态失败", zap.Int64("group_id", groupID), zap.Error(err))
			continue
		}
		if len(states) != 2 {
			continue
		}
		watermark := states[0].LastMessageLogID
		if states[1].LastMessageLogID < watermark {
			watermark = states[1].LastMessageLogID
		}
		if watermark == 0 {
			continue
		}
		var keepFloor uint
		if err := m.db.Model(&MessageLog{}).Where("group_id = ?", groupID).
			Order("id DESC").Offset(keepLatest-1).Limit(1).Pluck("id", &keepFloor).Error; err != nil {
			zap.L().Warn("读取消息清理保留边界失败", zap.Int64("group_id", groupID), zap.Error(err))
			continue
		}
		if keepFloor == 0 {
			continue
		}
		result := m.db.Exec(`WITH deletable AS (
			SELECT ml.id FROM message_logs ml
			WHERE ml.group_id = ? AND ml.id <= ? AND ml.id < ?
			AND NOT EXISTS (SELECT 1 FROM topic_assignments ta WHERE ta.message_log_id = ml.id AND ta.topic_id IS NOT NULL)
			AND NOT EXISTS (SELECT 1 FROM memory_evidence e WHERE e.message_log_id = ml.id)
			AND NOT EXISTS (SELECT 1 FROM style_pattern_evidence e WHERE e.message_log_id = ml.id)
			AND NOT EXISTS (SELECT 1 FROM jargon_evidence e WHERE e.message_log_id = ml.id)
			AND NOT EXISTS (SELECT 1 FROM member_trait_evidence e WHERE e.message_log_id = ml.id)
			ORDER BY ml.id LIMIT 500
		) DELETE FROM message_logs WHERE id IN (SELECT id FROM deletable)`, groupID, watermark, keepFloor)
		if result.Error != nil {
			zap.L().Warn("清理历史消息失败", zap.Int64("group_id", groupID), zap.Error(result.Error))
		} else if result.RowsAffected > 0 {
			zap.L().Info("清理历史消息完成", zap.Int64("group_id", groupID), zap.Int64("deleted", result.RowsAffected))
		}
	}
}
