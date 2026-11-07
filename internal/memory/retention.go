package memory

import (
	"context"
	"fmt"
	"math"
	"time"

	"mumu-bot/internal/config"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RetentionPolicy 是记忆和话题共用的遗忘策略，只接收已校验的数值
type RetentionPolicy struct {
	InitialHalfLifeDays float64
	MaxHalfLifeDays     float64
	ReinforceFactor     float64
	ForgetThreshold     float64
	HistoryDays         int
}

// PolicyFromConfig 把动态配置转成保留策略
func PolicyFromConfig(cfg config.RetentionConfig) RetentionPolicy {
	return RetentionPolicy{
		InitialHalfLifeDays: float64(cfg.InitialHalfLifeDays),
		MaxHalfLifeDays:     float64(cfg.MaxHalfLifeDays),
		ReinforceFactor:     cfg.ReinforceFactor,
		ForgetThreshold:     cfg.ForgetThreshold,
		HistoryDays:         cfg.HistoryDays,
	}
}

// CurrentRetentionPolicy 返回当前配置快照里的保留策略
func CurrentRetentionPolicy() RetentionPolicy {
	return PolicyFromConfig(config.Snapshot().Retention)
}

func (p RetentionPolicy) logFactor() float64 {
	return math.Log2(1 / p.ForgetThreshold)
}

// ForgetAfterDays 返回从锚点起无检索多少天后遗忘
func (p RetentionPolicy) ForgetAfterDays(halfLifeDays float64) float64 {
	return halfLifeDays * p.logFactor()
}

// ForgetAt 按锚点和当前半衰期计算到期时间
func (p RetentionPolicy) ForgetAt(anchor time.Time, halfLifeDays float64) time.Time {
	return anchor.Add(time.Duration(p.ForgetAfterDays(halfLifeDays) * 24 * float64(time.Hour)))
}

// Strength 返回锚点到 now 的工程保持度，仅用于计算强化量
func (p RetentionPolicy) Strength(anchor time.Time, halfLifeDays float64, now time.Time) float64 {
	if halfLifeDays <= 0 {
		halfLifeDays = 1
	}
	ageDays := math.Max(0, now.Sub(anchor).Hours()/24)
	return math.Pow(2, -ageDays/halfLifeDays)
}

// ReinforcedHalfLife 返回本次检索后的半衰期，已有半衰期不会降低
func (p RetentionPolicy) ReinforcedHalfLife(halfLifeDays float64, anchor time.Time, now time.Time) float64 {
	if halfLifeDays <= 0 {
		halfLifeDays = p.InitialHalfLifeDays
	}
	next := halfLifeDays * (1 + p.ReinforceFactor*(1-p.Strength(anchor, halfLifeDays, now)))
	if next > p.MaxHalfLifeDays {
		next = p.MaxHalfLifeDays
	}
	return next
}

// MemoryState 生成新记忆的保留字段；置顶记忆没有到期时间
func (p RetentionPolicy) MemoryState(pinned bool, now time.Time) (float64, time.Time, *time.Time) {
	anchor := now
	if pinned {
		return p.InitialHalfLifeDays, anchor, nil
	}
	forgetAt := p.ForgetAt(anchor, p.InitialHalfLifeDays)
	return p.InitialHalfLifeDays, anchor, &forgetAt
}

// TopicState 生成新话题的保留字段，锚点使用最早的真实事件时间
func (p RetentionPolicy) TopicState(anchor time.Time, now time.Time) (float64, time.Time, time.Time) {
	if anchor.IsZero() || anchor.After(now) {
		anchor = now
	}
	return p.InitialHalfLifeDays, anchor, p.ForgetAt(anchor, p.InitialHalfLifeDays)
}

// SettleExpired 结算当前已到期的记忆和话题；只由维护循环或配置事务调用
func (m *Manager) SettleExpired(ctx context.Context, now time.Time) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := settleExpiredMemoriesTx(tx, now); err != nil {
			return err
		}
		return settleExpiredTopicsTx(tx, now)
	})
}

func settleExpiredMemoriesTx(tx *gorm.DB, now time.Time) (int, error) {
	return archiveExpiredMemoriesTx(tx, "status='active' AND pinned=false AND forget_at IS NOT NULL AND forget_at<=?", []any{now}, now)
}

// settleExpiredMemoryHashTx 只结算与本次写入同哈希的到期条目，避免创建时全库扫描
func settleExpiredMemoryHashTx(tx *gorm.DB, subjectType, subjectTarget, memoryType, hash string, now time.Time) (int, error) {
	return archiveExpiredMemoriesTx(tx,
		"status='active' AND pinned=false AND subject_type=? AND subject_target=? AND memory_type=? AND content_hash=? AND forget_at<=?",
		[]any{subjectType, subjectTarget, memoryType, hash, now}, now)
}

