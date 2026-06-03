package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

func (m *Manager) SaveStyleCardCandidate(ctx context.Context, card *StyleCard) (bool, error) {
	if card == nil {
		return false, nil
	}
	if m.embedding == nil || m.styleCardMilvus == nil {
		return false, fmt.Errorf("风格卡片向量依赖未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	card.Intent = strings.TrimSpace(card.Intent)
	card.Tone = strings.TrimSpace(card.Tone)
	card.TriggerRule = strings.TrimSpace(card.TriggerRule)
	card.AvoidRule = strings.TrimSpace(card.AvoidRule)
	card.Example = strings.TrimSpace(card.Example)
	card.SourceExcerpt = strings.TrimSpace(card.SourceExcerpt)

	if !IsValidStyleIntent(card.Intent) || !IsValidStyleTone(card.Tone) {
		return false, fmt.Errorf("非法的风格标签")
	}
	if card.TriggerRule == "" || card.AvoidRule == "" || card.Example == "" {
		return false, fmt.Errorf("风格卡片缺少必填字段")
	}

	queryText := styleCardVectorText(card)
	embedding, err := m.embedding.Embed(ctx, queryText)
	if err != nil {
		return false, err
	}

	var existing StyleCard
	searchResults, err := m.styleCardMilvus.Search(
		ctx,
		embedding,
		card.GroupID,
		styleCardVectorKey(card.Intent, card.Tone),
		3,
		0.92,
	)
	if err != nil {
		return false, err
	}
	for _, result := range searchResults {
		if err := m.db.First(&existing, result.MemoryID).Error; err == nil {
			break
		}
	}
	if existing.ID != 0 {
		updates := map[string]any{
			"evidence_count": gorm.Expr("evidence_count + 1"),
			"updated_at":     time.Now(),
		}
		if nextStatus := styleCardStatusOnNewEvidence(existing.Status); nextStatus != existing.Status {
			updates["status"] = nextStatus
		}
		if shouldPreferLongerText(existing.TriggerRule, card.TriggerRule) {
			updates["trigger_rule"] = card.TriggerRule
		}
		if shouldPreferLongerText(existing.AvoidRule, card.AvoidRule) {
			updates["avoid_rule"] = card.AvoidRule
		}
		if shouldPreferShorterText(existing.Example, card.Example) {
			updates["example"] = card.Example
		}
		if mergedExcerpt := mergeStyleCardSourceExcerpt(existing.SourceExcerpt, card.SourceExcerpt); mergedExcerpt != strings.TrimSpace(existing.SourceExcerpt) {
			updates["source_excerpt"] = mergedExcerpt
		}
		if err := m.db.WithContext(ctx).Model(&existing).Updates(updates).Error; err != nil {
			return false, err
		}
		if err := m.db.WithContext(ctx).First(&existing, existing.ID).Error; err == nil {
			if err := m.refreshStyleCardVector(ctx, &existing); err != nil {
				return false, err
			}
		}
		return false, nil
	}

	if card.Status == "" {
		card.Status = StyleCardStatusCandidate
	}
	if card.EvidenceCount <= 0 {
		card.EvidenceCount = 1
	}
	if err := m.db.Create(card).Error; err != nil {
		return false, err
	}
	if err := m.insertStyleCardVector(ctx, card, embedding); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) SearchStyleCards(groupID int64, keyword string, limit int) ([]StyleCard, error) {
	var cards []StyleCard
	q := m.db.Model(&StyleCard{}).Where("status = ?", StyleCardStatusActive)
	if strings.TrimSpace(keyword) != "" {
		keywords := strings.Fields(keyword)
		likeConditions := make([]string, 0, len(keywords))
		args := make([]interface{}, 0, len(keywords)*4)
		for _, kw := range keywords {
			likeConditions = append(likeConditions, "trigger_rule LIKE ? OR avoid_rule LIKE ? OR example LIKE ? OR source_excerpt LIKE ?")
			pattern := "%" + kw + "%"
			args = append(args, pattern, pattern, pattern, pattern)
		}
		q = q.Where(strings.Join(likeConditions, " OR "), args...)
	}
	if limit <= 0 {
		limit = 10
	}

	orderGroup := fmt.Sprintf("CASE WHEN group_id = %d THEN 0 ELSE 1 END", groupID)
	err := q.Order(orderGroup).
		Order("evidence_count DESC").
		Order("use_count DESC").
		Order("updated_at DESC").
		Limit(limit).
		Find(&cards).Error
	return cards, err
}

func (m *Manager) ListUncheckedStyleCards(groupID int64, limit int) ([]StyleCard, error) {
	var cards []StyleCard
	q := m.db.Where("status = ?", StyleCardStatusCandidate)
	if groupID != 0 {
		q = q.Where("group_id = ?", groupID)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Order("updated_at DESC").Find(&cards).Error
	return cards, err
}

func (m *Manager) ReviewStyleCards(ids []uint, approve bool) error {
	if len(ids) == 0 {
		return nil
	}

	var cards []StyleCard
	if err := m.db.Where("id IN ?", ids).Find(&cards).Error; err != nil {
		return err
	}

	for _, card := range cards {
		nextStatus := StyleCardStatusRejected
		if approve {
			nextStatus = StyleCardStatusCandidate
			if card.EvidenceCount >= 2 {
				nextStatus = StyleCardStatusActive
			}
		}
		if err := m.db.Model(&StyleCard{}).Where("id = ?", card.ID).Updates(map[string]any{
			"status":     nextStatus,
			"updated_at": time.Now(),
		}).Error; err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) ListActiveStyleCardsByIntent(intent string, groupID int64, tone string, limit int) ([]StyleCard, error) {
	var cards []StyleCard
	if limit <= 0 {
		limit = 3
	}

	orderGroup := fmt.Sprintf("CASE WHEN group_id = %d THEN 0 ELSE 1 END", groupID)
	escapedTone := strings.ReplaceAll(tone, "'", "''")
	orderTone := fmt.Sprintf("CASE WHEN tone = '%s' THEN 0 ELSE 1 END", escapedTone)

	err := m.db.Model(&StyleCard{}).
		Where("status = ? AND intent = ?", StyleCardStatusActive, strings.TrimSpace(intent)).
		Order(orderGroup).
		Order(orderTone).
		Order("evidence_count DESC").
		Order("use_count DESC").
		Order("updated_at DESC").
		Limit(limit).
		Find(&cards).Error
	return cards, err
}

func (m *Manager) IncrementStyleCardUsage(ids []uint) error {
	if len(ids) == 0 {
		return nil
	}

	now := time.Now()
	return m.db.Model(&StyleCard{}).
		Where("id IN ?", ids).
		Updates(map[string]any{
			"use_count":    gorm.Expr("use_count + 1"),
			"last_used_at": &now,
		}).Error
}

func styleCardCollectionName(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "mumu_memories"
	}
	return base + "_style_cards"
}

func topicSummaryCollectionName(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "mumu_memories"
	}
	return base + "_topic_summaries"
}

func styleCardStatusOnNewEvidence(status StyleCardStatus) StyleCardStatus {
	if status == StyleCardStatusRejected {
		return StyleCardStatusCandidate
	}
	return status
}

func styleCardVectorKey(intent, tone string) string {
	return strings.TrimSpace(intent) + "|" + strings.TrimSpace(tone)
}

func styleCardVectorText(card *StyleCard) string {
	if card == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf(
		"intent:%s\ntone:%s\ntrigger:%s\navoid:%s\nexample:%s",
		card.Intent,
		card.Tone,
		card.TriggerRule,
		card.AvoidRule,
		card.Example,
	))
}

func (m *Manager) insertStyleCardVector(ctx context.Context, card *StyleCard, embedding []float64) error {
	if card == nil || m.styleCardMilvus == nil {
		return nil
	}
	if _, err := m.styleCardMilvus.Insert(ctx, card.ID, card.GroupID, styleCardVectorKey(card.Intent, card.Tone), embedding); err != nil {
		return fmt.Errorf("插入风格卡片向量失败: %w", err)
	}
	return nil
}

func (m *Manager) refreshStyleCardVector(ctx context.Context, card *StyleCard) error {
	if card == nil || m.embedding == nil || m.styleCardMilvus == nil {
		return nil
	}
	if err := m.styleCardMilvus.Delete(ctx, []uint{card.ID}); err != nil {
		return fmt.Errorf("删除风格卡片旧向量失败: %w", err)
	}
	embedding, err := m.embedding.Embed(ctx, styleCardVectorText(card))
	if err != nil {
		return err
	}
	return m.insertStyleCardVector(ctx, card, embedding)
}

func shouldPreferShorterText(existing, candidate string) bool {
	existing = strings.TrimSpace(existing)
	candidate = strings.TrimSpace(candidate)
	switch {
	case candidate == "":
		return false
	case existing == "":
		return true
	default:
		return len([]rune(candidate)) < len([]rune(existing))
	}
}

func shouldPreferLongerText(existing, candidate string) bool {
	existing = strings.TrimSpace(existing)
	candidate = strings.TrimSpace(candidate)
	switch {
	case candidate == "":
		return false
	case existing == "":
		return true
	default:
		return len([]rune(candidate)) > len([]rune(existing))
	}
}

func mergeStyleCardSourceExcerpt(existing, candidate string) string {
	var parts []string
	if strings.TrimSpace(existing) != "" {
		items := strings.Split(existing, "|")
		parts = make([]string, 0, len(items))
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			parts = append(parts, item)
		}
	}
	candidate = strings.TrimSpace(candidate)
	if candidate != "" {
		parts = append(parts, candidate)
	}
	if len(parts) > 3 {
		parts = parts[len(parts)-3:]
	}
	return strings.Join(parts, "|")
}
