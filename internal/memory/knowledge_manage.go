package memory

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Manager) GetWorkingNote(ctx context.Context, groupID int64) (*ConversationAgentState, error) {
	return m.GetWorkingNoteScope(ctx, ConversationKindGroup, groupID)
}

func (m *Manager) GetWorkingNoteScope(ctx context.Context, kind string, targetID int64) (*ConversationAgentState, error) {
	var row ConversationAgentState
	err := m.db.WithContext(ctx).First(&row, "conversation_kind=? AND target_id=?", kind, targetID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &ConversationAgentState{ConversationKind: kind, TargetID: targetID}, nil
	}
	return &row, err
}
func (m *Manager) SaveWorkingNote(ctx context.Context, groupID int64, note string) error {
	return m.SaveWorkingNoteScope(ctx, ConversationKindGroup, groupID, note)
}

func (m *Manager) SaveWorkingNoteScope(ctx context.Context, kind string, targetID int64, note string) error {
	note = strings.TrimSpace(note)
	if targetID <= 0 || utf8.RuneCountInString(note) > 300 {
		return invalidKnowledge("便签目标无效或超过 300 个字符，请修正后重试")
	}
	if note == "" {
		return m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=?", kind, targetID).Delete(&ConversationAgentState{}).Error
	}
	row := ConversationAgentState{ConversationKind: kind, TargetID: targetID, Note: note, UpdatedAt: time.Now()}
	return m.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "conversation_kind"}, {Name: "target_id"}}, DoUpdates: clause.AssignmentColumns([]string{"note", "updated_at"})}).Create(&row).Error
}

// SetKnowledgeStatusScope 按会话作用域启用或归档知识
func (m *Manager) SetKnowledgeStatusScope(ctx context.Context, kind string, targetID int64, id uint, status string) error {
	if !validKnowledgeStatus(status) {
		return invalidKnowledge("知识状态无效，请选择启用或归档")
	}
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := LockKnowledgeScope(tx, kind, targetID); err != nil {
			return err
		}
		var item KnowledgeItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_kind=? AND target_id=? AND id=?", kind, targetID, id).First(&item).Error; err != nil {
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
			err = tx.Where("conversation_kind=? AND target_id=? AND subject_user_id=? AND kind=? AND lower(btrim(label))=lower(btrim(?)) AND lower(btrim(content))=lower(btrim(?)) AND status='active' AND id<>?", kind, targetID, item.SubjectUserID, item.Kind, item.Label, item.Content, id).First(&duplicate).Error
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
				if err := tx.Model(&duplicate).Updates(map[string]any{"updated_at": time.Now(), "reviewed_through_id": gorm.Expr("GREATEST(reviewed_through_id,?,(SELECT COALESCE(max(id),0) FROM message_logs WHERE conversation_kind=? AND target_id=?))", item.ReviewedThroughID, kind, targetID)}).Error; err != nil {
					return err
				}
				if err := tx.Model(&item).Updates(map[string]any{"status": "archived", "updated_at": time.Now()}).Error; err != nil {
					return err
				}
				return ArchiveInvalidKnowledgeRelations(tx, kind, targetID)
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		updates := map[string]any{"status": status, "updated_at": time.Now(), "reviewed_through_id": gorm.Expr("GREATEST(reviewed_through_id,(SELECT COALESCE(max(id),0) FROM message_logs WHERE conversation_kind=? AND target_id=?))", kind, targetID)}
		if err := tx.Model(&item).Updates(updates).Error; err != nil {
			return err
		}
		return ArchiveInvalidKnowledgeRelations(tx, kind, targetID)
	})
}

// EmbeddingCursor 保存补全任务的轮转位置，失败记录留待下一轮扫描
type EmbeddingCursor struct {
	CreatedAt time.Time
	ID        uint
	Kind      string
}

func (m *Manager) FillKnowledgeEmbeddings(ctx context.Context, limit int, after EmbeddingCursor) (EmbeddingCursor, error) {
	var pending []struct {
		ID        uint
		Kind      string
		Content   string
		CreatedAt time.Time
	}
	for attempt := 0; attempt < 2; attempt++ {
		query := `SELECT id,kind,content,created_at FROM (
 SELECT id,'knowledge' kind,concat_ws(': ',nullif(label,''),content) content,created_at FROM knowledge_items WHERE embedding IS NULL AND status='active'
 UNION ALL SELECT ts.id,'topic' kind,` + TopicSummaryTextSQL + ` content,ts.created_at FROM topic_summaries ts WHERE ts.embedding IS NULL
		) pending`
		args := []any{}
		if !after.CreatedAt.IsZero() {
			query += " WHERE (created_at,id,kind) > (?,?,?)"
			args = append(args, after.CreatedAt, after.ID, after.Kind)
		}
		query += " ORDER BY created_at,id,kind LIMIT ?"
		args = append(args, max(1, min(limit, 10)))
		if err := m.db.WithContext(ctx).Raw(query, args...).Scan(&pending).Error; err != nil {
			return after, err
		}
		if len(pending) > 0 || after.CreatedAt.IsZero() {
			break
		}
		after = EmbeddingCursor{}
	}
	for _, item := range pending {
		if err := ctx.Err(); err != nil {
			return after, err
		}
		values, err := m.embedding.Embed(ctx, item.Content)
		// 游标记录已尝试的位置，超时或写入失败的记录留到扫描回绕后重试
		after = EmbeddingCursor{CreatedAt: item.CreatedAt, ID: item.ID, Kind: item.Kind}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return after, ctxErr
		}
		if err != nil {
			zap.L().Warn("知识向量补全失败，本轮跳过", zap.Uint("id", item.ID), zap.String("kind", item.Kind), zap.Error(err))
			continue
		}
		vector, err := EmbeddingVector(values)
		if err != nil {
			zap.L().Warn("知识向量维度校验失败，本轮跳过", zap.Uint("id", item.ID), zap.String("kind", item.Kind), zap.Error(err))
			continue
		}
		var row any = &KnowledgeItem{}
		if item.Kind == "topic" {
			row = &TopicSummaryRecord{}
		}
		if err := m.db.WithContext(ctx).Model(row).Where("id=? AND embedding IS NULL", item.ID).UpdateColumn("embedding", vector).Error; err != nil {
			return after, err
		}
	}
	return after, nil
}
