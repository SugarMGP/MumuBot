package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mumu-bot/internal/media"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const mediaRefsGINQuery = `media_refs @> ?::jsonb`

type deletedMessage struct {
	ID       uint   `gorm:"column:id"`
	MediaRef []byte `gorm:"column:media_refs"`
}

// CleanupExpiredMessages 删除超过最低保留期且不再被 transcript 引用的原文，返回待回收媒体
func (m *Manager) CleanupExpiredMessages(ctx context.Context, now time.Time, limit int) ([]media.Ref, int, error) {
	if limit <= 0 {
		limit = 500
	}
	cutoff := now.Add(-time.Duration(CurrentRetentionPolicy().HistoryDays) * 24 * time.Hour)
	var deleted []deletedMessage
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Raw(`
			WITH due AS (
				SELECT id FROM message_logs
				WHERE message_time < ?
					AND NOT EXISTS (
						SELECT 1 FROM conversation_transcript t WHERE t.message_log_id=message_logs.id
					)
				ORDER BY message_time,id LIMIT ?
				FOR UPDATE SKIP LOCKED
			)
			DELETE FROM message_logs
			WHERE id IN (SELECT id FROM due)
				AND NOT EXISTS (
					SELECT 1 FROM conversation_transcript t WHERE t.message_log_id=message_logs.id
				)
			RETURNING id, media_refs`, cutoff, limit).Scan(&deleted).Error
	})
	if err != nil {
		return nil, 0, err
	}
	refs := make([]media.Ref, 0)
	for _, row := range deleted {
		if len(row.MediaRef) == 0 {
			continue
		}
		var mediaRefs media.Refs
		if err := json.Unmarshal(row.MediaRef, &mediaRefs); err != nil {
			zap.L().Warn("解析已删除原文的媒体引用失败", zap.Uint("message_log_id", row.ID), zap.Error(err))
			continue
		}
		refs = append(refs, mediaRefs...)
	}
	return refs, len(deleted), nil
}

// ReleaseMedia 在原文事务提交后回收不再被任何原文和收藏引用的聊天媒体缓存
func (m *Manager) ReleaseMedia(ctx context.Context, refs []media.Ref) (int, error) {
	if len(refs) == 0 || strings.TrimSpace(m.mediaDir) == "" {
		return 0, nil
	}
	seen := map[string]bool{}
	removed := 0
	for _, ref := range refs {
		key := fmt.Sprintf("%t:%s", ref.Sticker, ref.Hash)
		if ref.Hash == "" || seen[key] {
			continue
		}
		seen[key] = true
		used, err := m.mediaRefExists(ctx, ref)
		if err != nil {
			return removed, err
		}
		if used {
			continue
		}
		path := m.mediaPath(ref)
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			zap.L().Warn("删除媒体缓存失败", zap.String("path", path), zap.Error(err))
			continue
		}
		removed++
	}
	return removed, nil
}

// CleanupOrphanMedia 补扫没有被任何原文引用的旧媒体文件
func (m *Manager) CleanupOrphanMedia(ctx context.Context, now time.Time) (int, error) {
	if strings.TrimSpace(m.mediaDir) == "" {
		return 0, nil
	}
	cutoff := now.Add(-time.Duration(CurrentRetentionPolicy().HistoryDays) * 24 * time.Hour)
	removed := 0
	for _, kind := range []struct {
		dir     string
		sticker bool
	}{{"image", false}, {"sticker", true}} {
		dir := filepath.Join(m.mediaDir, kind.dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil || info.ModTime().After(cutoff) {
				continue
			}
			hash := entry.Name()
			if !kind.sticker {
				if filepath.Ext(hash) != ".webp" {
					continue
				}
				hash = strings.TrimSuffix(hash, ".webp")
			}
			used, err := m.mediaRefExists(ctx, media.Ref{Hash: hash, Sticker: kind.sticker})
			if err != nil {
				return removed, err
			}
			if used {
				continue
			}
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
				zap.L().Warn("删除孤立媒体失败", zap.String("path", filepath.Join(dir, entry.Name())), zap.Error(err))
				continue
			}
			removed++
		}
	}
	return removed, nil
}

