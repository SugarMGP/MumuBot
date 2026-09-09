package memory

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A proof remains usable only while every required source message remains usable.
const validKnowledgeSetSQL = `(SELECT count(*) FROM knowledge_evidence_messages em WHERE em.evidence_set_id=es.id) BETWEEN 1 AND 16
 AND NOT EXISTS(SELECT 1 FROM knowledge_evidence_messages em JOIN message_logs ml ON ml.id=em.message_log_id WHERE em.evidence_set_id=es.id AND (ml.recalled_at IS NOT NULL OR btrim(ml.text_content)=''))`

func knowledgeEvidenceSQL(column string) string {
	return `EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.` + column + `=ki.id AND ` + validKnowledgeSetSQL + `)`
}
func knowledgeHasEvidence(tx *gorm.DB, itemID, relationID uint) (bool, error) {
	var count int64
	query := tx.Table("knowledge_evidence_sets es").Where(validKnowledgeSetSQL)
	if itemID != 0 {
		query = query.Where("es.item_id=?", itemID)
	} else {
		query = query.Where("es.relation_id=?", relationID)
	}
	err := query.Count(&count).Error
	return count > 0, err
}

func lockKnowledgeEvidence(tx *gorm.DB, batch KnowledgeBatch) (map[uint]MessageLog, error) {
	read := map[uint]bool{}
	for _, id := range batch.ReadMessageIDs {
		read[id] = true
	}
	wanted := map[uint]bool{}
	collect := func(sets [][]uint) error {
		for _, set := range sets {
			if len(set) < 1 || len(set) > 16 {
				return fmt.Errorf("evidence group must contain 1 to 16 messages")
			}
			for _, id := range set {
				if !read[id] {
					return fmt.Errorf("evidence message %d was not read", id)
				}
				wanted[id] = true
			}
		}
		return nil
	}
	for _, item := range batch.Items {
		if err := collect(item.EvidenceSets); err != nil {
			return nil, err
		}
	}
	for _, relation := range batch.Relations {
		if err := collect(relation.EvidenceSets); err != nil {
			return nil, err
		}
	}
	ids := make([]uint, 0, len(wanted))
	for id := range wanted {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := map[uint]MessageLog{}
	if len(ids) == 0 {
		return result, nil
	}
	var rows []MessageLog
	q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("group_id=? AND id IN ? AND id<=? AND recalled_at IS NULL AND btrim(text_content)<>''", batch.GroupID, ids, batch.ThroughID)
	if batch.RequireAssigned {
		q = q.Where("EXISTS(SELECT 1 FROM topic_assignments ta WHERE ta.message_log_id=message_logs.id)")
	}
	if err := q.Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) != len(ids) {
		return nil, fmt.Errorf("evidence has changed or is outside the snapshot")
	}
	for _, row := range rows {
		result[row.ID] = row
	}
	return result, nil
}

func knowledgeNeedsHuman(subject, selfID int64, kind string) bool {
	return subject != selfID || kind == "term" || kind == "expression" || kind == "alias"
}

func validateKnowledgeSubject(subject, selfID int64, kind string, ids []uint, messages map[uint]MessageLog) error {
	if !knowledgeNeedsHuman(subject, selfID, kind) && selfID > 0 {
		return nil
	}
	for _, id := range ids {
		message := messages[id]
		if message.UserID > 0 && message.UserID != selfID && (subject == 0 || subject == selfID || message.UserID == subject) {
			return nil
		}
	}
	return fmt.Errorf("knowledge requires independent member evidence for its subject; bot messages alone are not sufficient")
}

