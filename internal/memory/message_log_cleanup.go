package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
				m.cleanupUnusedStickers()
			case <-m.cleanupStop:
				return
			}
		}
	}()
}

func (m *Manager) cleanupMessageLogs(keepLatest int) {
	var groups []int64
	if err := m.db.Model(&MessageLog{}).Where("conversation_kind=?", ConversationKindGroup).Distinct("target_id").Pluck("target_id", &groups).Error; err != nil {
		zap.L().Warn("读取消息清理群列表失败", zap.Error(err))
		return
	}
	for _, groupID := range groups {
		var states []LearningState
		if err := m.db.Where("conversation_kind=? AND target_id = ?", ConversationKindGroup, groupID).Find(&states).Error; err != nil {
			zap.L().Warn("读取消息清理学习状态失败", zap.Int64("target_id", groupID), zap.Error(err))
			continue
		}
		if len(states) != 1 {
			continue
		}
		watermark := states[0].LastMessageLogID
		if watermark == 0 {
			continue
		}
		var keepFloor uint
		if err := m.db.Model(&MessageLog{}).Where("conversation_kind=? AND target_id = ?", ConversationKindGroup, groupID).
			Order("id DESC").Offset(keepLatest-1).Limit(1).Pluck("id", &keepFloor).Error; err != nil {
			zap.L().Warn("读取消息清理保留边界失败", zap.Int64("target_id", groupID), zap.Error(err))
			continue
		}
		if keepFloor == 0 {
			continue
		}
		result := m.db.Exec(`WITH deletable AS (
			SELECT ml.id FROM message_logs ml
			WHERE ml.conversation_kind = 'group' AND ml.target_id = ? AND ml.id <= ? AND ml.id < ?
			AND NOT EXISTS (SELECT 1 FROM topic_assignments ta WHERE ta.message_log_id = ml.id AND ta.topic_id IS NOT NULL)
			AND NOT EXISTS (SELECT 1 FROM knowledge_evidence_messages e WHERE e.message_log_id = ml.id)
			AND NOT EXISTS (SELECT 1 FROM topic_summary_sources s WHERE s.message_log_id = ml.id)
			ORDER BY ml.id LIMIT 500
		) DELETE FROM message_logs WHERE id IN (SELECT id FROM deletable)`, groupID, watermark, keepFloor)
		if result.Error != nil {
			zap.L().Warn("清理历史消息失败", zap.Int64("target_id", groupID), zap.Error(result.Error))
		} else if result.RowsAffected > 0 {
			zap.L().Info("清理历史消息完成", zap.Int64("target_id", groupID), zap.Int64("deleted", result.RowsAffected))
		}
	}
}

// cleanupUnusedStickers 清理创建超过 7 天且使用次数不超过 2 的表情包；文件删除失败只告警，不回滚记录
func (m *Manager) cleanupUnusedStickers() {
	deadline := time.Now().Add(-7 * 24 * time.Hour)
	var stickers []Sticker
	if err := m.db.Raw(`DELETE FROM stickers WHERE created_at<? AND use_count<=2 RETURNING *`, deadline).Scan(&stickers).Error; err != nil {
		zap.L().Warn("清理未使用表情包失败", zap.Error(err))
		return
	}
	if len(stickers) == 0 {
		return
	}
	dir := strings.TrimSpace(config.Get().Sticker.StoragePath)
	if dir != "" {
		for _, item := range stickers {
			path := filepath.Join(dir, item.FileName)
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				zap.L().Warn("清理表情包文件失败", zap.String("path", path), zap.Error(err))
			}
		}
	}
	zap.L().Info("清理未使用表情包完成", zap.Int("count", len(stickers)))
}
