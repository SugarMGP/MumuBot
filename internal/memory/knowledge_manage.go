package memory

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Manager) GetWorkingNote(ctx context.Context, groupID int64) (*GroupAgentState, error) {
	var row GroupAgentState
	err := m.db.WithContext(ctx).First(&row, "group_id=?", groupID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &GroupAgentState{GroupID: groupID}, nil
	}
	return &row, err
}
func (m *Manager) SaveWorkingNote(ctx context.Context, groupID int64, note string) error {
	note = strings.TrimSpace(note)
	if groupID <= 0 || utf8.RuneCountInString(note) > 300 {
		return invalidKnowledge("群便签需要有效群号且不能超过 300 个字符，请修正后重试")
	}
	if note == "" {
		return m.db.WithContext(ctx).Where("group_id=?", groupID).Delete(&GroupAgentState{}).Error
	}
	row := GroupAgentState{GroupID: groupID, Note: note, UpdatedAt: time.Now()}
	return m.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "group_id"}}, DoUpdates: clause.AssignmentColumns([]string{"note", "updated_at"})}).Create(&row).Error
}

func (m *Manager) SetKnowledgeStatus(ctx context.Context, groupID int64, id uint, status string) error {
	if !validKnowledgeStatus(status) {
		return invalidKnowledge("知识状态无效，请选择启用或归档")
	}
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := LockKnowledgeGroup(tx, groupID); err != nil {
			return err
		}
		var item KnowledgeItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("group_id=? AND id=?", groupID, id).First(&item).Error; err != nil {
			return err
		}
		if status == "active" {
			if err := checkKnowledgeActivation(tx, item.ID); err != nil {
				return err
			}
			ok, err := knowledgeHasEvidence(tx, id, 0)
			if err != nil {
				return err
			}
			if !ok {
				return invalidKnowledge("知识缺少完整有效依据，暂时不能启用")
			}
			var duplicate KnowledgeItem
			err = tx.Where("group_id=? AND subject_user_id=? AND kind=? AND lower(btrim(label))=lower(btrim(?)) AND btrim(content)=? AND status='active' AND id<>?", groupID, item.SubjectUserID, item.Kind, item.Label, item.Content, id).First(&duplicate).Error
			if err == nil {
				var sets []KnowledgeEvidenceSet
				if err := tx.Where("item_id=?", id).Find(&sets).Error; err != nil {
					return err
				}
				for _, set := range sets {
					var ids []uint
					if err := tx.Model(&KnowledgeEvidenceMessage{}).Where("evidence_set_id=?", set.ID).Order("message_log_id").Pluck("message_log_id", &ids).Error; err != nil {
						return err
					}
					if err := saveKnowledgeEvidence(tx, &duplicate.ID, nil, ids); err != nil {
						return err
					}
				}
				if err := tx.Model(&duplicate).Updates(map[string]any{"updated_at": time.Now(), "reviewed_through_id": gorm.Expr("GREATEST(reviewed_through_id,?,(SELECT COALESCE(max(id),0) FROM message_logs WHERE group_id=?))", item.ReviewedThroughID, groupID)}).Error; err != nil {
					return err
				}
				if err := tx.Model(&item).Updates(map[string]any{"status": "archived", "updated_at": time.Now()}).Error; err != nil {
					return err
				}
				return archiveInvalidKnowledgeRelations(tx, groupID)
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		updates := map[string]any{"status": status, "updated_at": time.Now(), "reviewed_through_id": gorm.Expr("GREATEST(reviewed_through_id,(SELECT COALESCE(max(id),0) FROM message_logs WHERE group_id=?))", groupID)}
		if err := tx.Model(&item).Updates(updates).Error; err != nil {
			return err
		}
		return archiveInvalidKnowledgeRelations(tx, groupID)
	})
}

func (m *Manager) SetKnowledgeRelationStatus(ctx context.Context, groupID int64, id uint, status string) error {
	if !validKnowledgeStatus(status) {
		return invalidKnowledge("关系状态无效，请选择启用或归档")
	}
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := LockKnowledgeGroup(tx, groupID); err != nil {
			return err
		}
		var relation KnowledgeRelation
		if err := tx.First(&relation, id).Error; err != nil {
			return err
		}
		var items []KnowledgeItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("group_id=? AND id IN ?", groupID, []uint{relation.SourceItemID, relation.TargetItemID}).Order("id").Find(&items).Error; err != nil {
			return err
		}
		if len(items) != 2 {
			return invalidKnowledge("关系不属于当前群，请刷新后重新选择")
		}
		if status == "active" {
			if err := checkRelationActivation(tx, relation); err != nil {
				return err
			}
			for _, item := range items {
				ok, err := knowledgeHasEvidence(tx, item.ID, 0)
				if err != nil {
					return err
				}
				if !ok || (item.Status != "active" && !(relation.Kind == "supersedes" && item.ID == relation.TargetItemID && item.Status == "archived")) {
					return invalidKnowledge("关系端点尚未启用或缺少有效依据，请先处理端点知识")
				}
			}
			ok, err := knowledgeHasEvidence(tx, 0, id)
			if err != nil {
				return err
			}
			if !ok {
				return invalidKnowledge("关系缺少完整有效依据，暂时不能启用")
			}
		}
		if err := tx.Model(&relation).Update("status", status).Error; err != nil {
			return err
		}
		updates := map[string]any{"updated_at": time.Now(), "reviewed_through_id": gorm.Expr("GREATEST(reviewed_through_id,(SELECT COALESCE(max(id),0) FROM message_logs WHERE group_id=?))", groupID)}
		if err := tx.Model(&KnowledgeItem{}).Where("id IN ?", []uint{relation.SourceItemID, relation.TargetItemID}).Updates(updates).Error; err != nil {
			return err
		}
		if status == "active" && relation.Kind == "supersedes" {
			if err := tx.Model(&KnowledgeItem{}).Where("id=?", relation.TargetItemID).Updates(map[string]any{"status": "archived", "updated_at": time.Now()}).Error; err != nil {
				return err
			}
		}
		return archiveInvalidKnowledgeRelations(tx, groupID)
	})
}

func (m *Manager) FillKnowledgeEmbeddings(ctx context.Context, limit int) error {
	var pending []struct {
		ID      uint
		Kind    string
		Content string
	}
	if err := m.db.WithContext(ctx).Raw(`SELECT id,kind,content FROM (
 SELECT id,'knowledge' kind,concat_ws(': ',nullif(label,''),content) content,created_at FROM knowledge_items WHERE embedding IS NULL AND status='active'
 UNION ALL SELECT ts.id,'topic' kind,`+TopicSummaryTextSQL+` content,ts.created_at FROM topic_summaries ts WHERE ts.embedding IS NULL
 ) pending ORDER BY created_at,id LIMIT ?`, max(1, min(limit, 10))).Scan(&pending).Error; err != nil {
		return err
	}
	for _, item := range pending {
		values, err := m.embedding.Embed(ctx, item.Content)
		if err != nil {
			return err
		}
		vector, err := EmbeddingVector(values)
		if err != nil {
			return err
		}
		var row any = &KnowledgeItem{}
		if item.Kind == "topic" {
			row = &TopicSummaryRecord{}
		}
		if err := m.db.WithContext(ctx).Model(row).Where("id=? AND embedding IS NULL", item.ID).UpdateColumn("embedding", vector).Error; err != nil {
			return err
		}
	}
	return nil
}