func saveKnowledgeEvidence(tx *gorm.DB, itemID, relationID *uint, ids []uint) error {
	if len(ids) < 1 || len(ids) > 16 {
		return fmt.Errorf("invalid evidence group size")
	}
	sorted := append([]uint(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for i, id := range sorted {
		if id == 0 || (i > 0 && id == sorted[i-1]) {
			return fmt.Errorf("duplicate or invalid evidence message")
		}
	}
	q := tx.Model(&KnowledgeEvidenceSet{})
	if itemID != nil {
		q = q.Where("item_id=?", *itemID)
	} else {
		q = q.Where("relation_id=?", *relationID)
	}
	var sets []KnowledgeEvidenceSet
	if err := q.Find(&sets).Error; err != nil {
		return err
	}
	for _, set := range sets {
		var existing []uint
		if err := tx.Model(&KnowledgeEvidenceMessage{}).Where("evidence_set_id=?", set.ID).Order("message_log_id").Pluck("message_log_id", &existing).Error; err != nil {
			return err
		}
		if slices.Equal(existing, sorted) {
			return nil
		}
	}
	set := KnowledgeEvidenceSet{ItemID: itemID, RelationID: relationID}
	if err := tx.Create(&set).Error; err != nil {
		return err
	}
	rows := make([]KnowledgeEvidenceMessage, len(sorted))
	for i, id := range sorted {
		rows[i] = KnowledgeEvidenceMessage{EvidenceSetID: set.ID, MessageLogID: id}
	}
	return tx.Create(&rows).Error
}

func (m *Manager) ListKnowledgeEvidence(ctx context.Context, groupID int64, itemID, relationID uint) ([]KnowledgeEvidence, error) {
	if groupID <= 0 || (itemID == 0) == (relationID == 0) {
		return nil, fmt.Errorf("invalid evidence target")
	}
	q := m.db.WithContext(ctx).Table("knowledge_evidence_sets es").Select("es.*")
	if itemID != 0 {
		q = q.Joins("JOIN knowledge_items ki ON ki.id=es.item_id").Where("ki.group_id=? AND ki.id=?", groupID, itemID)
	} else {
		q = q.Joins("JOIN knowledge_relations kr ON kr.id=es.relation_id JOIN knowledge_items ki ON ki.id=kr.source_item_id").Where("ki.group_id=? AND kr.id=?", groupID, relationID)
	}
	var sets []KnowledgeEvidenceSet
	if err := q.Order("es.id").Scan(&sets).Error; err != nil {
		return nil, err
	}
	result := make([]KnowledgeEvidence, 0, len(sets))
	for _, set := range sets {
		var rows []MessageLog
		if err := m.db.WithContext(ctx).Table("message_logs ml").Select("ml.*").Joins("JOIN knowledge_evidence_messages em ON em.message_log_id=ml.id").Where("em.evidence_set_id=? AND ml.group_id=?", set.ID, groupID).Order("ml.message_time,ml.id").Scan(&rows).Error; err != nil {
			return nil, err
		}
		valid := len(rows) > 0 && len(rows) <= 16
		for i := range rows {
			if rows[i].RecalledAt != nil || strings.TrimSpace(rows[i].TextContent) == "" {
				valid = false
				rows[i].TextContent = ""
				rows[i].DisplayContent = RecalledMessageDisplayContent
			}
		}
		result = append(result, KnowledgeEvidence{ID: set.ID, Messages: rows, Valid: valid})
	}
	return result, nil
}

// InvalidateKnowledgeEvidence runs inside the caller's same-group recall transaction.
func InvalidateKnowledgeEvidence(tx *gorm.DB, groupID int64) error {
	if err := tx.Exec(`UPDATE knowledge_items ki SET status='candidate',reviewed_through_id=0,updated_at=now() WHERE ki.group_id=? AND ki.status='active' AND NOT (`+knowledgeEvidenceSQL("item_id")+`)`, groupID).Error; err != nil {
		return err
	}
	return tx.Exec(`WITH changed AS (
		UPDATE knowledge_relations kr SET status='candidate' WHERE kr.status='active' AND EXISTS(SELECT 1 FROM knowledge_items ki WHERE ki.id=kr.source_item_id AND ki.group_id=?) AND (NOT EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.relation_id=kr.id AND `+validKnowledgeSetSQL+`) OR EXISTS(SELECT 1 FROM knowledge_items ki WHERE ki.id IN(kr.source_item_id,kr.target_item_id) AND ki.status='candidate')) RETURNING source_item_id,target_item_id
	) UPDATE knowledge_items SET reviewed_through_id=0,updated_at=now() WHERE id IN(SELECT source_item_id FROM changed UNION SELECT target_item_id FROM changed)`, groupID).Error
}
