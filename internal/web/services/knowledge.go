package services

import (
	"context"
	"strings"

	"mumu-bot/internal/memory"

	"gorm.io/gorm"
)

type KnowledgeFilter struct {
	MemoryFilter
	UserID, AuthorID int64
}

const knowledgeParticipationSQL = `EXISTS (
 SELECT 1 FROM knowledge_evidence_sets es
 JOIN knowledge_evidence_messages em ON em.evidence_set_id=es.id
 JOIN message_logs ml ON ml.id=em.message_log_id
 LEFT JOIN knowledge_relations kr ON kr.id=es.relation_id
 WHERE (es.item_id=ki.id OR kr.source_item_id=ki.id OR kr.target_item_id=ki.id)
 AND ml.user_id=? AND ` + memory.KnowledgeEvidenceSetValiditySQL + `
)`

type KnowledgeDetail struct {
	Page              int
	HasMore           bool
	SensesHasMore     bool
	RelationsHaveMore bool
	Item              memory.KnowledgeItem
	Evidence          []memory.KnowledgeEvidence
	Relations         []KnowledgeRelationView
	Topics            map[uint]uint
	Senses            []memory.KnowledgeItem
}
type KnowledgeRelationView struct {
	memory.KnowledgeRelation
	Source, Target memory.KnowledgeItem
}

func (s *AdminService) ListKnowledge(f KnowledgeFilter) (Page[memory.KnowledgeItem], error) {
	var items []memory.KnowledgeItem
	q := s.filterKnowledge(s.db.Table("knowledge_items ki"), f)
	q = order(q, f.Sort, f.Order, map[string]string{"updated": "ki.updated_at", "created": "ki.created_at", "default": "ki.updated_at"})
	return paginate(q, f.Page, f.PageSize, &items)
}

func (s *AdminService) filterKnowledge(q *gorm.DB, f KnowledgeFilter) *gorm.DB {
	if f.GroupID > 0 {
		q = q.Where("ki.group_id=?", f.GroupID)
	}
	if f.UserID > 0 {
		q = q.Where("ki.subject_user_id=?", f.UserID)
	}
	if f.AuthorID > 0 {
		q = q.Where(knowledgeParticipationSQL, f.AuthorID)
	}
	if f.Kind != "" {
		q = q.Where("ki.kind=?", f.Kind)
	}
	if f.Status != "" && f.Status != "all" {
		q = q.Where("ki.status=?", f.Status)
	}
	if k := strings.TrimSpace(f.Keyword); k != "" {
		q = q.Where("ki.label ILIKE ? OR ki.content ILIKE ?", "%"+k+"%", "%"+k+"%")
	}
	return q
}

type KnowledgeMetadata struct {
	ID           uint
	Name         string
	Total, Valid int64
}

func (s *AdminService) KnowledgeMetadata(ctx context.Context, items []memory.KnowledgeItem) (map[uint]KnowledgeMetadata, error) {
	out := map[uint]KnowledgeMetadata{}
	if len(items) == 0 {
		return out, nil
	}
	ids := make([]uint, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	var rows []KnowledgeMetadata
	err := s.db.WithContext(ctx).Table("knowledge_items ki").Select(`ki.id,
	 COALESCE(NULLIF(btrim(mp.nickname),''),(SELECT mn.value FROM member_names mn WHERE mn.user_id=ki.subject_user_id AND btrim(mn.value)<>'' ORDER BY (mn.group_id=ki.group_id) DESC,mn.updated_at DESC LIMIT 1),'') name,
	 (SELECT count(*) FROM knowledge_evidence_sets es WHERE es.item_id=ki.id) total,
	 (SELECT count(*) FROM knowledge_evidence_sets es WHERE es.item_id=ki.id AND `+memory.KnowledgeEvidenceSetValiditySQL+`) valid`).
		Joins("LEFT JOIN member_profiles mp ON mp.user_id=ki.subject_user_id").Where("ki.id IN ?", ids).Scan(&rows).Error
	for _, row := range rows {
		out[row.ID] = row
	}
	return out, err
}

func (s *AdminService) GetKnowledge(id uint) (memory.KnowledgeItem, error) {
	var item memory.KnowledgeItem
	err := s.db.First(&item, id).Error
	return item, err
}

func (s *AdminService) KnowledgeDetail(ctx context.Context, id uint, page int) (KnowledgeDetail, error) {
	d := KnowledgeDetail{Page: max(1, page)}
	offset := (d.Page - 1) * 5
	item, err := s.GetKnowledge(id)
	if err != nil {
		return d, err
	}
	d.Item = item
	d.Evidence, d.HasMore, err = s.memory.ListKnowledgeEvidencePage(ctx, item.GroupID, id, 0, 0, offset, 5)
	if err != nil {
		return d, err
	}
	var relations []memory.KnowledgeRelation
	if err = s.db.Where("source_item_id=? OR target_item_id=?", id, id).Order("id").Limit(31).Find(&relations).Error; err != nil {
		return d, err
	}
	relationsHaveMore := len(relations) > 30
	if relationsHaveMore {
		relations = relations[:30]
	}
	endpointIDs := []uint{id}
	for _, rel := range relations {
		endpointIDs = append(endpointIDs, rel.SourceItemID, rel.TargetItemID)
	}
	var endpoints []memory.KnowledgeItem
	if err = s.db.Where("group_id=? AND id IN ?", item.GroupID, endpointIDs).Order("id").Find(&endpoints).Error; err != nil {
		return d, err
	}
	byID := map[uint]memory.KnowledgeItem{}
	for _, endpoint := range endpoints {
		byID[endpoint.ID] = endpoint
	}
	for _, rel := range relations {
		d.Relations = append(d.Relations, KnowledgeRelationView{KnowledgeRelation: rel, Source: byID[rel.SourceItemID], Target: byID[rel.TargetItemID]})
	}
	if item.Kind == "term" {
		err = s.db.Where("group_id=? AND kind='term' AND label=? AND id<>?", item.GroupID, item.Label, id).Order("id").Limit(31).Find(&d.Senses).Error
		if err != nil {
			return d, err
		}
	}
	d.SensesHasMore = len(d.Senses) > 30
	if d.SensesHasMore {
		d.Senses = d.Senses[:30]
	}
	d.RelationsHaveMore = relationsHaveMore
	ids := []uint{}
	for _, set := range d.Evidence {
		for _, msg := range set.Messages {
			ids = append(ids, msg.ID)
		}
	}
	d.Topics = map[uint]uint{}
	if len(ids) > 0 {
		var rows []memory.TopicAssignment
		if err = s.db.Where("message_log_id IN ?", ids).Find(&rows).Error; err != nil {
			return d, err
		}
		for _, row := range rows {
			if row.TopicID != nil {
				d.Topics[row.MessageLogID] = *row.TopicID
			}
		}
	}
	return d, nil
}

func (s *AdminService) UpdateKnowledgeStatus(ctx context.Context, id uint, status string) error {
	item, err := s.GetKnowledge(id)
	if err != nil {
		return err
	}
	return s.memory.SetKnowledgeStatus(ctx, item.GroupID, id, strings.TrimSpace(status))
}
func (s *AdminService) UpdateRelationStatus(ctx context.Context, id uint, status string) error {
	var rel memory.KnowledgeRelation
	if err := s.db.First(&rel, id).Error; err != nil {
		return err
	}
	item, err := s.GetKnowledge(rel.SourceItemID)
	if err != nil {
		return err
	}
	return s.memory.SetKnowledgeRelationStatus(ctx, item.GroupID, id, strings.TrimSpace(status))
}
