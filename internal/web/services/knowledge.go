package services

import (
	"context"
	"strings"

	"mumu-bot/internal/memory"

	"gorm.io/gorm"
)

type KnowledgeFilter struct {
	MemoryFilter
	ConversationKind string
	TargetID         int64
	UserID, AuthorID int64
}

// knowledgeParticipationSQL 返回知识条目与原文参与关联的 EXISTS 条件
// authorExpr 在子查询内以 message_logs 的 user_id 匹配作者：列表筛选传 "?"（参数绑定），
// 成员页统计传相关外层列（如 "mp.user_id"），不能引用子查询作用域之外的其他别名
func knowledgeParticipationSQL(authorExpr string) string {
	return `EXISTS (
 SELECT 1 FROM knowledge_evidence_sets es
 JOIN knowledge_evidence_messages em ON em.evidence_set_id=es.id
 JOIN message_logs ml ON ml.id=em.message_log_id
 LEFT JOIN knowledge_relations kr ON kr.id=es.relation_id
 WHERE (es.item_id=ki.id OR kr.source_item_id=ki.id OR kr.target_item_id=ki.id)
 AND ml.user_id=` + authorExpr + ` AND ` + memory.KnowledgeEvidenceSetValiditySQL + `
)`
}

type KnowledgeDetail struct {
	Page     int
	HasMore  bool
	Item     memory.KnowledgeItem
	Evidence []memory.KnowledgeEvidence
	Topics   map[uint]uint
}

// KnowledgeGroups 列出有记忆或话题的群号，供话题筛选使用
func (s *AdminService) KnowledgeGroups(ctx context.Context) ([]int64, error) {
	var ids []int64
	err := s.db.WithContext(ctx).Raw("SELECT target_id FROM knowledge_items WHERE conversation_kind='group' UNION SELECT group_id AS target_id FROM topic_threads ORDER BY target_id").Scan(&ids).Error
	return ids, err
}

// KnowledgeConversation 是后台知识筛选用的会话项
type KnowledgeConversation struct {
	Kind     string
	TargetID int64
	Name     string
}

// KnowledgeConversations 列出有知识或话题的会话，供知识筛选使用
func (s *AdminService) KnowledgeConversations(ctx context.Context) ([]KnowledgeConversation, error) {
	var rows []KnowledgeConversation
	err := s.db.WithContext(ctx).Raw(`SELECT c.conversation_kind AS kind, c.target_id AS target_id, COALESCE(ct.name,'') AS name
	 FROM (SELECT conversation_kind,target_id FROM knowledge_items UNION SELECT 'group',group_id FROM topic_threads) c
	 LEFT JOIN conversation_targets ct ON ct.conversation_kind=c.conversation_kind AND ct.target_id=c.target_id
	 ORDER BY c.conversation_kind, c.target_id`).Scan(&rows).Error
	return rows, err
}

func (s *AdminService) ListKnowledge(f KnowledgeFilter) (Page[memory.KnowledgeItem], error) {
	var items []memory.KnowledgeItem
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
		q = q.Where("ki.label ILIKE ? OR ki.content ILIKE ?", "%"+k+"%", "%"+k+"%")
	}
	return q
}

type KnowledgeMetadata struct {
	ID       uint
	Name     string
	Messages int64 // 全部原文条数
	Recalled int64 // 其中已撤回的条数
}

// ValidMessages 返回仍可用的原文条数
func (m KnowledgeMetadata) ValidMessages() int64 {
	return max(0, m.Messages-m.Recalled)
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
	err := s.db.WithContext(ctx).Table("knowledge_items ki").Select("ki.id,\n\t COALESCE(NULLIF(btrim(mp.nickname),''),(SELECT mn.value FROM member_names mn WHERE mn.user_id=ki.subject_user_id AND btrim(mn.value)<>'' ORDER BY (ki.conversation_kind='group' AND mn.group_id=ki.target_id) DESC,mn.updated_at DESC LIMIT 1),'') name,\n\t (SELECT count(*) FROM knowledge_evidence_messages em JOIN knowledge_evidence_sets es ON es.id=em.evidence_set_id WHERE es.item_id=ki.id) messages,\n\t (SELECT count(*) FROM knowledge_evidence_messages em JOIN knowledge_evidence_sets es ON es.id=em.evidence_set_id JOIN message_logs ml ON ml.id=em.message_log_id WHERE es.item_id=ki.id AND (ml.recalled_at IS NOT NULL OR "+memory.OriginalMessageTextSQL+"='')) recalled").
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
	d.Evidence, d.HasMore, err = s.memory.ListKnowledgeEvidencePageScope(ctx, item.ConversationKind, item.TargetID, id, 0, offset, 5)
	if err != nil {
		return d, err
	}
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
	return s.memory.SetKnowledgeStatusScope(ctx, item.ConversationKind, item.TargetID, id, strings.TrimSpace(status))
}
