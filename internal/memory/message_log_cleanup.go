package memory

import (
	"errors"
	"time"

	"mumu-bot/internal/config"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// startMessageLogCleanup 启动消息日志清理定时任务
func (m *Manager) startMessageLogCleanup() {
	cleanupCfg := config.Get().Memory.MessageLogCleanup
	enabled := true
	if cleanupCfg.Enabled != nil {
		enabled = *cleanupCfg.Enabled
	}
	if !enabled {
		return
	}

	intervalHours := cleanupCfg.IntervalHours
	if intervalHours <= 0 {
		intervalHours = 6
	}
	keepLatest := cleanupCfg.KeepLatest
	if keepLatest <= 0 {
		keepLatest = 500
	}

	go m.cleanupMessageLogs(keepLatest)

	ticker := time.NewTicker(time.Duration(intervalHours) * time.Hour)
	go func() {
		for {
			select {
			case <-ticker.C:
				m.cleanupMessageLogs(keepLatest)
			case <-m.cleanupStop:
				ticker.Stop()
				return
			}
		}
	}()
}

// cleanupMessageLogs 清理消息日志，仅保留每个群最新的 keepLatest 条
func (m *Manager) cleanupMessageLogs(keepLatest int) {
	if keepLatest <= 0 {
		return
	}

	var groupIDs []int64
	if err := m.db.Model(&MessageLog{}).Distinct("group_id").Pluck("group_id", &groupIDs).Error; err != nil {
		zap.L().Warn("清理消息日志失败：获取群列表失败", zap.Error(err))
		return
	}

	for _, groupID := range groupIDs {
		var threshold struct {
			ID        uint
			CreatedAt time.Time
		}
		err := m.db.Model(&MessageLog{}).
			Select("id", "created_at").
			Where("group_id = ?", groupID).
			Order("created_at DESC").
			Order("id DESC").
			Offset(keepLatest - 1).
			Limit(1).
			Take(&threshold).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			zap.L().Warn("清理消息日志失败：获取最新保留边界失败", zap.Int64("group_id", groupID), zap.Error(err))
			continue
		}

		state, err := m.GetLearningState(groupID)
		if err != nil {
			zap.L().Warn("清理消息日志失败：获取学习进度失败", zap.Int64("group_id", groupID), zap.Error(err))
			continue
		}
		if state.LastMessageID == 0 {
			continue
		}

		query := m.db.Where(
			"group_id = ? AND topic_thread_id = 0 AND (created_at < ? OR (created_at = ? AND id < ?))",
			groupID,
			threshold.CreatedAt,
			threshold.CreatedAt,
			threshold.ID,
		)
		query = query.Where("id <= ?", state.LastMessageID)
		result := query.Delete(&MessageLog{})
		if result.Error != nil {
			zap.L().Warn("清理消息日志失败：删除旧记录失败", zap.Int64("group_id", groupID), zap.Error(result.Error))
			continue
		}
		if result.RowsAffected > 0 {
			zap.L().Info("消息日志已清理", zap.Int64("group_id", groupID), zap.Int("deleted", int(result.RowsAffected)))
		}
	}
}
