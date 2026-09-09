package memory

import (
	"context"
	"fmt"
	"github.com/bytedance/sonic"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
)

type ConversationTopic struct {
	ID         uint         `json:"id" jsonschema:"description=已有话题 ID，新话题填0"`
	MessageIDs []uint       `json:"message_ids" jsonschema:"description=本批归入此话题的原始消息 ID，不重复分配历史消息"`
	Summary    TopicSummary `json:"summary" jsonschema:"description=保留旧摘要仍有效内容后的完整更新；title 和 gist 必填，不包含 claims"`
}

type TopicContext struct {
	ID        uint         `json:"id"`
	SummaryID uint         `json:"-"`
	Summary   TopicSummary `json:"summary"`
}

type ConversationContext struct {
	Topics      []TopicContext `json:"topics"`
	Assignments map[uint]uint  `json:"existing_assignments"`
}

func (m *Manager) ConversationContext(ctx context.Context, groupID int64, upper uint, rows []MessageLog) (ConversationContext, error) {
	result := ConversationContext{Assignments: map[uint]uint{}}
	ids := messageLogIDs(rows)
	var assignments []TopicAssignment
	if len(ids) > 0 {
		if err := m.db.WithContext(ctx).Where("message_log_id IN ?", ids).Find(&assignments).Error; err != nil {
			return result, err
		}
	}
	topicIDs := []uint{}
	add := func(id uint) {
		if id > 0 && !slices.Contains(topicIDs, id) {
			topicIDs = append(topicIDs, id)
		}
	}
	for _, a := range assignments {
		result.Assignments[a.MessageLogID] = 0
		if a.TopicID != nil {
			result.Assignments[a.MessageLogID] = *a.TopicID
			add(*a.TopicID)
		}
	}
	for _, r := range rows {
		if r.ReplyToMessageID == nil {
			continue
		}
		var id uint
		if err := m.db.WithContext(ctx).Raw(`SELECT COALESCE(ta.topic_id,0) FROM topic_assignments ta JOIN message_logs ml ON ml.id=ta.message_log_id WHERE ml.group_id=? AND ml.one_bot_message_id=? AND ml.id<=? AND ml.recalled_at IS NULL`, groupID, *r.ReplyToMessageID, upper).Scan(&id).Error; err != nil {
			return result, err
		}
		add(id)
	}
	var recent []uint
	if err := m.db.WithContext(ctx).Raw(`SELECT ta.topic_id FROM topic_assignments ta JOIN message_logs ml ON ml.id=ta.message_log_id WHERE ml.group_id=? AND ml.id<=? AND ta.topic_id IS NOT NULL AND ml.recalled_at IS NULL GROUP BY ta.topic_id ORDER BY max(ml.id) DESC LIMIT 6`, groupID, upper).Scan(&recent).Error; err != nil {
		return result, err
	}
	for _, id := range recent {
		add(id)
	}
	var err error
	result.Topics, err = m.topicContexts(ctx, groupID, upper, topicIDs)
	return result, err
}

func (m *Manager) SearchConversationTopics(ctx context.Context, groupID int64, upper uint, query string) ([]TopicContext, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("topic query cannot be empty")
	}
	var ids []uint
	if err := m.db.WithContext(ctx).Raw(`SELECT ta.topic_id FROM topic_summaries ts JOIN topic_assignments ta ON ta.id=ts.through_topic_assignment_id JOIN topic_threads tt ON tt.id=ta.topic_id WHERE tt.group_id=? AND NOT EXISTS(SELECT 1 FROM topic_assignments a WHERE a.topic_id=ta.topic_id AND a.id<=ts.through_topic_assignment_id AND a.message_log_id>?) AND strpos(lower(ts.summary_json::text),lower(?))>0 GROUP BY ta.topic_id ORDER BY max(ts.id) DESC LIMIT 6`, groupID, upper, query).Scan(&ids).Error; err != nil {
		return nil, err
	}
	return m.topicContexts(ctx, groupID, upper, ids)
}

func (m *Manager) topicContexts(ctx context.Context, groupID int64, upper uint, topicIDs []uint) ([]TopicContext, error) {
	result := []TopicContext{}
	for _, id := range topicIDs {
		var record TopicSummaryRecord
		err := m.db.WithContext(ctx).Table("topic_summaries ts").Select("ts.*").Joins("JOIN topic_assignments ta ON ta.id=ts.through_topic_assignment_id JOIN topic_threads tt ON tt.id=ta.topic_id").Where("tt.group_id=? AND ta.topic_id=?", groupID, id).Where("NOT EXISTS(SELECT 1 FROM topic_assignments a WHERE a.topic_id=ta.topic_id AND a.id<=ts.through_topic_assignment_id AND a.message_log_id>?)", upper).Order("ts.through_topic_assignment_id DESC").Limit(1).Find(&record).Error
		if err != nil {
			return result, err
		}
		item := TopicContext{ID: id, SummaryID: record.ID}
		if err := m.db.WithContext(ctx).Raw(`SELECT COALESCE(max(ts.id),0) FROM topic_summaries ts JOIN topic_assignments ta ON ta.id=ts.through_topic_assignment_id JOIN topic_threads tt ON tt.id=ta.topic_id WHERE tt.group_id=? AND ta.topic_id=?`, groupID, id).Scan(&item.SummaryID).Error; err != nil {
			return result, err
		}
		if record.ID > 0 {
			if err := sonic.UnmarshalString(record.SummaryJSON, &item.Summary); err != nil {
				return result, err
			}
		}
		result = append(result, item)
	}
	return result, nil
}

