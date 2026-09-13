package memory

import (
	"context"
	"slices"
	"strings"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ConversationTopic struct {
	ID               uint         `json:"id" jsonschema:"description=已有话题 ID，新话题填0"`
	MessageIDs       []uint       `json:"message_ids" jsonschema:"description=本批归入此话题的原始消息 ID，不重复分配历史消息"`
	SourceMessageIDs []uint       `json:"source_message_ids" jsonschema:"description=完整支持摘要的已读取原文，包括仍然成立的历史依据"`
	Summary          TopicSummary `json:"summary" jsonschema:"description=保留旧摘要仍有效内容后的完整更新；title 和 gist 必填，不包含 claims"`
}

type TopicContext struct {
	ID               uint         `json:"id"`
	SummaryID        uint         `json:"-"`
	Summary          TopicSummary `json:"summary"`
	SourceMessageIDs []uint       `json:"source_message_ids"`
	SourcesValid     bool         `json:"sources_valid"`
	LatestID         uint         `json:"-"`
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
		return nil, invalidKnowledge("话题查询不能为空，请提供关键词后重试")
	}
	var ids []uint
	if err := LatestTopicSummaries(m.db.WithContext(ctx), groupID, upper).Select("ts.topic_id").Where(TopicSummaryValiditySQL).Where("strpos(lower("+TopicSummaryTextSQL+"),lower(?))>0", query).Order("ts.id DESC").Limit(6).Scan(&ids).Error; err != nil {
		return nil, err
	}
	return m.topicContexts(ctx, groupID, upper, ids)
}

func (m *Manager) topicContexts(ctx context.Context, groupID int64, upper uint, topicIDs []uint) ([]TopicContext, error) {
	result := []TopicContext{}
	for _, id := range topicIDs {
		var record struct {
			ID, LatestID uint
			SummaryJSON  string
			SourcesValid bool
		}
		db := m.db.WithContext(ctx)
		visible := LatestTopicSummaries(db, groupID, upper).Where("ts.topic_id=?", id)
		// 可见版本与全局版本必须来自同一数据库快照，后者只用于检测变化
		read := db.Table("topic_threads tt").Select(`COALESCE(v.id,0) id,COALESCE(v.summary_json::text,'') summary_json,COALESCE(v.sources_valid,false) sources_valid,
		 (SELECT COALESCE(max(s.id),0) FROM topic_summaries s JOIN topic_assignments a ON a.id=s.through_topic_assignment_id WHERE a.topic_id=tt.id) latest_id`).
			Joins("LEFT JOIN (?) v ON v.topic_id=tt.id", visible).Where("tt.group_id=? AND tt.id=?", groupID, id).Scan(&record)
		if read.Error != nil {
			return result, read.Error
		}
		if read.RowsAffected == 0 {
			continue
		}
		item := TopicContext{ID: id, SummaryID: record.ID, LatestID: record.LatestID, SourcesValid: record.SourcesValid}
		if record.ID > 0 && record.SourcesValid {
			if err := sonic.UnmarshalString(record.SummaryJSON, &item.Summary); err != nil {
				return result, err
			}
			if err := m.db.WithContext(ctx).Model(&TopicSummarySource{}).Where("summary_id=?", record.ID).Order("message_log_id").Pluck("message_log_id", &item.SourceMessageIDs).Error; err != nil {
				return result, err
			}
		}
		result = append(result, item)
	}
	return result, nil
}

// CommitConversation 是后台唯一的写入入口，话题归属、摘要和知识在同一事务中提交
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
			return ErrSnapshotChanged
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
				return ErrSnapshotChanged
			}
		}
		pending := map[uint]MessageLog{}
		for i, r := range current {
			if r.ID != rows[i].ID || r.TextContent != rows[i].TextContent || (r.RecalledAt == nil) != (rows[i].RecalledAt == nil) {
				return ErrSnapshotChanged
			}
			pending[r.ID] = r
		}
		assigned := map[uint]bool{}
		assign := func(id uint, topicID *uint) (uint, error) {
			if _, ok := pending[id]; !ok || assigned[id] {
				return 0, invalidKnowledge("消息归属缺失或重复，请确保每条消息只提交一次")
			}
			assigned[id] = true
			if r := pending[id]; topicID != nil && (r.RecalledAt != nil || strings.TrimSpace(r.TextContent) == "") {
				return 0, invalidKnowledge("话题来源原文不可用，请改用本轮已完整读取且未撤回的原文")
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
					return 0, invalidKnowledge("原有消息归属不能改写，请只提交尚未归属的消息")
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
			if (input.ID == 0 && len(input.MessageIDs) == 0) || strings.TrimSpace(input.Summary.Title) == "" || strings.TrimSpace(input.Summary.Gist) == "" {
				return invalidKnowledge("话题必须包含原文、标题和概括，请补全后重试")
			}
			id := input.ID
			var latest uint
			var newer bool
			if id == 0 {
				row := TopicThread{GroupID: batch.GroupID}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				id = row.ID
			} else {
				if seenTopics[id] {
					return invalidKnowledge("同一话题不能在一次提交中重复更新，请合并后重试")
				}
				var count int64
				if err := tx.Model(&TopicThread{}).Where("group_id=? AND id=?", batch.GroupID, id).Count(&count).Error; err != nil {
					return err
				}
				if count != 1 {
					return invalidKnowledge("话题不属于当前群，请改用本群话题或创建新话题")
				}
				if err := tx.Raw(`SELECT COALESCE(max(ts.id),0) FROM topic_summaries ts JOIN topic_assignments ta ON ta.id=ts.through_topic_assignment_id WHERE ta.topic_id=?`, id).Scan(&latest).Error; err != nil {
					return err
				}
				read := false
				for _, old := range observed.Topics {
					if old.ID == id {
						if old.LatestID != latest {
							return ErrSnapshotChanged
						}
						read = true
						newer = old.SummaryID != latest
						break
					}
				}
				if !read {
					return invalidKnowledge("话题未在本轮读取或摘要版本已变化，请重新查询后再提交")
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
			// 迁移后的消息可能已有较新的摘要，不能用较早批次覆盖
			if newer || (through > 0 && through < existingThrough) {
				continue
			}
			through = max(through, existingThrough)
			if through == 0 {
				return invalidKnowledge("话题尚无可用归属")
			}
			input.Summary.Version = 2
			input.Summary.Participants = append([]TopicParticipant{}, input.Summary.Participants...)
			input.Summary.OpenLoops = append([]string{}, input.Summary.OpenLoops...)
			input.Summary.RecentTurns = append([]string{}, input.Summary.RecentTurns...)
			input.Summary.Keywords = append([]string{}, input.Summary.Keywords...)
			input.Summary.RelatedTopics = append([]RelatedTopic{}, input.Summary.RelatedTopics...)
			body, err := sonic.MarshalString(input.Summary)
			if err != nil {
				return err
			}
			record := TopicSummaryRecord{ThroughTopicAssignmentID: through, SummaryJSON: body}
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
			if err := saveTopicSources(ctx, tx, batch, id, input, &record); err != nil {
				return err
			}
		}
		if len(assigned) != len(pending) {
			return invalidKnowledge("本批仍有消息没有归属，请为每条消息指定话题或无话题")
		}
		batch.RequireAssigned = true
		var err error
		result, err = (&Manager{db: tx}).CommitKnowledgeBatch(ctx, batch)
		return err
	})
	return result, err
}
