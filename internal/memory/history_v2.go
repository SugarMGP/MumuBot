package memory

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

type HistoryQueryV2 struct {
	ConversationKind string
	TargetID         int64
	UserID           int64
	Keywords         []string
	From             *time.Time
	To               *time.Time
	BeforeID         uint
	Limit            int
}

func (m *Manager) SearchHistoryV2(ctx context.Context, input HistoryQueryV2) ([]MessageLog, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	q := m.db.WithContext(ctx).Table("message_logs ml")
	if input.ConversationKind != "" {
		q = q.Where("ml.conversation_kind=?", input.ConversationKind)
	}
	if input.TargetID > 0 {
		q = q.Where("ml.target_id=?", input.TargetID)
	}
	if input.UserID != 0 {
		q = q.Where("ml.user_id=?", input.UserID)
	}
	if input.From != nil {
		q = q.Where("ml.message_time>=?", *input.From)
	}
	if input.To != nil {
		q = q.Where("ml.message_time<=?", *input.To)
	}
	if input.BeforeID > 0 {
		q = q.Where("ml.id<?", input.BeforeID)
	}
	keywords := normalizeKeywords(input.Keywords)
	if len(keywords) > 0 {
		var clauses []string
		var args []any
		for _, keyword := range keywords {
			clauses = append(clauses, "(lower(ml.text_content) LIKE ? OR ml.text_content % ?)")
			args = append(args, "%"+strings.ToLower(keyword)+"%", keyword)
		}
		if len(clauses) > 0 {
			q = q.Where("("+strings.Join(clauses, " OR ")+")", args...)
		}
	}
	var rows []MessageLog
	if err := q.Order("ml.id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

type HistoryRange struct {
	ConversationKind string
	Exact            bool
	TargetID         int64
	AroundMessageID  uint
	TopicID          uint
	ThroughID        uint
	Before           int
	After            int
}

func (m *Manager) ReadHistoryRange(ctx context.Context, input HistoryRange) ([]MessageLog, error) {
	if input.ConversationKind == "" || input.TargetID <= 0 {
		return nil, fmt.Errorf("历史读取需要会话来源")
	}
	if input.AroundMessageID == 0 && input.TopicID == 0 {
		return nil, fmt.Errorf("历史读取需要消息或话题")
	}
	if input.ThroughID > 0 && input.AroundMessageID > input.ThroughID {
		return nil, fmt.Errorf("消息超出当前读取快照")
	}
	before := input.Before
	after := input.After
	if !input.Exact {
		if before <= 0 {
			before = 5
		}
		if after <= 0 {
			after = 5
		}
	}
	if before > 15 {
		before = 15
	}
	if after > 15 {
		after = 15
	}
	var ids []uint
	if input.TopicID > 0 {
		var topic TopicThreadRow
		if err := m.db.WithContext(ctx).Where("id=? AND conversation_kind=? AND target_id=?", input.TopicID, input.ConversationKind, input.TargetID).Take(&topic).Error; err != nil {
			return nil, fmt.Errorf("话题不属于指定会话")
		}
		q := m.db.WithContext(ctx).Table("topic_assignments ta").Select("ta.message_log_id").Where("ta.topic_id=?", input.TopicID)
		if input.ThroughID > 0 {
			q = q.Where("ta.message_log_id<=?", input.ThroughID)
		}
		if err := q.Pluck("message_log_id", &ids).Error; err != nil {
			return nil, err
		}
		var sourceIDs []uint
		sourceQuery := m.db.WithContext(ctx).Table("topic_summary_sources src").
			Select("src.message_log_id").
			Joins("JOIN topic_summaries ts ON ts.id=src.summary_id").
			Where("ts.topic_id=?", input.TopicID)
		if input.ThroughID > 0 {
			sourceQuery = sourceQuery.Where("src.message_log_id<=?", input.ThroughID)
		}
		if err := sourceQuery.Pluck("message_log_id", &sourceIDs).Error; err != nil {
			return nil, err
		}
		ids = append(ids, sourceIDs...)
		slices.Sort(ids)
		ids = slices.Compact(ids)
		if len(ids) == 0 {
			return nil, nil
		}
	}
	var rows []MessageLog
	q := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=?", input.ConversationKind, input.TargetID)
	if input.AroundMessageID > 0 {
		var anchor MessageLog
		if err := q.Where("id=?", input.AroundMessageID).Take(&anchor).Error; err != nil {
			return nil, err
		}
		var beforeRows []MessageLog
		if before > 0 {
			qBefore := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=? AND id<?", input.ConversationKind, input.TargetID, input.AroundMessageID)
			if input.ThroughID > 0 {
				qBefore = qBefore.Where("id<=?", input.ThroughID)
			}
			if err := qBefore.Order("id DESC").Limit(before).Find(&beforeRows).Error; err != nil {
				return nil, err
			}
			slices.Reverse(beforeRows)
		}
		var afterRows []MessageLog
		if after > 0 {
			qAfter := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=? AND id>?", input.ConversationKind, input.TargetID, input.AroundMessageID)
			if input.ThroughID > 0 {
				qAfter = qAfter.Where("id<=?", input.ThroughID)
			}
			if err := qAfter.Order("id ASC").Limit(after).Find(&afterRows).Error; err != nil {
				return nil, err
			}
		}
		rows = append(rows, beforeRows...)
		rows = append(rows, anchor)
		rows = append(rows, afterRows...)
	} else {
		qTopic := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=? AND id IN ?", input.ConversationKind, input.TargetID, ids)
		if input.ThroughID > 0 {
			qTopic = qTopic.Where("id<=?", input.ThroughID)
		}
		if err := qTopic.Order("id ASC").Limit(before + after + 1).Find(&rows).Error; err != nil {
			return nil, err
		}
	}
	return rows, nil
}
