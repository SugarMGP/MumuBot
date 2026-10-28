package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
)

type KnowledgeFilter struct {
	MemoryFilter
	ConversationKind string
	TargetID         int64
	UserID, AuthorID int64
}

func knowledgeParticipationSQL(authorExpr string) string {
	return `EXISTS (
	 SELECT 1 FROM jsonb_array_elements_text(COALESCE(ki.source_meta->'message_log_ids','[]'::jsonb)) src
	 JOIN message_logs ml ON ml.id=src.value::bigint
	 WHERE ml.user_id=` + authorExpr + `
	)`
}

type LegacyRelation struct {
	ID         uint
	Kind       string
	Status     string
	OtherID    uint
	OtherLabel string
}

type KnowledgeDetail struct {
	Page            int
	HasMore         bool
	Item            memory.KnowledgeRecord
	Revisions       []memory.KnowledgeRevision
	Sources         []memory.MessageLog
	LegacySources   []memory.MessageLog
	LegacyRelations []LegacyRelation
	Topics          map[uint]uint
}

type KnowledgeConversation struct {
	Kind     string
	TargetID int64
	Name     string
}

// KnowledgeConversations 列出有知识或话题的会话
func (s *AdminService) KnowledgeConversations(ctx context.Context) ([]KnowledgeConversation, error) {
	var rows []KnowledgeConversation
	err := s.db.WithContext(ctx).Raw(`SELECT c.conversation_kind AS kind,c.target_id AS target_id,COALESCE(ct.name,'') AS name
		FROM (SELECT conversation_kind,target_id FROM knowledge_items
		      UNION SELECT conversation_kind,target_id FROM topic_threads) c
		LEFT JOIN conversation_targets ct ON ct.conversation_kind=c.conversation_kind AND ct.target_id=c.target_id
		ORDER BY c.conversation_kind,c.target_id`).Scan(&rows).Error
	return rows, err
}

func (s *AdminService) ListKnowledge(f KnowledgeFilter) (Page[memory.KnowledgeRecord], error) {
	var items []memory.KnowledgeRecord
	q := s.filterKnowledge(s.db.Table("knowledge_items ki"), f)
	q = order(q, f.Sort, f.Order, map[string]string{"updated": "ki.updated_at", "created": "ki.created_at", "default": "ki.updated_at"})
	return paginate(q, f.Page, f.PageSize, &items)
}

func (s *AdminService) filterKnowledge(q *gorm.DB, f KnowledgeFilter) *gorm.DB {
	if f.ConversationKind != "" && f.TargetID > 0 {
		q = q.Where("ki.conversation_kind=? AND ki.target_id=?", f.ConversationKind, f.TargetID)
	}
	if f.UserID > 0 {
		q = q.Where("ki.subject_user_id=?", f.UserID)
	}
	if f.AuthorID > 0 {
		q = q.Where(knowledgeParticipationSQL("?"), f.AuthorID)
	}
	if f.Kind != "" {
		q = q.Where("ki.kind=?", f.Kind)
	}
	if f.Status != "" && f.Status != "all" {
		q = q.Where("ki.status=?", f.Status)
	}
	if k := strings.TrimSpace(f.Keyword); k != "" {
		p := "%" + k + "%"
		q = q.Where("ki.label ILIKE ? OR ki.content ILIKE ?", p, p)
	}
	return q
}

type KnowledgeMetadata struct {
	Name     string
	Messages int64
	Recalled int64
}

func (m KnowledgeMetadata) ValidMessages() int64 {
	return max(0, m.Messages-m.Recalled)
}

