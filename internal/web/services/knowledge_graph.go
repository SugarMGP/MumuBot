package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
	"mumu-bot/internal/memory"
)

type GraphTopic struct {
	memory.TopicSummaryRecord
	TopicID uint
}

type GraphSelection struct {
	Item        *memory.KnowledgeItem
	Relation    *memory.KnowledgeRelation
	Topic       *GraphTopic
	Description string
	Evidence    []memory.KnowledgeEvidence
	HasMore     bool
}

func (s *AdminService) GraphSelection(ctx context.Context, groupID int64, kind string, id, related uint, offset int) (GraphSelection, error) {
	d := GraphSelection{}
	db := s.db.WithContext(ctx)
	if groupID <= 0 || id == 0 || offset < 0 {
		return d, gorm.ErrRecordNotFound
	}
	switch kind {
	case "knowledge":
		var item memory.KnowledgeItem
		if err := db.Where("group_id=? AND id=?", groupID, id).First(&item).Error; err != nil {
			return d, err
		}
		d.Item = &item
		var err error
		d.Evidence, d.HasMore, err = s.memory.ListKnowledgeEvidencePage(ctx, groupID, id, 0, related, offset, 5)
		return d, err
	case "relation":
		var relation memory.KnowledgeRelation
		if err := db.Table("knowledge_relations kr").Select("kr.*").Joins("JOIN knowledge_items ki ON ki.id=kr.source_item_id").Where("ki.group_id=? AND kr.id=?", groupID, id).First(&relation).Error; err != nil {
			return d, err
		}
		var endpoints []memory.KnowledgeItem
		if err := db.Where("group_id=? AND id IN ?", groupID, []uint{relation.SourceItemID, relation.TargetItemID}).Find(&endpoints).Error; err != nil {
			return d, err
		}
		if len(endpoints) != 2 {
			return d, gorm.ErrRecordNotFound
		}
		for _, item := range endpoints {
			if item.ID == relation.SourceItemID {
				d.Description = item.Content
			}
		}
		for _, item := range endpoints {
			if item.ID == relation.TargetItemID {
				d.Description += "\n↓\n" + item.Content
			}
		}
		d.Relation = &relation
		var err error
		d.Evidence, d.HasMore, err = s.memory.ListKnowledgeEvidencePage(ctx, groupID, 0, id, 0, offset, 5)
		return d, err
	case "topic":
		var topic GraphTopic
		if err := memory.LatestTopicSummaries(db, groupID, 0).Where("ts.topic_id=?", id).First(&topic).Error; err != nil {
			return d, err
		}
		d.Topic = &topic
		q := db.Table("message_logs ml").Select("ml.*").Joins("JOIN topic_summary_sources ss ON ss.message_log_id=ml.id").Where("ss.summary_id=? AND ml.group_id=?", topic.ID, groupID)
		if related > 0 {
			var summary memory.TopicSummary
			if err := sonic.UnmarshalString(topic.SummaryJSON, &summary); err != nil {
				return d, err
			}
			found := false
			for _, link := range summary.RelatedTopics {
				if link.TopicID == related {
					found = true
					d.Description = link.Reason
					q = q.Where("ml.id IN ?", link.SourceMessageIDs)
					break
				}
			}
			if !found {
				return d, gorm.ErrRecordNotFound
			}
			if !topic.SourcesValid {
				d.Description = "关联的原文依据已失效，等待重新整理。"
			}
		}
		var messages []memory.MessageLog
		if err := q.Order("ml.id").Offset(offset).Limit(6).Scan(&messages).Error; err != nil {
			return d, err
		}
		d.HasMore = len(messages) > 5
		if d.HasMore {
			messages = messages[:5]
		}
		valid := topic.SourcesValid
		for i := range messages {
			if messages[i].RecalledAt != nil {
				messages[i].TextContent = ""
				messages[i].DisplayContent = memory.RecalledMessageDisplayContent
				valid = false
			}
		}
		if len(messages) > 0 {
			d.Evidence = []memory.KnowledgeEvidence{{ID: topic.ID, Messages: messages, Valid: valid}}
		}
		return d, nil
	default:
		return d, gorm.ErrRecordNotFound
	}
}

type GraphSource struct {
	ItemID  uint
	TopicID uint
}

type GroupKnowledgeGraph struct {
	Items     []memory.KnowledgeItem
	Relations []memory.KnowledgeRelation
	Topics    []GraphTopic
	Sources   []GraphSource
	Total     int64
}

func (s *AdminService) KnowledgeGroups(ctx context.Context) ([]int64, error) {
	var ids []int64
	err := s.db.WithContext(ctx).Raw("SELECT group_id FROM knowledge_items UNION SELECT group_id FROM topic_threads ORDER BY group_id").Scan(&ids).Error
	return ids, err
}