// CommitConversation is the only background write boundary: topic, summary and knowledge commit together.
func (m *Manager) CommitConversation(ctx context.Context, batch KnowledgeBatch, rows []MessageLog, observed ConversationContext, topics []ConversationTopic, noTopic []uint) (*KnowledgeCommitResult, error) {
	var result *KnowledgeCommitResult
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := LockKnowledgeGroup(tx, batch.GroupID); err != nil {
			return err
		}
		var current []MessageLog
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("group_id=? AND id>? AND id<=?", batch.GroupID, batch.AfterID, batch.ThroughID).Order("id").Find(&current).Error; err != nil {
			return err
		}
		if len(current) != len(rows) {
			return fmt.Errorf("conversation batch changed")
		}
		readIDs := slices.Clone(batch.ReadMessageIDs)
		slices.Sort(readIDs)
		readIDs = slices.Compact(readIDs)
		if len(readIDs) > 0 {
			var valid []uint
			if err := tx.Raw("SELECT id FROM message_logs WHERE group_id=? AND id IN ? AND id<=? AND recalled_at IS NULL ORDER BY id FOR UPDATE", batch.GroupID, readIDs, batch.ThroughID).Scan(&valid).Error; err != nil {
				return err
			}
			if len(valid) != len(readIDs) {
				return fmt.Errorf("read context changed before commit")
			}
		}
		pending := map[uint]MessageLog{}
		for i, r := range current {
			if r.ID != rows[i].ID || r.TextContent != rows[i].TextContent || (r.RecalledAt == nil) != (rows[i].RecalledAt == nil) {
				return fmt.Errorf("conversation source changed")
			}
			pending[r.ID] = r
		}
		assigned := map[uint]bool{}
		assign := func(id uint, topicID *uint) (uint, error) {
			if _, ok := pending[id]; !ok || assigned[id] {
				return 0, fmt.Errorf("unknown or duplicate message assignment")
			}
			assigned[id] = true
			if r := pending[id]; topicID != nil && (r.RecalledAt != nil || strings.TrimSpace(r.TextContent) == "") {
				return 0, fmt.Errorf("unusable topic source")
			}
			var old TopicAssignment
			if err := tx.Where("message_log_id=?", id).Limit(1).Find(&old).Error; err != nil {
				return 0, err
			}
			if old.ID > 0 {
				if row := pending[id]; topicID == nil && (row.RecalledAt != nil || strings.TrimSpace(row.TextContent) == "") {
					return old.ID, nil
				}
				if (old.TopicID == nil) != (topicID == nil) || (topicID != nil && *old.TopicID != *topicID) {
					return 0, fmt.Errorf("existing assignment cannot be rewritten")
				}
				return old.ID, nil
			}
			row := TopicAssignment{MessageLogID: id, TopicID: topicID}
			if err := tx.Create(&row).Error; err != nil {
				return 0, err
			}
			return row.ID, nil
		}
		for _, id := range noTopic {
			if _, err := assign(id, nil); err != nil {
				return err
			}
		}
		seenTopics := map[uint]bool{}
		for _, input := range topics {
			if len(input.MessageIDs) == 0 || strings.TrimSpace(input.Summary.Title) == "" || strings.TrimSpace(input.Summary.Gist) == "" {
				return fmt.Errorf("topic requires messages, title and gist")
			}
			id := input.ID
			var latest uint
			if id == 0 {
				row := TopicThread{GroupID: batch.GroupID}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				id = row.ID
			} else {
				if seenTopics[id] {
					return fmt.Errorf("duplicate topic update")
				}
				var count int64
				if err := tx.Model(&TopicThread{}).Where("group_id=? AND id=?", batch.GroupID, id).Count(&count).Error; err != nil {
					return err
				}
				if count != 1 {
					return fmt.Errorf("topic is outside group")
				}
				if err := tx.Raw(`SELECT COALESCE(max(ts.id),0) FROM topic_summaries ts JOIN topic_assignments ta ON ta.id=ts.through_topic_assignment_id WHERE ta.topic_id=?`, id).Scan(&latest).Error; err != nil {
					return err
				}
				read := false
				for _, old := range observed.Topics {
					if old.ID == id && old.SummaryID == latest {
						read = true
						break
					}
				}
				if !read {
					return fmt.Errorf("topic was not read or summary changed")
				}
			}
			seenTopics[id] = true
			var through uint
			for _, messageID := range input.MessageIDs {
				a, err := assign(messageID, &id)
				if err != nil {
					return err
				}
				through = max(through, a)
			}
			var existingThrough uint
			if latest > 0 {
				if err := tx.Model(&TopicSummaryRecord{}).Select("through_topic_assignment_id").Where("id=?", latest).Scan(&existingThrough).Error; err != nil {
					return err
				}
			}
			// Migrated messages may already have a newer summary; never replace it with an older batch.
			if through <= existingThrough {
				continue
			}
			input.Summary.Version = 1
			input.Summary.Participants = append([]TopicParticipant{}, input.Summary.Participants...)
			input.Summary.OpenLoops = append([]string{}, input.Summary.OpenLoops...)
			input.Summary.RecentTurns = append([]string{}, input.Summary.RecentTurns...)
			input.Summary.Keywords = append([]string{}, input.Summary.Keywords...)
			body, err := sonic.MarshalString(input.Summary)
			if err != nil {
				return err
			}
			if err := tx.Create(&TopicSummaryRecord{ThroughTopicAssignmentID: through, SummaryJSON: body}).Error; err != nil {
				return err
			}
		}
		if len(assigned) != len(pending) {
			return fmt.Errorf("not every batch message has an assignment")
		}
		batch.RequireAssigned = true
		var err error
		result, err = (&Manager{db: tx}).CommitKnowledgeBatch(ctx, batch)
		return err
	})
	return result, err
}