func (m *Manager) mediaRefExists(ctx context.Context, ref media.Ref) (bool, error) {
	payload, err := json.Marshal([]map[string]any{{"hash": ref.Hash, "sticker": ref.Sticker}})
	if err != nil {
		return false, err
	}
	var exists bool
	if err := m.db.WithContext(ctx).Raw(`
		SELECT EXISTS(SELECT 1 FROM message_logs WHERE `+mediaRefsGINQuery+`)`, string(payload)).Scan(&exists).Error; err != nil {
		return false, err
	}
	return exists, nil
}

func (m *Manager) mediaPath(ref media.Ref) string {
	if ref.Hash == "" || strings.TrimSpace(m.mediaDir) == "" {
		return ""
	}
	base := filepath.Join(m.mediaDir, "image", ref.Hash+".webp")
	if ref.Sticker {
		base = filepath.Join(m.mediaDir, "sticker", ref.Hash)
	}
	return base
}

// CleanupTrajectories 删除 life/worker 已被有效摘要覆盖且超过历史期限的可见旧轨迹
func (m *Manager) CleanupTrajectories(ctx context.Context, now time.Time) (TranscriptCandidates, int, error) {
	cutoff := now.Add(-time.Duration(CurrentRetentionPolicy().HistoryDays) * 24 * time.Hour)
	released := TranscriptCandidates{}
	deleted := 0
	var life struct {
		Summary   []byte `gorm:"column:context_summary"`
		ThroughID uint   `gorm:"column:context_through_id"`
	}
	if err := m.db.WithContext(ctx).Raw(`SELECT context_summary,context_through_id FROM life_state WHERE id=?`, LifeTargetID).Scan(&life).Error; err != nil {
		return TranscriptCandidates{}, 0, err
	}
	if validTrajectorySummary(life.Summary) && life.ThroughID > 0 {
		candidates, count, err := m.cleanupTrajectory(ctx, ConversationKindLife, int64(LifeTargetID), life.ThroughID, cutoff, true)
		if err != nil {
			return TranscriptCandidates{}, 0, err
		}
		released.ToolRefs = append(released.ToolRefs, candidates.ToolRefs...)
		released.SnapshotRefs = append(released.SnapshotRefs, candidates.SnapshotRefs...)
		deleted += count
	}
	var workers []struct {
		ID        uint   `gorm:"column:id"`
		Summary   []byte `gorm:"column:context_summary"`
		StartID   uint   `gorm:"column:context_start_id"`
		ThroughID uint   `gorm:"column:context_through_id"`
	}
	if err := m.db.WithContext(ctx).Raw(`SELECT id,context_summary,context_start_id,context_through_id FROM worker_sessions`).Scan(&workers).Error; err != nil {
		return TranscriptCandidates{}, 0, err
	}
	for _, worker := range workers {
		// 显式关闭的旧上下文不要求先有摘要，过了原文保留期即可清理
		if worker.StartID > 1 {
			candidates, count, err := m.cleanupTrajectory(ctx, ConversationKindWorker, int64(worker.ID), worker.StartID-1, cutoff, false)
			if err != nil {
				return TranscriptCandidates{}, 0, err
			}
			released.ToolRefs = append(released.ToolRefs, candidates.ToolRefs...)
			released.SnapshotRefs = append(released.SnapshotRefs, candidates.SnapshotRefs...)
			deleted += count
		}
		if !validTrajectorySummary(worker.Summary) || worker.ThroughID == 0 {
			continue
		}
		candidates, count, err := m.cleanupTrajectory(ctx, ConversationKindWorker, int64(worker.ID), worker.ThroughID, cutoff, true)
		if err != nil {
			return TranscriptCandidates{}, 0, err
		}
		released.ToolRefs = append(released.ToolRefs, candidates.ToolRefs...)
		released.SnapshotRefs = append(released.SnapshotRefs, candidates.SnapshotRefs...)
		deleted += count
	}
	return released, deleted, nil
}