func (s *AdminService) KnowledgeMetadata(ctx context.Context, items []memory.KnowledgeRecord) (map[uint]KnowledgeMetadata, error) {
	out := map[uint]KnowledgeMetadata{}
	if len(items) == 0 {
		return out, nil
	}
	ids := make([]uint, 0, len(items))
	messageIDs := make([]uint, 0, len(items)*2)
	byItem := make(map[uint][]uint, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
		for _, id := range sourceMessageIDs(item.SourceMeta) {
			byItem[item.ID] = append(byItem[item.ID], id)
			messageIDs = append(messageIDs, id)
		}
	}
	var names []struct {
		ID   uint
		Name string
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT ki.id,
		COALESCE(NULLIF(btrim(mp.nickname),''),(
		 SELECT mn.value FROM member_names mn WHERE mn.user_id=ki.subject_user_id AND btrim(mn.value)<>''
		 ORDER BY mn.updated_at DESC LIMIT 1),'') AS name
		FROM knowledge_items ki LEFT JOIN member_profiles mp ON mp.user_id=ki.subject_user_id
		WHERE ki.id IN ?`, ids).Scan(&names).Error; err != nil {
		return nil, err
	}
	recalledByID := map[uint]int64{}
	if len(messageIDs) > 0 {
		var rows []struct {
			ID         uint
			RecalledAt *time.Time
		}
		if err := s.db.WithContext(ctx).Model(&memory.MessageLog{}).Select("id,recalled_at").Where("id IN ?", messageIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		recalled := map[uint]bool{}
		for _, row := range rows {
			recalled[row.ID] = row.RecalledAt != nil
		}
		for itemID, ids := range byItem {
			for _, id := range ids {
				if recalled[id] {
					recalledByID[itemID]++
				}
			}
		}
	}
	for _, row := range names {
		out[row.ID] = KnowledgeMetadata{Name: row.Name, Messages: int64(len(byItem[row.ID])), Recalled: recalledByID[row.ID]}
	}
	return out, nil
}

func (s *AdminService) GetKnowledge(id uint) (memory.KnowledgeRecord, error) {
	var item memory.KnowledgeRecord
	err := s.db.First(&item, id).Error
	return item, err
}

func (s *AdminService) KnowledgeDetail(ctx context.Context, id uint, page int) (KnowledgeDetail, error) {
	d := KnowledgeDetail{Page: max(1, page), Topics: map[uint]uint{}}
	item, err := s.GetKnowledge(id)
	if err != nil {
		return d, err
	}
	d.Item = item
	if err := s.db.WithContext(ctx).Where("item_id=?", id).Order("version DESC,id DESC").
		Offset((d.Page - 1) * 5).Limit(6).Find(&d.Revisions).Error; err != nil {
		return d, err
	}
	if len(d.Revisions) > 5 {
		d.HasMore = true
		d.Revisions = d.Revisions[:5]
	}
	ids := sourceMessageIDs(item.SourceMeta)
	if len(ids) > 50 {
		ids = ids[:50]
	}
	if len(ids) > 0 {
		if err := s.db.WithContext(ctx).Where("id IN ?", ids).Order("id").Find(&d.Sources).Error; err != nil {
			return d, err
		}
		var assignments []memory.TopicAssignment
		if err := s.db.WithContext(ctx).Where("message_log_id IN ?", ids).Find(&assignments).Error; err != nil {
			return d, err
		}
		for _, row := range assignments {
			if row.TopicID != nil {
				d.Topics[row.MessageLogID] = *row.TopicID
			}
		}
	}
	if s.db.Migrator().HasTable("legacy_knowledge_evidence_messages") && s.db.Migrator().HasTable("legacy_knowledge_evidence_sets") {
		if err := s.db.WithContext(ctx).Table("message_logs ml").Select("DISTINCT ml.*").
			Joins("JOIN legacy_knowledge_evidence_messages lem ON lem.message_log_id=ml.id").
			Joins("JOIN legacy_knowledge_evidence_sets les ON les.id=lem.evidence_set_id").
			Where("les.item_id=?", id).Order("ml.id").Limit(50).Scan(&d.LegacySources).Error; err != nil {
			return d, err
		}
	}
	if s.db.Migrator().HasTable("legacy_knowledge_relations") {
		if err := s.db.WithContext(ctx).Raw(`SELECT kr.id,kr.kind,kr.status,
			CASE WHEN kr.source_item_id=? THEN kr.target_item_id ELSE kr.source_item_id END AS other_id,
			COALESCE(ki.label,left(ki.content,40),'') AS other_label
			FROM legacy_knowledge_relations kr LEFT JOIN knowledge_items ki
			ON ki.id=CASE WHEN kr.source_item_id=? THEN kr.target_item_id ELSE kr.source_item_id END
			WHERE kr.source_item_id=? OR kr.target_item_id=? ORDER BY kr.id`, id, id, id, id).Scan(&d.LegacyRelations).Error; err != nil {
			return d, err
		}
	}
	return d, nil
}

func (s *AdminService) UpdateKnowledgeStatus(ctx context.Context, id uint, status string) error {
	status = strings.TrimSpace(status)
	if status != "active" && status != "archived" {
		return fmt.Errorf("记忆状态无效")
	}
	item, err := s.GetKnowledge(id)
	if err != nil {
		return err
	}
	if item.Status == status {
		return nil
	}
	action := "archive"
	if status == "active" {
		action = "restore"
		var duplicate int64
		if err := s.db.WithContext(ctx).Model(&memory.KnowledgeRecord{}).
			Where("subject_user_id=? AND kind=? AND content_hash=? AND status='active' AND id<>?", item.SubjectUserID, item.Kind, item.ContentHash, item.ID).
			Count(&duplicate).Error; err != nil {
			return err
		}
		if duplicate > 0 {
			return fmt.Errorf("已有一条相同内容处于启用状态")
		}
	}
	_, err = s.memory.ManageMemories(ctx, []memory.MemoryChange{{
		Action: action, ItemID: item.ID, ExpectedVersion: item.Version, Reason: "后台维护",
	}})
	return err
}

func sourceMessageIDs(raw string) []uint {
	var meta struct {
		MessageLogIDs []uint `json:"message_log_ids"`
	}
	if sonic.UnmarshalString(raw, &meta) != nil {
		return nil
	}
	return meta.MessageLogIDs
}
