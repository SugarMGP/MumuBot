package services

import (
	"context"
	"fmt"
	"strings"

	"mumu-bot/internal/memory"

	"gorm.io/gorm"
)

type KnowledgeFilter struct {
	MemoryFilter
	UserID int64
}

type KnowledgeConversation struct {
	Kind     string
	TargetID int64
	Name     string
}

// KnowledgeConversations 列出有话题的会话
func (s *AdminService) KnowledgeConversations(ctx context.Context) ([]KnowledgeConversation, error) {
	var rows []KnowledgeConversation
	err := s.db.WithContext(ctx).Raw(`SELECT c.conversation_kind AS kind,c.target_id AS target_id,COALESCE(ct.name,'') AS name
		FROM (SELECT DISTINCT conversation_kind,target_id FROM topic_threads) c
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
	if f.SubjectType != "" {
		q = q.Where("ki.subject_type=?", f.SubjectType)
	}
	if f.SubjectTarget != "" {
		q = q.Where("ki.subject_target=?", f.SubjectTarget)
	}
	if f.MemoryType != "" {
		q = q.Where("ki.memory_type=?", f.MemoryType)
	}
	if f.UserID > 0 {
		q = q.Where("ki.subject_type='user' AND ki.subject_target=?", fmt.Sprint(f.UserID))
	}
	if f.Status != "" && f.Status != "all" {
		q = q.Where("ki.status=?", f.Status)
	}
	if k := strings.TrimSpace(f.Keyword); k != "" {
		p := memory.LiteralContainsPattern(k)
		q = q.Where("ki.label ILIKE ? OR ki.content ILIKE ?", p, p)
	}
	return q
}

type KnowledgeDetail struct {
	Item      memory.KnowledgeRecord
	Revisions []memory.KnowledgeRevision
}

func (s *AdminService) GetKnowledge(id uint) (memory.KnowledgeRecord, error) {
	var item memory.KnowledgeRecord
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
	if err := s.db.WithContext(ctx).Where("item_id=?", id).Order("version DESC,id DESC").Find(&d.Revisions).Error; err != nil {
		return d, err
	}
	return d, nil
}

func (s *AdminService) UpdateKnowledgeStatus(ctx context.Context, id uint, status string) error {
	status = strings.TrimSpace(status)
	if status != "archived" {
		return fmt.Errorf("记忆只能归档，不能从后台恢复")
	}
	item, err := s.GetKnowledge(id)
	if err != nil {
		return err
	}
	if item.Status == status {
		return nil
	}
	_, err = s.memory.ManageMemories(ctx, []memory.MemoryChange{{
		Action: "archive", ItemID: item.ID, ExpectedVersion: item.Version, Reason: "后台归档",
	}})
	return err
}
