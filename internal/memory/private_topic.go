package memory

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const privateTopicValiditySQL = "EXISTS(SELECT 1 FROM private_topic_sources s WHERE s.target_id=ps.target_id)\n AND NOT EXISTS(SELECT 1 FROM private_topic_sources s JOIN message_logs ml ON ml.id=s.message_log_id WHERE s.target_id=ps.target_id\n AND (ml.conversation_kind<>'private' OR ml.target_id<>ps.target_id OR ml.recalled_at IS NOT NULL OR " + OriginalMessageTextSQL + "=''))"

// SummaryText 把摘要 JSON 转成聊天提示词使用的可读文本
func (s PrivateTopicState) SummaryText() string {
	var summary TopicSummary
	if err := sonic.UnmarshalString(s.SummaryJSON, &summary); err != nil {
		return ""
	}
	var parts []string
	if title := strings.TrimSpace(summary.Title); title != "" {
		parts = append(parts, "标题："+title)
	}
	if gist := strings.TrimSpace(summary.Gist); gist != "" {
		parts = append(parts, "概要："+gist)
	}
	return strings.Join(parts, "\n")
}

// CommitPrivateBatch 原子提交当前好友的知识、来源可核对的摘要和整理水位，不修改聊天便签
func (m *Manager) CommitPrivateBatch(ctx context.Context, batch KnowledgeBatch, rows []MessageLog, observed *PrivateTopicState, summary TopicSummary, sourceMessageIDs []uint) (*KnowledgeCommitResult, error) {
	if batch.ConversationKind != ConversationKindPrivate || batch.TargetID <= 0 || batch.ThroughID <= batch.AfterID || !batch.AdvanceCursor || len(rows) == 0 {
		return nil, invalidKnowledge("私聊整理范围无效，请重新读取原文后重试")
	}
	summary.Title = strings.TrimSpace(summary.Title)
	summary.Gist = strings.TrimSpace(summary.Gist)
	hasRaw := slices.ContainsFunc(rows, func(row MessageLog) bool { return row.RecalledAt == nil && strings.TrimSpace(row.TextContent) != "" })
	if hasRaw && (summary.Title == "" || summary.Gist == "" || len(sourceMessageIDs) == 0) {
		return nil, invalidKnowledge("私聊摘要必须提供 title、gist 和 source_message_ids")
	}
	if !hasRaw && (summary.Title != "" || summary.Gist != "" || len(sourceMessageIDs) != 0 || len(batch.Items) != 0 || len(batch.Relations) != 0) {
		return nil, invalidKnowledge("本批没有可用原文，不能提交摘要或知识")
	}
	sources := slices.Clone(sourceMessageIDs)
	slices.Sort(sources)
	sources = slices.Compact(sources)
	for _, id := range sources {
		if !slices.Contains(batch.ReadMessageIDs, id) {
			return nil, invalidKnowledge("摘要原文 %d 尚未完整读取", id)
		}
	}
	var result *KnowledgeCommitResult
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := LockKnowledgeScope(tx, batch.ConversationKind, batch.TargetID); err != nil {
			return err
		}
		if _, err := lockConversationSnapshot(tx, batch, rows); err != nil {
			return err
		}
		var current PrivateTopicState
		if err := tx.Where("target_id=?", batch.TargetID).Limit(1).Find(&current).Error; err != nil {
			return err
		}
		if (observed == nil && current.TargetID != 0) || (observed != nil && (current.TargetID != observed.TargetID || !current.UpdatedAt.Equal(observed.UpdatedAt))) {
			return ErrSnapshotChanged
		}
		// 接续旧库时可能先重读较早批次，不用旧范围覆盖已存在的较新摘要
		if hasRaw && current.ThroughMessageLogID <= batch.ThroughID {
			body, err := sonic.MarshalString(map[string]string{"title": summary.Title, "gist": summary.Gist})
			if err != nil {
				return err
			}
			row := PrivateTopicState{TargetID: batch.TargetID, SummaryJSON: body, ThroughMessageLogID: batch.ThroughID, UpdatedAt: time.Now()}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "target_id"}}, DoUpdates: clause.AssignmentColumns([]string{"summary_json", "through_message_log_id", "updated_at"})}).Create(&row).Error; err != nil {
				return err
			}
			if err := tx.Exec("DELETE FROM private_topic_sources WHERE target_id=?", batch.TargetID).Error; err != nil {
				return err
			}
			for _, id := range sources {
				if err := tx.Exec("INSERT INTO private_topic_sources(target_id,message_log_id) VALUES (?,?)", batch.TargetID, id).Error; err != nil {
					return err
				}
			}
		}
		batch.RequireAssigned = false
		var err error
		result, err = (&Manager{db: tx}).CommitKnowledgeBatch(ctx, batch)
		return err
	})
	return result, err
}

// GetPrivateTopicStateAt 隐去撤回来源或超出固定原文上界的摘要，保留版本信息用于提交并发校验
func (m *Manager) GetPrivateTopicStateAt(ctx context.Context, targetID int64, upper uint) (*PrivateTopicState, error) {
	var record struct {
		PrivateTopicState
		Valid       bool
		SourcesJSON string
	}
	q := m.db.WithContext(ctx).Table("private_topic_states ps").Select("ps.*, ("+privateTopicValiditySQL+") AS valid, COALESCE((SELECT jsonb_agg(s.message_log_id ORDER BY s.message_log_id) FROM private_topic_sources s WHERE s.target_id=ps.target_id),'[]'::jsonb)::text AS sources_json").Where("ps.target_id=?", targetID)
	if err := q.Take(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	row := record.PrivateTopicState
	row.SourcesValid = record.Valid
	if err := sonic.UnmarshalString(record.SourcesJSON, &row.SourceMessageIDs); err != nil {
		return nil, err
	}
	if upper > 0 && (row.ThroughMessageLogID > upper || slices.ContainsFunc(row.SourceMessageIDs, func(id uint) bool { return id > upper })) {
		row.SourcesValid = false
	}
	if !row.SourcesValid {
		row.SummaryJSON = ""
	}
	return &row, nil
}
