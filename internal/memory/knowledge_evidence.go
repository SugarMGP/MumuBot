package memory

import (
	"context"
	"slices"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// KnowledgeEvidenceSetValiditySQL 匹配仍能站住的依据组：组内只要有未被撤回的原文就算有效，整组原文全部撤回才失效
const KnowledgeEvidenceSetValiditySQL = "(SELECT count(*) FROM knowledge_evidence_messages em WHERE em.evidence_set_id=es.id) BETWEEN 1 AND 16\n AND EXISTS(SELECT 1 FROM knowledge_evidence_messages em JOIN message_logs ml ON ml.id=em.message_log_id WHERE em.evidence_set_id=es.id AND ml.recalled_at IS NULL AND " + OriginalMessageTextSQL + "<>'')"

const knowledgeItemEvidenceSQL = `EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.item_id=ki.id AND ` + KnowledgeEvidenceSetValiditySQL + `)`

func knowledgeHasEvidence(tx *gorm.DB, itemID, relationID uint) (bool, error) {
	var count int64
	query := tx.Table("knowledge_evidence_sets es").Where(KnowledgeEvidenceSetValiditySQL)
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
				return invalidKnowledge("每组依据必须包含 1 至 16 条原文，请调整后重试")
			}
			for _, id := range set {
				if !read[id] {
					return invalidKnowledge("原文 %d 尚未读取，请先用 readContext 完整读取后重试", id)
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
	q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_kind=? AND target_id=? AND id IN ? AND id<=? AND recalled_at IS NULL AND "+originalMessageTextSQL+"<>''", batch.ConversationKind, batch.TargetID, ids, batch.ThroughID)
	if batch.RequireAssigned {
		q = q.Where("EXISTS(SELECT 1 FROM topic_assignments ta WHERE ta.message_log_id=message_logs.id)")
	}
	if err := q.Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) != len(ids) {
		return nil, ErrSnapshotChanged
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
	return invalidKnowledge("该主体需要至少一条非机器人原文作为独立依据，请补充证据或放弃提交")
}

func saveKnowledgeEvidence(tx *gorm.DB, itemID, relationID *uint, ids []uint) error {
	if len(ids) < 1 || len(ids) > 16 {
		return invalidKnowledge("每组依据必须包含 1 至 16 条原文，请调整后重试")
	}
	sorted := append([]uint(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for i, id := range sorted {
		if id == 0 || (i > 0 && id == sorted[i-1]) {
			return invalidKnowledge("依据中包含重复或无效的原文 ID，请去重并移除 0 后重试")
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

func (m *Manager) ListKnowledgeEvidenceScope(ctx context.Context, kind string, targetID int64, itemID, relationID uint) ([]KnowledgeEvidence, error) {
	if targetID <= 0 || (itemID == 0) == (relationID == 0) {
		return nil, invalidKnowledge("依据目标必须且只能指定一个知识 ID 或关系 ID，请修正参数后重试")
	}
	q := m.db.WithContext(ctx).Table("knowledge_evidence_sets es").Select("es.*")
	if itemID != 0 {
		q = q.Joins("JOIN knowledge_items ki ON ki.id=es.item_id").Where("ki.conversation_kind=? AND ki.target_id=? AND ki.id=?", kind, targetID, itemID)
	} else {
		q = q.Joins("JOIN knowledge_relations kr ON kr.id=es.relation_id JOIN knowledge_items ks ON ks.id=kr.source_item_id JOIN knowledge_items kt ON kt.id=kr.target_item_id").Where("ks.conversation_kind=? AND ks.target_id=? AND kt.conversation_kind=? AND kt.target_id=? AND kr.id=?", kind, targetID, kind, targetID, relationID)
	}
	var sets []KnowledgeEvidenceSet
	if err := q.Order("es.id").Scan(&sets).Error; err != nil {
		return nil, err
	}
	return m.loadEvidenceMessagesScope(ctx, kind, targetID, sets)
}

func (m *Manager) loadEvidenceMessagesScope(ctx context.Context, kind string, targetID int64, sets []KnowledgeEvidenceSet) ([]KnowledgeEvidence, error) {
	ids := make([]uint, 0, len(sets))
	for _, set := range sets {
		ids = append(ids, set.ID)
	}
	var rows []struct {
		MessageLog
		EvidenceSetID uint
	}
	if len(ids) > 0 {
		if err := m.db.WithContext(ctx).Table("message_logs ml").Select("ml.*,em.evidence_set_id").Joins("JOIN knowledge_evidence_messages em ON em.message_log_id=ml.id").Where("em.evidence_set_id IN ? AND ml.conversation_kind=? AND ml.target_id=?", ids, kind, targetID).Order("ml.message_time,ml.id").Scan(&rows).Error; err != nil {
			return nil, err
		}
	}
	bySet := map[uint][]MessageLog{}
	for _, row := range rows {
		bySet[row.EvidenceSetID] = append(bySet[row.EvidenceSetID], row.MessageLog)
	}
	result := make([]KnowledgeEvidence, 0, len(sets))
	for _, set := range sets {
		messages := bySet[set.ID]
		valid := false
		for i := range messages {
			if messages[i].RecalledAt != nil {
				messages[i].TextContent = ""
				messages[i].DisplayContent = RecalledMessageDisplayContent
			} else if strings.TrimSpace(messages[i].TextContent) != "" {
				valid = true
			}
		}
		result = append(result, KnowledgeEvidence{ID: set.ID, Messages: messages, Valid: valid && len(messages) <= 16})
	}
	return result, nil
}

// ListKnowledgeEvidencePageScope 为后台依据面板按组分页，失效组也保留展示
func (m *Manager) ListKnowledgeEvidencePageScope(ctx context.Context, kind string, targetID int64, itemID, relationID uint, offset, limit int) ([]KnowledgeEvidence, bool, error) {
	if targetID <= 0 || (itemID == 0) == (relationID == 0) || offset < 0 || limit < 1 || limit > 30 {
		return nil, false, invalidKnowledge("无效依据分页")
	}
	if kind == "" {
		kind = ConversationKindGroup
	}
	q := m.db.WithContext(ctx).Table("knowledge_evidence_sets es").Select("es.*")
	if itemID > 0 {
		q = q.Joins("JOIN knowledge_items ki ON ki.id=es.item_id").Where("ki.conversation_kind=? AND ki.target_id=? AND ki.id=?", kind, targetID, itemID)
	} else {
		q = q.Joins("JOIN knowledge_relations kr ON kr.id=es.relation_id JOIN knowledge_items ks ON ks.id=kr.source_item_id JOIN knowledge_items kt ON kt.id=kr.target_item_id").Where("ks.conversation_kind=? AND ks.target_id=? AND kt.conversation_kind=? AND kt.target_id=? AND kr.id=?", kind, targetID, kind, targetID, relationID)
	}
	var sets []KnowledgeEvidenceSet
	if err := q.Order("es.id").Offset(offset).Limit(limit + 1).Scan(&sets).Error; err != nil {
		return nil, false, err
	}
	more := len(sets) > limit
	if more {
		sets = sets[:limit]
	}
	items, err := m.loadEvidenceMessagesScope(ctx, kind, targetID, sets)
	return items, more, err
}

// InvalidateKnowledgeEvidenceScope 在调用方的消息撤回事务中按会话作用域使相关证据失效
func InvalidateKnowledgeEvidenceScope(tx *gorm.DB, kind string, targetID int64) error {
	if err := tx.Exec(`UPDATE knowledge_items ki SET status='archived',updated_at=now() WHERE ki.conversation_kind=? AND ki.target_id=? AND ki.status='active' AND NOT (`+knowledgeItemEvidenceSQL+`)`, kind, targetID).Error; err != nil {
		return err
	}
	return ArchiveInvalidKnowledgeRelations(tx, kind, targetID)
}

// ArchiveInvalidKnowledgeRelations 统一收口失效关系；替代关系的旧端归档是正常状态
func ArchiveInvalidKnowledgeRelations(tx *gorm.DB, kind string, targetID int64) error {
	return tx.Exec(`WITH changed AS (
		UPDATE knowledge_relations kr SET status='archived'
		FROM knowledge_items s,knowledge_items t
		WHERE kr.source_item_id=s.id AND kr.target_item_id=t.id AND s.conversation_kind=? AND s.target_id=? AND t.conversation_kind=? AND t.target_id=? AND kr.status='active'
		AND (s.status<>'active' OR (t.status<>'active' AND kr.kind<>'supersedes')
		 OR NOT EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.relation_id=kr.id AND `+KnowledgeEvidenceSetValiditySQL+`)
		 OR EXISTS(SELECT 1 FROM knowledge_items ki WHERE ki.id IN(s.id,t.id) AND NOT (`+knowledgeItemEvidenceSQL+`)))
		RETURNING kr.source_item_id,kr.target_item_id
	) UPDATE knowledge_items SET updated_at=now() WHERE id IN(SELECT source_item_id FROM changed UNION SELECT target_item_id FROM changed)`, kind, targetID, kind, targetID).Error
}
