package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mumu-bot/internal/config"

	"go.uber.org/zap"
	"gorm.io/gorm"
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
	for _, kind := range []string{ConversationKindGroup, ConversationKindPrivate} {
		m.cleanupConversationLogs(kind, keepLatest)
	}
}

// cleanupConversationLogs 清理单类会话的历史消息：只删除整理水位之下、保留窗口之外
// 且未被话题归属、知识证据或摘要来源引用的原文
func (m *Manager) cleanupConversationLogs(kind string, keepLatest int) {
	var targets []int64
	if err := m.db.Model(&MessageLog{}).Where("conversation_kind=?", kind).Distinct("target_id").Pluck("target_id", &targets).Error; err != nil {
		zap.L().Warn("读取消息清理会话列表失败", zap.String("conversation_kind", kind), zap.Error(err))
		return
	}
	for _, targetID := range targets {
		// 尚未整理的会话不清理，避免删除 learner 待处理的消息
		var state LearningState
		if err := m.db.Where("conversation_kind=? AND target_id=?", kind, targetID).Take(&state).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				zap.L().Warn("读取消息清理学习状态失败", zap.String("conversation_kind", kind), zap.Int64("target_id", targetID), zap.Error(err))
			}
			continue
		}
		if state.LastMessageLogID == 0 {
			continue
		}
		var keepFloor uint
		if err := m.db.Model(&MessageLog{}).Where("conversation_kind=? AND target_id=?", kind, targetID).
			Order("id DESC").Offset(keepLatest-1).Limit(1).Pluck("id", &keepFloor).Error; err != nil {
			zap.L().Warn("读取消息清理保留边界失败", zap.String("conversation_kind", kind), zap.Int64("target_id", targetID), zap.Error(err))
			continue
		}
		if keepFloor == 0 {
			continue
		}
		result := m.db.Exec(`WITH deletable AS (
			SELECT ml.id FROM message_logs ml
			WHERE ml.conversation_kind = ? AND ml.target_id = ? AND ml.id <= ? AND ml.id < ?
			AND NOT EXISTS (SELECT 1 FROM topic_assignments ta WHERE ta.message_log_id = ml.id AND ta.topic_id IS NOT NULL)
			AND NOT EXISTS (SELECT 1 FROM knowledge_evidence_messages e WHERE e.message_log_id = ml.id)
			AND NOT EXISTS (SELECT 1 FROM topic_summary_sources s WHERE s.message_log_id = ml.id)
			ORDER BY ml.id LIMIT 500
		) DELETE FROM message_logs WHERE id IN (SELECT id FROM deletable)`, kind, targetID, state.LastMessageLogID, keepFloor)
		if result.Error != nil {
			zap.L().Warn("清理历史消息失败", zap.String("conversation_kind", kind), zap.Int64("target_id", targetID), zap.Error(result.Error))
		} else if result.RowsAffected > 0 {
			zap.L().Info("清理历史消息完成", zap.String("conversation_kind", kind), zap.Int64("target_id", targetID), zap.Int64("deleted", result.RowsAffected))
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
