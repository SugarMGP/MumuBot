package memory

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

func (m *Manager) knowledgeHistoryQuery(ctx context.Context, groupID int64, upper uint) *gorm.DB {
	return m.db.WithContext(ctx).Table("message_logs ml").Select("ml.*").Where("ml.group_id=? AND ml.id<=? AND ml.recalled_at IS NULL AND btrim(ml.text_content)<>''", groupID, upper)
}

func knowledgeHistoryPage(q *gorm.DB, limit int) (KnowledgeMessagePage, error) {
	if limit <= 0 || limit > 30 {
		limit = 30
	}
	var rows []MessageLog
	if err := q.Order("ml.id ASC").Limit(limit + 1).Scan(&rows).Error; err != nil {
		return KnowledgeMessagePage{}, err
	}
	result := KnowledgeMessagePage{HasMore: len(rows) > limit}
	if result.HasMore {
		rows = rows[:limit]
	}
	result.Messages = rows
	if len(rows) > 0 {
		result.NextID = rows[len(rows)-1].ID
	}
	return result, nil
}

func (m *Manager) SearchKnowledgeMessages(ctx context.Context, input KnowledgeMessageQuery) (KnowledgeMessagePage, error) {
	if input.GroupID <= 0 || input.ThroughID == 0 {
		return KnowledgeMessagePage{}, fmt.Errorf("无效历史范围")
	}
	q := m.knowledgeHistoryQuery(ctx, input.GroupID, input.ThroughID).Where("ml.id>?", input.AfterID)
	if input.UserID != 0 {
		q = q.Where("ml.user_id=?", input.UserID)
	}
	if text := strings.TrimSpace(input.Text); text != "" {
		q = q.Where("strpos(lower(ml.text_content),lower(?))>0", text)
	}
	if input.ReplyToMessageID != nil {
		q = q.Where("ml.reply_to_message_id=?", *input.ReplyToMessageID)
	}
	if input.From != nil {
		q = q.Where("ml.message_time>=?", *input.From)
	}
	if input.To != nil {
		q = q.Where("ml.message_time<=?", *input.To)
	}
	return knowledgeHistoryPage(q, input.Limit)
}

func (m *Manager) ReadKnowledgeContext(ctx context.Context, groupID int64, upper, id, after uint, mode string) (KnowledgeMessagePage, error) {
	var target MessageLog
	if err := m.knowledgeHistoryQuery(ctx, groupID, upper).Where("ml.id=?", id).Take(&target).Error; err != nil {
		return KnowledgeMessagePage{}, err
	}
	q := m.knowledgeHistoryQuery(ctx, groupID, upper).Where("ml.id>?", after)
	switch mode {
	case "message":
		q = q.Where("ml.id=?", id)
	case "replies":
		if target.ReplyToMessageID != nil {
			q = q.Where("ml.id=? OR ml.one_bot_message_id=? OR ml.reply_to_message_id=?", id, *target.ReplyToMessageID, target.OneBotMessageID)
		} else {
			q = q.Where("ml.id=? OR ml.reply_to_message_id=?", id, target.OneBotMessageID)
		}
	case "topic":
		q = q.Where("EXISTS (SELECT 1 FROM topic_assignments a JOIN topic_assignments b ON a.topic_id=b.topic_id WHERE a.message_log_id=ml.id AND b.message_log_id=?)", id)
	case "window":
		q = q.Where(`ml.id IN (SELECT id FROM (SELECT id FROM message_logs WHERE group_id=? AND id<=? ORDER BY id DESC LIMIT 15) before_rows UNION SELECT id FROM (SELECT id FROM message_logs WHERE group_id=? AND id>? ORDER BY id ASC LIMIT 15) after_rows)`, groupID, id, groupID, id)
	default:
		return KnowledgeMessagePage{}, fmt.Errorf("context mode 必须为 message/replies/topic/window")
	}
	return knowledgeHistoryPage(q, 30)
}
