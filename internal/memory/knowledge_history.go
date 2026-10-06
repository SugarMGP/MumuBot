package memory

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

func (m *Manager) knowledgeHistoryQuery(ctx context.Context, kind string, targetID int64, upper uint) *gorm.DB {
	if kind == "" {
		kind = ConversationKindGroup
	}
	return m.db.WithContext(ctx).Table("message_logs ml").Select("ml.*").Where("ml.conversation_kind=? AND ml.target_id=? AND ml.id<=? AND ml.recalled_at IS NULL AND "+OriginalMessageTextSQL+"<>''", kind, targetID, upper)
}

func knowledgeHistoryPage(q *gorm.DB, limit int, newestFirst bool) (KnowledgeMessagePage, error) {
	if limit <= 0 || limit > 30 {
		limit = 30
	}
	var rows []MessageLog
	order := "ml.id ASC"
	if newestFirst {
		order = "ml.id DESC"
	}
	if err := q.Order(order).Limit(limit + 1).Scan(&rows).Error; err != nil {
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
	if input.TargetID <= 0 || input.ThroughID == 0 {
		return KnowledgeMessagePage{}, fmt.Errorf("无效历史范围")
	}
	q := m.knowledgeHistoryQuery(ctx, input.ConversationKind, input.TargetID, input.ThroughID).Where("ml.id>?", input.AfterID)
	if input.BeforeID > 0 {
		q = q.Where("ml.id<?", input.BeforeID)
	}
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
	return knowledgeHistoryPage(q, input.Limit, input.NewestFirst)
}

func (m *Manager) ReadKnowledgeContext(ctx context.Context, groupID int64, upper, id, after uint, mode string) (KnowledgeMessagePage, error) {
	return m.ReadKnowledgeContextScope(ctx, ConversationKindGroup, groupID, upper, id, after, mode)
}

// ReadKnowledgeContextScope 只读取当前会话固定上界内的原文及回复上下文
func (m *Manager) ReadKnowledgeContextScope(ctx context.Context, kind string, targetID int64, upper, id, after uint, mode string) (KnowledgeMessagePage, error) {
	if targetID <= 0 || upper == 0 || (kind != ConversationKindGroup && kind != ConversationKindPrivate) {
		return KnowledgeMessagePage{}, fmt.Errorf("无效历史范围")
	}
	var target MessageLog
	if err := m.knowledgeHistoryQuery(ctx, kind, targetID, upper).Where("ml.id=?", id).Take(&target).Error; err != nil {
		return KnowledgeMessagePage{}, err
	}
	q := m.knowledgeHistoryQuery(ctx, kind, targetID, upper).Where("ml.id>?", after)
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
		if kind != ConversationKindGroup {
			return KnowledgeMessagePage{}, fmt.Errorf("私聊请使用 message、replies 或 window 模式")
		}
		q = q.Where("EXISTS (SELECT 1 FROM topic_assignments a JOIN topic_assignments b ON a.topic_id=b.topic_id WHERE a.message_log_id=ml.id AND b.message_log_id=?)", id)
	case "window":
		q = q.Where(`ml.id IN (SELECT id FROM (SELECT id FROM message_logs WHERE conversation_kind=? AND target_id=? AND id<=? ORDER BY id DESC LIMIT 15) before_rows UNION SELECT id FROM (SELECT id FROM message_logs WHERE conversation_kind=? AND target_id=? AND id>? AND id<=? ORDER BY id ASC LIMIT 15) after_rows)`, kind, targetID, id, kind, targetID, id, upper)
	default:
		return KnowledgeMessagePage{}, fmt.Errorf("读取模式必须为 message、replies、topic 或 window，请修正后重试")
	}
	return knowledgeHistoryPage(q, 30, false)
}