func archiveExpiredMemoriesTx(tx *gorm.DB, condition string, args []any, now time.Time) (int, error) {
	total := 0
	for {
		rows := make([]KnowledgeRecord, 0, 500)
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(condition, args...).
			Order("id").Limit(500).Find(&rows).Error; err != nil {
			return total, err
		}
		if len(rows) == 0 {
			return total, nil
		}
		for _, row := range rows {
			version := row.Version + 1
			result := tx.Model(&KnowledgeRecord{}).Where("id=? AND version=?", row.ID, row.Version).
				Updates(map[string]any{"status": "archived", "version": version, "updated_at": now})
			if result.Error != nil {
				return total, result.Error
			}
			if result.RowsAffected == 0 {
				return total, fmt.Errorf("记忆 %d 版本已变化，保留期结算失败", row.ID)
			}
			if err := tx.Create(&KnowledgeRevision{
				ItemID: row.ID, Version: version,
				BeforeContent: row.Content, AfterContent: row.Content,
				BeforeStatus: row.Status, AfterStatus: "archived",
				BeforePinned: row.Pinned, AfterPinned: row.Pinned,
				Reason: "保留期到期", CreatedAt: now,
			}).Error; err != nil {
				return total, err
			}
		}
		total += len(rows)
	}
}

func settleExpiredTopicsTx(tx *gorm.DB, now time.Time) error {
	for {
		result := tx.Exec(`
			WITH due AS (
				SELECT id FROM topic_threads
				WHERE forgotten_at IS NULL AND forget_at<=?
				ORDER BY forget_at,id LIMIT 500
			)
			UPDATE topic_threads SET forgotten_at=?
			WHERE id IN (SELECT id FROM due)`, now, now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
	}
}

// ApplyRetentionPolicy 在调用方事务里先按旧期限结算，再按新策略重算
func ApplyRetentionPolicy(tx *gorm.DB, policy RetentionPolicy, now time.Time) error {
	if _, err := settleExpiredMemoriesTx(tx, now); err != nil {
		return err
	}
	if err := settleExpiredTopicsTx(tx, now); err != nil {
		return err
	}
	factor := policy.logFactor()
	maxHalfLife := policy.MaxHalfLifeDays
	if err := tx.Exec(`
		UPDATE knowledge_items
		SET stability_days=LEAST(stability_days, ?),
			forget_at=CASE WHEN pinned THEN NULL
				ELSE recall_anchor_at + interval '1 day' * (LEAST(stability_days, ?) * ?)
			END
		WHERE status='active'`, maxHalfLife, maxHalfLife, factor).Error; err != nil {
		return err
	}
	if err := tx.Exec(`
		UPDATE topic_threads
		SET stability_days=LEAST(stability_days, ?),
			forget_at=recall_anchor_at + interval '1 day' * (LEAST(stability_days, ?) * ?)
		WHERE forgotten_at IS NULL`, maxHalfLife, maxHalfLife, factor).Error; err != nil {
		return err
	}
	// 重算后可能立即到期（例如阈值调高或最大半衰期调低），同样按已到期结算
	if _, err := settleExpiredMemoriesTx(tx, now); err != nil {
		return err
	}
	return settleExpiredTopicsTx(tx, now)
}

// ReinforceMemories 只更新保留字段，不改正文、业务版本或更新时间
func (m *Manager) ReinforceMemories(ctx context.Context, ids []uint, now time.Time) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	policy := CurrentRetentionPolicy()
	count := 0
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows := make([]KnowledgeRecord, 0, len(ids))
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id IN ? AND status='active' AND (pinned OR forget_at IS NULL OR forget_at>?)", ids, now).
			Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			halfLife := policy.ReinforcedHalfLife(row.StabilityDays, row.RecallAnchorAt, now)
			updates := map[string]any{"stability_days": halfLife, "recall_anchor_at": now}
			if !row.Pinned {
				updates["forget_at"] = policy.ForgetAt(now, halfLife)
			}
			if err := tx.Model(&KnowledgeRecord{}).Where("id=?", row.ID).UpdateColumns(updates).Error; err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

// ReinforceTopics 只更新保留字段，不改状态、摘要版本或更新时间
func (m *Manager) ReinforceTopics(ctx context.Context, ids []uint, now time.Time) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	policy := CurrentRetentionPolicy()
	count := 0
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows := make([]TopicThreadRow, 0, len(ids))
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id IN ? AND forgotten_at IS NULL AND forget_at>?", ids, now).
			Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			halfLife := policy.ReinforcedHalfLife(row.StabilityDays, row.RecallAnchorAt, now)
			if err := tx.Model(&TopicThreadRow{}).Where("id=?", row.ID).UpdateColumns(map[string]any{
				"stability_days": halfLife, "recall_anchor_at": now,
				"forget_at": policy.ForgetAt(now, halfLife),
			}).Error; err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

// RefreshTopicActivity 用真实来源事件时间推进话题锚点；更旧的来源不会把锚点拉回过去
func RefreshTopicActivity(topic *TopicThreadRow, latestSourceAt time.Time, now time.Time, policy RetentionPolicy) {
	if topic.StabilityDays <= 0 {
		topic.StabilityDays = policy.InitialHalfLifeDays
	}
	if topic.RecallAnchorAt.IsZero() || latestSourceAt.After(topic.RecallAnchorAt) {
		topic.RecallAnchorAt = latestSourceAt
	}
	if topic.RecallAnchorAt.IsZero() || topic.RecallAnchorAt.After(now) {
		topic.RecallAnchorAt = now
	}
	if topic.StabilityDays > policy.MaxHalfLifeDays {
		topic.StabilityDays = policy.MaxHalfLifeDays
	}
	topic.ForgetAt = policy.ForgetAt(topic.RecallAnchorAt, topic.StabilityDays)
}