func (s *AdminService) GroupKnowledgeGraph(ctx context.Context, f KnowledgeFilter, focusKind string, focusID uint) (GroupKnowledgeGraph, error) {
	g := GroupKnowledgeGraph{}
	if f.GroupID <= 0 {
		return g, nil
	}
	db := s.db.WithContext(ctx)
	kq := db.Table("knowledge_items ki").Where("ki.group_id=?", f.GroupID)
	if f.Status == "" {
		kq = kq.Where("ki.status IN ('active','candidate')")
	} else if f.Status != "all" {
		kq = kq.Where("ki.status=?", f.Status)
	}
	if f.Kind != "" {
		kq = kq.Where("ki.kind=?", f.Kind)
	}
	if f.UserID > 0 {
		kq = kq.Where("ki.subject_user_id=?", f.UserID)
	}
	if f.AuthorID > 0 {
		kq = kq.Where(strings.ReplaceAll(knowledgeParticipationSQL, "knowledge_items.id", "ki.id"), f.AuthorID)
	}
	tq := memory.LatestTopicSummaries(db, f.GroupID, 0)
	if f.Kind != "" && f.Kind != "topic" {
		tq = tq.Where("false")
	}
	if f.Keyword != "" {
		kq = kq.Where("ki.label ILIKE ? OR ki.content ILIKE ?", "%"+f.Keyword+"%", "%"+f.Keyword+"%")
		tq = tq.Where(memory.TopicSummaryTextSQL+" ILIKE ?", "%"+f.Keyword+"%")
	}
	if focusID > 0 {
		switch focusKind {
		case "knowledge":
			kq = kq.Where(`ki.id=? OR ki.id IN(SELECT source_item_id FROM knowledge_relations WHERE target_item_id=? UNION SELECT target_item_id FROM knowledge_relations WHERE source_item_id=?)`, focusID, focusID, focusID)
			tq = tq.Where(`ts.topic_id IN(SELECT a.topic_id FROM knowledge_evidence_sets es JOIN knowledge_evidence_messages em ON em.evidence_set_id=es.id JOIN topic_assignments a ON a.message_log_id=em.message_log_id WHERE es.item_id=? AND `+memory.KnowledgeEvidenceSetValiditySQL+`)`, focusID)
		case "topic":
			kq = kq.Where(`ki.id IN(SELECT es.item_id FROM knowledge_evidence_sets es JOIN knowledge_evidence_messages em ON em.evidence_set_id=es.id JOIN topic_assignments a ON a.message_log_id=em.message_log_id WHERE a.topic_id=? AND `+memory.KnowledgeEvidenceSetValiditySQL+`)`, focusID)
			tq = tq.Where(`ts.topic_id=? OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(ts.summary_json->'related_topics','[]'::jsonb)) r WHERE r->>'topic_id'=?) OR ts.topic_id::text IN(SELECT r->>'topic_id' FROM (?) f CROSS JOIN LATERAL jsonb_array_elements(COALESCE(f.summary_json->'related_topics','[]'::jsonb)) r WHERE f.topic_id=?)`, focusID, fmt.Sprint(focusID), memory.LatestTopicSummaries(db, f.GroupID, 0), focusID)
		}
	}
	krows := kq.Select("ki.id,'knowledge' kind,ki.updated_at changed")
	trows := tq.Select("ts.topic_id id,'topic' kind,ts.created_at changed")
	if err := db.Raw("SELECT count(*) FROM ((?) UNION ALL (?)) nodes", krows, trows).Scan(&g.Total).Error; err != nil {
		return g, err
	}
	var selected []struct {
		ID   uint
		Kind string
	}
	if err := db.Raw("SELECT id,kind FROM ((?) UNION ALL (?)) nodes ORDER BY changed DESC,kind,id LIMIT 200", krows, trows).Scan(&selected).Error; err != nil {
		return g, err
	}
	var kids, tids []uint
	for _, node := range selected {
		if node.Kind == "knowledge" {
			kids = append(kids, node.ID)
		} else {
			tids = append(tids, node.ID)
		}
	}
	if len(kids) > 0 {
		if err := db.Where("group_id=? AND id IN ?", f.GroupID, kids).Order("id").Find(&g.Items).Error; err != nil {
			return g, err
		}
		q := db.Where("source_item_id IN ? AND target_item_id IN ?", kids, kids)
		if f.Status == "" {
			q = q.Where("status IN ('active','candidate')")
		} else if f.Status != "all" {
			q = q.Where("status=?", f.Status)
		}
		if err := q.Order("id").Find(&g.Relations).Error; err != nil {
			return g, err
		}
	}
	if len(tids) > 0 {
		if err := memory.LatestTopicSummaries(db, f.GroupID, 0).Where("ts.topic_id IN ?", tids).Order("ts.topic_id").Scan(&g.Topics).Error; err != nil {
			return g, err
		}
	}
	if len(kids) > 0 && len(tids) > 0 {
		if err := db.Raw(`SELECT DISTINCT es.item_id,a.topic_id FROM knowledge_evidence_sets es JOIN knowledge_evidence_messages em ON em.evidence_set_id=es.id JOIN message_logs ml ON ml.id=em.message_log_id JOIN topic_assignments a ON a.message_log_id=ml.id WHERE es.item_id IN ? AND a.topic_id IN ? AND ml.group_id=? AND `+memory.KnowledgeEvidenceSetValiditySQL+` ORDER BY es.item_id,a.topic_id`, kids, tids, f.GroupID).Scan(&g.Sources).Error; err != nil {
			return g, err
		}
	}
	return g, nil
}
