package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UpdateTopicInput struct {
	TopicID  uint
	Summary  *TopicSummary
	Messages []TopicMessageRef
	Status   string
}

type UpdateTopicResult struct {
	TopicID        uint
	SummaryVersion uint64
	Status         string
	AssignedCount  int
}

// UpdateTopic 主 Agent 直接维护话题索引，不改变检查点和当前摘要头
func (m *Manager) UpdateTopic(ctx context.Context, kind string, targetID int64, input UpdateTopicInput) (*UpdateTopicResult, error) {
	if kind == "" || targetID <= 0 {
		return nil, fmt.Errorf("话题会话参数无效")
	}
	status := strings.TrimSpace(input.Status)
	if status != "" && status != "active" && status != "dormant" {
		return nil, fmt.Errorf("话题状态只能是 active 或 dormant")
	}
	if input.TopicID == 0 && input.Summary == nil {
		return nil, fmt.Errorf("新建话题必须提供摘要")
	}
	if input.TopicID != 0 && input.Summary == nil && status == "" {
		return nil, fmt.Errorf("至少需要摘要、消息归属或状态变更")
	}
	if input.Summary != nil {
		if err := ValidateTopicSummary(*input.Summary); err != nil {
			return nil, err
		}
	}
	result := &UpdateTopicResult{}
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var topic TopicThreadRow
		if input.TopicID != 0 {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id=? AND conversation_kind=? AND target_id=?", input.TopicID, kind, targetID).Take(&topic).Error; err != nil {
				return fmt.Errorf("话题不存在或不属于当前会话")
			}
		} else {
			topic = TopicThreadRow{ConversationKind: kind, TargetID: targetID, Status: "dormant"}
			if status != "" {
				topic.Status = status
			}
			if err := tx.Create(&topic).Error; err != nil {
				return err
			}
		}
		result.TopicID = topic.ID
		result.Status = topic.Status
		if status != "" && topic.Status != status {
			if err := tx.Model(&TopicThreadRow{}).Where("id=?", topic.ID).Updates(map[string]any{"status": status, "updated_at": time.Now()}).Error; err != nil {
				return err
			}
			topic.Status = status
			result.Status = status
		}
		var summaryRow TopicSummaryRow
		if input.Summary != nil {
			var latest TopicSummaryRow
			err := tx.Where("topic_id=?", topic.ID).Order("version DESC").Take(&latest).Error
			nextVersion := uint64(1)
			if err == nil {
				nextVersion = latest.Version + 1
			} else if err != gorm.ErrRecordNotFound {
				return err
			}
			body, err := sonic.MarshalString(*input.Summary)
			if err != nil {
				return err
			}
			summaryRow = TopicSummaryRow{TopicID: topic.ID, Version: nextVersion, SummaryJSON: body}
			if err := tx.Create(&summaryRow).Error; err != nil {
				return err
			}
			result.SummaryVersion = nextVersion
		}
		if len(input.Messages) > 0 {
			if summaryRow.ID == 0 {
				return fmt.Errorf("更新消息归属时必须同时提供话题摘要")
			}
			ids := make([]int64, 0, len(input.Messages))
			for _, ref := range input.Messages {
				if ref.MessageID == 0 {
					return fmt.Errorf("话题 message_id 不能为空")
				}
				ids = append(ids, ref.MessageID)
			}
			var rows []MessageLog
			if err := tx.Where("conversation_kind=? AND target_id=? AND one_bot_message_id IN ?", kind, targetID, ids).Find(&rows).Error; err != nil {
				return err
			}
			byOneBot := make(map[int64]uint, len(rows))
			for _, row := range rows {
				byOneBot[row.OneBotMessageID] = row.ID
			}
			assignments := make([]TopicAssignment, 0, len(input.Messages))
			sources := make([]TopicSourceRow, 0, len(input.Messages))
			for _, ref := range input.Messages {
				messageID, ok := byOneBot[ref.MessageID]
				if !ok {
					return fmt.Errorf("消息 %d 不属于当前会话", ref.MessageID)
				}
				topicID := topic.ID
				assignments = append(assignments, TopicAssignment{MessageLogID: messageID, TopicID: &topicID})
				sources = append(sources, TopicSourceRow{SummaryID: summaryRow.ID, MessageLogID: messageID, IsAnchor: ref.IsAnchor})
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "message_log_id"}},
				DoUpdates: clause.Assignments(map[string]any{"topic_id": gorm.Expr("EXCLUDED.topic_id")}),
			}).CreateInBatches(assignments, 500).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(sources, 500).Error; err != nil {
				return err
			}
			result.AssignedCount = len(assignments)
		}
		if input.Summary == nil && status == "" {
			return nil
		}
		return tx.Model(&TopicThreadRow{}).Where("id=?", topic.ID).Update("updated_at", time.Now()).Error
	})
	if err != nil {
		return nil, err
	}
	if status != "" {
		result.Status = status
	}
	return result, nil
}
