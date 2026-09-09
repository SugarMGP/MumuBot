package services

import (
	"context"
	"strings"

	"mumu-bot/internal/memory"
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
 WHERE (es.item_id=knowledge_items.id OR kr.source_item_id=knowledge_items.id OR kr.target_item_id=knowledge_items.id)
 AND ml.user_id=? AND ml.recalled_at IS NULL
)`

type KnowledgeDetail struct {
	Item      memory.KnowledgeItem
	Graph     memory.KnowledgeGraph
	Evidence  []memory.KnowledgeEvidence
	Relations []KnowledgeRelationView
	Topics    map[uint]uint
	Senses    []memory.KnowledgeItem
}
type KnowledgeRelationView struct {
	memory.KnowledgeRelation
	Source, Target memory.KnowledgeItem
	Evidence       []memory.KnowledgeEvidence
}

func (s *AdminService) ListKnowledge(f KnowledgeFilter) (Page[memory.KnowledgeItem], error) {
	var items []memory.KnowledgeItem
	q := s.db.Model(&memory.KnowledgeItem{})
	if f.GroupID > 0 {
		q = q.Where("group_id=?", f.GroupID)
	}
	if f.UserID > 0 {
		q = q.Where("subject_user_id=?", f.UserID)
	}
	if f.AuthorID > 0 {
		q = q.Where(knowledgeParticipationSQL, f.AuthorID)
	}
	selfID := int64(0)
	if s.selfID != nil {
		selfID = s.selfID()
	}
	switch f.Subject {
	case "group":
		q = q.Where("subject_user_id=0")
	case "self":
		q = q.Where("subject_user_id=? AND subject_user_id>0", selfID)
	case "member":
		q = q.Where("subject_user_id>0 AND subject_user_id<>?", selfID)
	}
	if f.Kind != "" {
		q = q.Where("kind=?", f.Kind)
	}
	if f.Status != "" {
		q = q.Where("status=?", f.Status)
	}
	if k := strings.TrimSpace(f.Keyword); k != "" {
		q = q.Where("label ILIKE ? OR content ILIKE ?", "%"+k+"%", "%"+k+"%")
	}
	q = order(q, f.Sort, f.Order, map[string]string{"updated": "updated_at", "created": "created_at", "default": "updated_at"})
	return paginate(q, f.Page, f.PageSize, &items)
}

func (s *AdminService) GetKnowledge(id uint) (memory.KnowledgeItem, error) {
	var item memory.KnowledgeItem
	err := s.db.First(&item, id).Error
	return item, err
}

func (s *AdminService) KnowledgeDetail(ctx context.Context, id uint) (KnowledgeDetail, error) {
	var d KnowledgeDetail
	item, err := s.GetKnowledge(id)
	if err != nil {
		return d, err
	}
	d.Item = item
	d.Evidence, err = s.memory.ListKnowledgeEvidence(ctx, item.GroupID, id, 0)
	if err != nil {
		return d, err
	}
	graph, err := s.memory.GetKnowledgeNeighborhood(ctx, item.GroupID, []uint{id}, 3, true, memory.KnowledgeGraphOptions{})
	if err != nil {
		return d, err
	}
	d.Graph = *graph
	byID := map[uint]memory.KnowledgeItem{}
	for _, v := range graph.Items {
		byID[v.ID] = v
	}
	var relations []memory.KnowledgeRelation
	if err = s.db.Where("source_item_id=? OR target_item_id=?", id, id).Order("id").Limit(30).Find(&relations).Error; err != nil {
		return d, err
	}
	for _, rel := range relations {
		src, ok := byID[rel.SourceItemID]
		if !ok {
			src, err = s.GetKnowledge(rel.SourceItemID)
			if err != nil {
				return d, err
			}
		}
		dst, ok := byID[rel.TargetItemID]
		if !ok {
			dst, err = s.GetKnowledge(rel.TargetItemID)
			if err != nil {
				return d, err
			}
		}
		evidence, e := s.memory.ListKnowledgeEvidence(ctx, item.GroupID, 0, rel.ID)
		if e != nil {
			return d, e
		}
		d.Relations = append(d.Relations, KnowledgeRelationView{KnowledgeRelation: rel, Source: src, Target: dst, Evidence: evidence})
	}
	if item.Kind == "term" {
		err = s.db.Where("group_id=? AND kind='term' AND label=? AND id<>?", item.GroupID, item.Label, id).Order("id").Limit(20).Find(&d.Senses).Error
		if err != nil {
			return d, err
		}
	}
	ids := []uint{}
	for _, set := range d.Evidence {
		for _, msg := range set.Messages {
			ids = append(ids, msg.ID)
		}
	}
	for _, rel := range d.Relations {
		for _, set := range rel.Evidence {
			for _, msg := range set.Messages {
				ids = append(ids, msg.ID)
			}
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
func (s *AdminService) WorkingNote(ctx context.Context, groupID int64) (*memory.GroupAgentState, error) {
	return s.memory.GetWorkingNote(ctx, groupID)
}
func (s *AdminService) ClearWorkingNote(ctx context.Context, groupID int64) error {
	return s.memory.SaveWorkingNote(ctx, groupID, "")
}
