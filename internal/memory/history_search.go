package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// HistorySearchQuery 历史原文检索条件
type HistorySearchQuery struct {
	ConversationKind string
	TargetID         int64
	UserID           int64
	Text             string
	From             *time.Time
	To               *time.Time
	AfterID          uint
	BeforeID         uint
	Limit            int
	NewestFirst      bool
}

// SearchConversationHistory 检索历史原文，不限会话即可跨群跨私聊查询
func (m *Manager) SearchConversationHistory(ctx context.Context, input HistorySearchQuery) (KnowledgeMessagePage, error) {
	q := m.db.WithContext(ctx).Table("message_logs ml").Select("ml.*").
		Where("ml.recalled_at IS NULL AND " + OriginalMessageTextSQL + "<>''")
	if input.ConversationKind != "" {
		q = q.Where("ml.conversation_kind=?", input.ConversationKind)
	}
	if input.TargetID > 0 {
		q = q.Where("ml.target_id=?", input.TargetID)
	}
	if input.UserID != 0 {
		q = q.Where("ml.user_id=?", input.UserID)
	}
	if text := strings.TrimSpace(input.Text); text != "" {
		q = q.Where("strpos(lower(ml.text_content),lower(?))>0", text)
	}
	if input.From != nil {
		q = q.Where("ml.message_time>=?", *input.From)
	}
	if input.To != nil {
		q = q.Where("ml.message_time<=?", *input.To)
	}
	if input.AfterID > 0 {
		q = q.Where("ml.id>?", input.AfterID)
	}
	if input.BeforeID > 0 {
		q = q.Where("ml.id<?", input.BeforeID)
	}
	return knowledgeHistoryPage(q, input.Limit, input.NewestFirst)
}

// KnowledgeUpdate 知识条目更新内容
type KnowledgeUpdate struct {
	Content        *string
	Label          *string
	Kind           *string
	SubjectUserID  *int64
	ExpectedUpdate time.Time
}

// UpdateKnowledgeItem 按 ID 更新知识条目，用更新时间做并发校验
func (m *Manager) UpdateKnowledgeItem(ctx context.Context, id uint, update KnowledgeUpdate) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item KnowledgeItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&item).Error; err != nil {
			return err
		}
		if !update.ExpectedUpdate.IsZero() && !item.UpdatedAt.Equal(update.ExpectedUpdate) {
			return fmt.Errorf("知识 %d 已被其他会话修改，请重新读取后再更新", id)
		}
		changed := false
		if update.Content != nil {
			item.Content = strings.TrimSpace(*update.Content)
			changed = true
		}
		if update.Label != nil {
			item.Label = strings.TrimSpace(*update.Label)
			changed = true
		}
		if update.Kind != nil {
			item.Kind = *update.Kind
			changed = true
		}
		if update.SubjectUserID != nil {
			item.SubjectUserID = *update.SubjectUserID
			changed = true
		}
		if !changed {
			return nil
		}
		// 正文变化后向量失效，由补全任务重新生成
		item.Embedding = nil
		return tx.Save(&item).Error
	})
}

// DeleteKnowledgeItem 删除知识条目及其证据与关系
func (m *Manager) DeleteKnowledgeItem(ctx context.Context, id uint) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item KnowledgeItem
		if err := tx.Where("id=?", id).Take(&item).Error; err != nil {
			return err
		}
		var setIDs []uint
		if err := tx.Model(&KnowledgeEvidenceSet{}).Where("item_id=?", id).Pluck("id", &setIDs).Error; err != nil {
			return err
		}
		if len(setIDs) > 0 {
			if err := tx.Where("evidence_set_id IN ?", setIDs).Delete(&KnowledgeEvidenceMessage{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("item_id=?", id).Delete(&KnowledgeEvidenceSet{}).Error; err != nil {
			return err
		}
		if err := tx.Where("source_item_id=? OR target_item_id=?", id, id).Delete(&KnowledgeRelation{}).Error; err != nil {
			return err
		}
		return tx.Delete(&KnowledgeItem{}, "id=?", id).Error
	})
}
