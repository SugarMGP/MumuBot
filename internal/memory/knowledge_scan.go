package memory

import (
	"context"
	"fmt"
)

func (m *Manager) KnowledgeScanCursor(ctx context.Context, groupID int64) (uint, error) {
	var id uint
	err := m.db.WithContext(ctx).Raw("SELECT COALESCE((SELECT last_message_log_id FROM learning_states WHERE group_id = ?),0)", groupID).Scan(&id).Error
	return id, err
}

func (m *Manager) KnowledgeScanBatch(ctx context.Context, groupID int64, after uint, limit int) ([]MessageLog, error) {
	if groupID <= 0 || limit <= 0 || limit > 100 {
		return nil, fmt.Errorf("无效调查批次范围")
	}
	var rows []MessageLog
	err := m.db.WithContext(ctx).Where("group_id=? AND id>?", groupID, after).Order("id").Limit(limit).Find(&rows).Error
	return rows, err
}

func (m *Manager) KnowledgeReviewCandidates(ctx context.Context, groupID int64, upper uint, limit int) ([]KnowledgeItem, error) {
	var rows []KnowledgeItem
	err := m.db.WithContext(ctx).Where("group_id=? AND (status='candidate' OR (status='active' AND EXISTS(SELECT 1 FROM knowledge_relations kr WHERE kr.status='candidate' AND (kr.source_item_id=knowledge_items.id OR kr.target_item_id=knowledge_items.id)))) AND reviewed_through_id<?", groupID, upper).
		Where("NOT EXISTS(SELECT 1 FROM knowledge_evidence_sets es JOIN knowledge_evidence_messages em ON em.evidence_set_id=es.id WHERE es.item_id=knowledge_items.id AND em.message_log_id>?)", upper).
		Order("reviewed_through_id ASC,id ASC").Limit(min(limit, 10)).Find(&rows).Error
	return rows, err
}