func validTrajectorySummary(raw []byte) bool {
	text := strings.TrimSpace(string(raw))
	return text != "" && text != "[]" && text != "null"
}

func (m *Manager) cleanupTrajectory(ctx context.Context, kind string, targetID int64, throughID uint, cutoff time.Time, requireVisible bool) (TranscriptCandidates, int, error) {
	candidates := TranscriptCandidates{}
	deleted := 0
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 只清理过期前缀，保留新轨迹以及尚未结束的工具链
		var firstProtected *uint
		if err := tx.Raw(`SELECT MIN(t.id) FROM conversation_transcript t
			LEFT JOIN message_logs m ON m.id=t.message_log_id
			WHERE t.conversation_kind=? AND t.target_id=? AND t.id<=?
				AND t.entry_kind IN ('user','observation','agent_message','assistant','tool','system_note')
				AND (COALESCE((t.payload->'extra'->>'completed_at')::timestamptz,m.message_time,t.created_at)>=?
					OR t.tool_status IN ('planned','running')
					OR (? AND t.entry_kind='tool' AND t.visible_seq IS NULL))`, kind, targetID, throughID, cutoff, requireVisible).Scan(&firstProtected).Error; err != nil {
			return err
		}
		if firstProtected != nil {
			throughID = min(throughID, *firstProtected-1)
		}
		if throughID == 0 {
			return nil
		}
		incomplete, err := incompleteToolChains(tx, kind, targetID, throughID, requireVisible)
		if err != nil || incomplete {
			return err
		}
		condition := "conversation_kind=? AND target_id=? AND id<=? AND entry_kind IN ('user','observation','agent_message','assistant','tool','system_note')"
		args := []any{kind, targetID, throughID}
		if requireVisible {
			condition += " AND visible_seq IS NOT NULL"
		}
		candidates, err = transcriptCandidates(tx, condition, args...)
		if err != nil {
			return err
		}
		result := tx.Where(condition, args...).Delete(&TranscriptEntry{})
		deleted = int(result.RowsAffected)
		return result.Error
	})
	return candidates, deleted, err
}

// incompleteToolChains 保护跨 cutoff、尚未回执或回执尚未进入成功请求的助手调用链
func incompleteToolChains(tx *gorm.DB, kind string, targetID int64, throughID uint, requireVisible bool) (bool, error) {
	var assistants []TranscriptEntry
	q := tx.Where("conversation_kind=? AND target_id=? AND entry_kind='assistant' AND id<=?", kind, targetID, throughID)
	if requireVisible {
		q = q.Where("visible_seq IS NOT NULL")
	}
	if err := q.Find(&assistants).Error; err != nil {
		return false, err
	}
	for _, assistant := range assistants {
		var message struct {
			ContentBlocks []struct {
				FunctionToolCall *struct {
					CallID string `json:"call_id"`
				} `json:"function_tool_call"`
			} `json:"content_blocks"`
		}
		if err := sonic.Unmarshal(assistant.Payload, &message); err != nil {
			return true, nil
		}
		for _, block := range message.ContentBlocks {
			callID := ""
			if block.FunctionToolCall != nil {
				callID = strings.TrimSpace(block.FunctionToolCall.CallID)
			}
			if callID == "" {
				continue
			}
			var result TranscriptEntry
			err := tx.Where("conversation_kind=? AND target_id=? AND entry_kind='tool' AND run_id=? AND tool_call_id=?", kind, targetID, assistant.RunID, callID).Take(&result).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return true, nil
				}
				return false, err
			}
			if (requireVisible && result.VisibleSeq == nil) || result.ID > throughID || result.ToolStatus == "" || result.ToolStatus == "running" || result.ToolStatus == "planned" {
				return true, nil
			}
		}
	}
	return false, nil
}
