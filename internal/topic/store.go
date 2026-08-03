package topic

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/utils"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DBStore struct {
	db        *gorm.DB
	embedding memory.EmbeddingProvider
	memory    *memory.Manager
	selfID    func() int64
}

func NewDBStore(db *gorm.DB, embedding memory.EmbeddingProvider, memoryManager *memory.Manager, selfID func() int64) *DBStore {
	return &DBStore{db: db, embedding: embedding, memory: memoryManager, selfID: selfID}
}

func (s *DBStore) PersistMessageLog(ctx context.Context, item memory.MessageLog) (*memory.MessageLog, bool, error) {
	var stored memory.MessageLog
	created := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&item)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Where("one_bot_message_id = ?", item.OneBotMessageID).First(&stored).Error; err != nil {
				return err
			}
			if strings.TrimSpace(stored.TextContent) == "" {
				return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&memory.TopicAssignment{MessageLogID: stored.ID}).Error
			}
			return nil
		}
		stored = item
		created = true
		if strings.TrimSpace(stored.TextContent) == "" {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&memory.TopicAssignment{MessageLogID: stored.ID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &stored, created, nil
}

func (s *DBStore) UpdateMessagePresentation(ctx context.Context, messageLogID uint, displayContent string, forwardPayload *string, mentioned bool) (bool, error) {
	result := s.db.WithContext(ctx).Model(&memory.MessageLog{}).Where("id = ? AND recalled_at IS NULL", messageLogID).Updates(map[string]any{
		"display_content": displayContent,
		"forward_payload": forwardPayload,
		"is_mentioned":    mentioned,
	})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (s *DBStore) ListPendingTopicAssignmentMessages(ctx context.Context, groupID int64, limit int) ([]memory.MessageLog, error) {
	var rows []memory.MessageLog
	err := s.db.WithContext(ctx).Table("message_logs ml").Select("ml.*").
		Joins("LEFT JOIN topic_assignments ta ON ta.message_log_id = ml.id").
		Where("ml.group_id = ? AND ml.recalled_at IS NULL AND ta.id IS NULL AND btrim(ml.text_content) <> ''", groupID).
		Order("ml.id ASC").Limit(limit).Scan(&rows).Error
	return rows, err
}

func (s *DBStore) TopicRefForOneBotMessage(ctx context.Context, groupID, messageID int64) (topicID, messageLogID uint, err error) {
	var row struct {
		MessageLogID uint
		TopicID      *uint
	}
	err = s.db.WithContext(ctx).Table("topic_assignments ta").Select("ml.id message_log_id, ta.topic_id").
		Joins("JOIN message_logs ml ON ml.id = ta.message_log_id").
		Where("ml.group_id = ? AND ml.one_bot_message_id = ? AND ml.recalled_at IS NULL", groupID, messageID).Scan(&row).Error
	if err != nil || row.TopicID == nil {
		return 0, row.MessageLogID, err
	}
	return *row.TopicID, row.MessageLogID, nil
}

func (s *DBStore) ApplyTopicAssignmentBatch(ctx context.Context, groupID int64, sourceMessageIDs []uint, items []AssignmentBatchItem) ([]uint, error) {
	var updatedTopicIDs []uint
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var validIDs []uint
		if err := tx.Model(&memory.MessageLog{}).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("group_id = ? AND id IN ? AND recalled_at IS NULL", groupID, sourceMessageIDs).
			Order("id ASC").Pluck("id", &validIDs).Error; err != nil {
			return err
		}
		if len(validIDs) != len(sourceMessageIDs) {
			return fmt.Errorf("话题归属输入消息已变化")
		}
		valid := make(map[uint]struct{}, len(validIDs))
		for _, id := range validIDs {
			valid[id] = struct{}{}
		}
		newTopics := make(map[string]uint)
		for _, item := range items {
			if _, ok := valid[item.MessageLogID]; !ok {
				return fmt.Errorf("话题归属结果不属于当前输入")
			}
			var topicID *uint
			switch item.Action {
			case AssignmentActionNoTopic:
			case AssignmentActionReuse:
				if item.TopicID == 0 {
					continue
				}
				var count int64
				if err := tx.Model(&memory.TopicThread{}).Where("id = ? AND group_id = ?", item.TopicID, groupID).Count(&count).Error; err != nil {
					return err
				}
				if count != 1 {
					continue
				}
				id := item.TopicID
				topicID = &id
			case AssignmentActionNew:
				if item.NewTopicKey == "" {
					continue
				}
				id := newTopics[item.NewTopicKey]
				if id == 0 {
					topic := memory.TopicThread{GroupID: groupID}
					if err := tx.Create(&topic).Error; err != nil {
						return err
					}
					id = topic.ID
					newTopics[item.NewTopicKey] = id
				}
				topicID = &id
			default:
				continue
			}
			assignment := memory.TopicAssignment{MessageLogID: item.MessageLogID, TopicID: topicID}
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&assignment)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				continue
			}
			if topicID != nil {
				updatedTopicIDs = append(updatedTopicIDs, *topicID)
			}
		}
		return nil
	})
	return utils.UniqueIDs(updatedTopicIDs), err
}

func (s *DBStore) ListRecentTopicThreads(ctx context.Context, groupID int64, throughMessageLogID uint, limit int) ([]memory.TopicThread, error) {
	var rows []memory.TopicThread
	query := s.db.WithContext(ctx).Table("topic_threads tt").Select("tt.*").
		Joins("JOIN topic_assignments ta ON ta.topic_id = tt.id").
		Joins("JOIN message_logs ml ON ml.id = ta.message_log_id").
		Where("tt.group_id = ? AND ml.recalled_at IS NULL", groupID)
	if throughMessageLogID > 0 {
		query = query.Where("ml.id <= ?", throughMessageLogID)
	}
	err := query.Group("tt.id").Order("max(ml.message_time) DESC, max(ml.id) DESC").Limit(limit).Scan(&rows).Error
	return rows, err
}

func (s *DBStore) LatestTopicSummary(ctx context.Context, topicID, throughMessageLogID uint) (*memory.TopicSummaryRecord, error) {
	var row memory.TopicSummaryRecord
	query := s.db.WithContext(ctx).Table("topic_summaries ts").Select("ts.*").
		Joins("JOIN topic_assignments ta ON ta.id = ts.through_topic_assignment_id").
		Where("ta.topic_id = ?", topicID)
	if throughMessageLogID > 0 {
		query = query.Where(`NOT EXISTS (
			SELECT 1 FROM topic_assignments covered
			WHERE covered.topic_id = ta.topic_id
				AND covered.id <= ts.through_topic_assignment_id
				AND covered.message_log_id > ?
		)`, throughMessageLogID)
	}
	err := query.Order("ts.through_topic_assignment_id DESC").First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &row, err
}

func (s *DBStore) ListRecentTopicMessages(ctx context.Context, topicID, throughMessageLogID uint, limit int) ([]memory.MessageLog, error) {
	var rows []memory.MessageLog
	q := s.db.WithContext(ctx).Table("message_logs ml").Select("ml.*").
		Joins("JOIN topic_assignments ta ON ta.message_log_id = ml.id").Where("ta.topic_id = ? AND ml.recalled_at IS NULL", topicID).
		Order("ml.message_time DESC, ml.id DESC").Limit(limit)
	if throughMessageLogID > 0 {
		q = q.Where("ml.id <= ?", throughMessageLogID)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, nil
}

func (s *DBStore) ListRecentTopicParticipants(ctx context.Context, topicID, throughAssignmentID uint, limit int) ([]memory.TopicParticipantRef, error) {
	var rows []memory.TopicParticipantRef
	err := s.db.WithContext(ctx).Raw(`SELECT user_id, nickname FROM (
		SELECT DISTINCT ON (ml.user_id) ml.user_id, ml.nickname, ml.message_time, ml.id
		FROM message_logs ml JOIN topic_assignments ta ON ta.message_log_id = ml.id
		WHERE ta.topic_id = ? AND ml.recalled_at IS NULL AND (? = 0 OR ta.id <= ?)
		ORDER BY ml.user_id, ml.message_time DESC, ml.id DESC
	) latest ORDER BY message_time DESC, id DESC LIMIT ?`, topicID, throughAssignmentID, throughAssignmentID, limit).Scan(&rows).Error
	return rows, err
}

func (s *DBStore) MessagesAfterSummary(ctx context.Context, topicID uint, limit int) ([]memory.MessageLog, uint, error) {
	var watermark uint
	if err := s.db.WithContext(ctx).Table("topic_summaries ts").Select("COALESCE(max(ts.through_topic_assignment_id), 0)").
		Joins("JOIN topic_assignments ta ON ta.id = ts.through_topic_assignment_id").Where("ta.topic_id = ?", topicID).Scan(&watermark).Error; err != nil {
		return nil, 0, err
	}
	type batchRow struct {
		memory.MessageLog
		AssignmentID uint
	}
	var rows []batchRow
	err := s.db.WithContext(ctx).Raw(`WITH batch AS (
		SELECT ta.id assignment_id, ta.message_log_id FROM topic_assignments ta
		JOIN message_logs ml ON ml.id = ta.message_log_id
		WHERE ta.topic_id = ? AND ta.id > ? AND ml.recalled_at IS NULL ORDER BY ta.id ASC LIMIT ?
	)
	SELECT ml.*, batch.assignment_id FROM batch JOIN message_logs ml ON ml.id = batch.message_log_id
	ORDER BY ml.message_time ASC, ml.id ASC`, topicID, watermark, limit).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	messages := make([]memory.MessageLog, 0, len(rows))
	throughID := watermark
	for _, row := range rows {
		messages = append(messages, row.MessageLog)
		if row.AssignmentID > throughID {
			throughID = row.AssignmentID
		}
	}
	return messages, throughID, nil
}

func (s *DBStore) SaveTopicSummary(ctx context.Context, topicID, throughAssignmentID uint, sourceMessageIDs []uint, summary memory.TopicSummary) (*memory.TopicSummaryRecord, error) {
	if topicID == 0 || throughAssignmentID == 0 || len(sourceMessageIDs) == 0 {
		return nil, nil
	}
	jsonText, err := MarshalSummary(summary)
	if err != nil {
		return nil, err
	}
	embedding, err := s.embedding.Embed(ctx, summaryVectorText(summary))
	if err != nil {
		return nil, err
	}
	vector, err := memory.EmbeddingVector(embedding)
	if err != nil {
		return nil, err
	}
	record := memory.TopicSummaryRecord{ThroughTopicAssignmentID: throughAssignmentID, SummaryJSON: jsonText, Embedding: vector}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var validIDs []uint
		if err := tx.Model(&memory.MessageLog{}).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id IN ? AND recalled_at IS NULL", sourceMessageIDs).Order("id ASC").Pluck("id", &validIDs).Error; err != nil {
			return err
		}
		if len(validIDs) != len(sourceMessageIDs) {
			return fmt.Errorf("话题摘要来源消息已变化")
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error
	})
	if err != nil {
		return nil, err
	}
	if record.ID == 0 {
		if err := s.db.WithContext(ctx).Where("through_topic_assignment_id = ?", throughAssignmentID).First(&record).Error; err != nil {
			return nil, err
		}
	}
	return &record, nil
}

func (s *DBStore) topicGroupID(ctx context.Context, topicID uint) (int64, error) {
	var groupID int64
	err := s.db.WithContext(ctx).Model(&memory.TopicThread{}).Where("id = ?", topicID).Pluck("group_id", &groupID).Error
	if err != nil {
		return 0, err
	}
	if groupID == 0 {
		return 0, fmt.Errorf("话题 %d 缺少群归属", topicID)
	}
	return groupID, nil
}

func (s *DBStore) SearchTopicHits(ctx context.Context, query memory.HybridQuery, groupID int64, throughMessageLogID uint, limit int) ([]memory.TopicThread, error) {
	if query.Empty() || limit <= 0 {
		return nil, nil
	}
	latest := `SELECT DISTINCT ON (ta.topic_id) ts.id, ta.topic_id, ts.summary_json, ts.embedding
		FROM topic_summaries ts JOIN topic_assignments ta ON ta.id = ts.through_topic_assignment_id
		JOIN topic_threads tt ON tt.id = ta.topic_id
		WHERE tt.group_id = ?`
	latestArgs := []any{groupID}
	if throughMessageLogID > 0 {
		latest += ` AND NOT EXISTS (
			SELECT 1 FROM topic_assignments covered
			WHERE covered.topic_id = ta.topic_id
				AND covered.id <= ts.through_topic_assignment_id
				AND covered.message_log_id > ?
		)`
		latestArgs = append(latestArgs, throughMessageLogID)
	}
	latest += ` ORDER BY ta.topic_id, ts.through_topic_assignment_id DESC`
	var vectorRows []struct{ TopicID uint }
	vectorArgs := append(append([]any(nil), latestArgs...), query.Vector(), query.Vector())
	if err := s.db.WithContext(ctx).Raw(`SELECT topic_id FROM (`+latest+`) latest
		WHERE 1 - (embedding <=> ?) >= 0.3 ORDER BY embedding <=> ? LIMIT 20`, vectorArgs...).Scan(&vectorRows).Error; err != nil {
		return nil, err
	}
	var textRows []struct{ TopicID uint }
	textArgs := append([]any{query.FragmentArray()}, latestArgs...)
	textArgs = append(textArgs, 0.1)
	if err := s.db.WithContext(ctx).Raw(`SELECT topic_id FROM (
		SELECT topic_id, (SELECT max(greatest(
			word_similarity(fragment, latest.summary_json::text),
			word_similarity(latest.summary_json::text, fragment)
		))
			FROM unnest(?::text[]) AS fragments(fragment)) score
		FROM (`+latest+`) latest
	) ranked WHERE score >= ? ORDER BY score DESC LIMIT 20`, textArgs...).Scan(&textRows).Error; err != nil {
		return nil, err
	}
	vectorIDs := make([]uint, len(vectorRows))
	for i, row := range vectorRows {
		vectorIDs[i] = row.TopicID
	}
	textIDs := make([]uint, len(textRows))
	for i, row := range textRows {
		textIDs[i] = row.TopicID
	}
	items := fuseTopicRanks(limit, vectorIDs, textIDs)
	hits := make([]memory.TopicThread, 0, len(items))
	for _, id := range items {
		hits = append(hits, memory.TopicThread{ID: id, GroupID: groupID})
	}
	return hits, nil
}

func fuseTopicRanks(limit int, lists ...[]uint) []uint {
	scores := make(map[uint]float64)
	for _, list := range lists {
		for i, id := range list {
			scores[id] += 1 / float64(61+i)
		}
	}
	items := make([]uint, 0, len(scores))
	for id := range scores {
		items = append(items, id)
	}
	sort.Slice(items, func(i, j int) bool {
		if scores[items[i]] == scores[items[j]] {
			return items[i] < items[j]
		}
		return scores[items[i]] > scores[items[j]]
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func (s *DBStore) ListTopicsNeedingSummary(ctx context.Context, minMessages int, coldBefore time.Time, limit int) ([]uint, error) {
	var ids []uint
	err := s.db.WithContext(ctx).Raw(`SELECT ta.topic_id FROM topic_assignments ta
		JOIN message_logs ml ON ml.id = ta.message_log_id
		LEFT JOIN (SELECT ta2.topic_id, max(ts.through_topic_assignment_id) watermark
			FROM topic_summaries ts JOIN topic_assignments ta2 ON ta2.id = ts.through_topic_assignment_id GROUP BY ta2.topic_id) s ON s.topic_id = ta.topic_id
		WHERE ta.topic_id IS NOT NULL AND ta.id > COALESCE(s.watermark, 0) AND ml.recalled_at IS NULL
		GROUP BY ta.topic_id HAVING count(*) >= ? OR max(ml.message_time) < ?
		ORDER BY min(ta.id) LIMIT ?`, minMessages, coldBefore, limit).Scan(&ids).Error
	return ids, err
}

func (s *DBStore) ListUnprocessedSummaries(ctx context.Context, limit int) ([]memory.TopicSummaryRecord, error) {
	var rows []memory.TopicSummaryRecord
	err := s.db.WithContext(ctx).Where("memory_processed = false").Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (s *DBStore) ProcessTopicSummaryMemory(ctx context.Context, record memory.TopicSummaryRecord) error {
	selfID := s.selfID()
	if selfID <= 0 {
		return fmt.Errorf("OneBot机器人账号尚未就绪")
	}
	topicID, err := s.TopicIDForSummary(ctx, record)
	if err != nil {
		return err
	}
	groupID, err := s.topicGroupID(ctx, topicID)
	if err != nil {
		return err
	}
	participants, err := s.ListRecentTopicParticipants(ctx, topicID, record.ThroughTopicAssignmentID, TailKeepMessages)
	if err != nil {
		return err
	}
	summary := ParseSummary(record.SummaryJSON)
	claims := make([]memory.TopicMemoryClaimInput, 0, len(summary.Facts)+len(summary.OpenLoops))
	for _, claim := range summary.Facts {
		claims = append(claims, memory.TopicMemoryClaimInput{Content: claim})
	}
	for _, claim := range summary.OpenLoops {
		claims = append(claims, memory.TopicMemoryClaimInput{Content: claim, AllowedKinds: []memory.MemoryKind{memory.MemoryKindGoal}})
	}
	_, err = s.memory.UpsertTopicMemoryCandidate(ctx, memory.TopicMemoryCandidateInput{
		GroupID: groupID, TopicSummaryID: record.ID, SelfID: selfID, Claims: claims, Participants: participants,
	})
	return err
}

func (s *DBStore) TopicIDForSummary(ctx context.Context, record memory.TopicSummaryRecord) (uint, error) {
	var topicID uint
	err := s.db.WithContext(ctx).Model(&memory.TopicAssignment{}).Where("id = ? AND topic_id IS NOT NULL", record.ThroughTopicAssignmentID).Pluck("topic_id", &topicID).Error
	if err != nil || topicID == 0 {
		return 0, fmt.Errorf("摘要 %d 缺少话题归属", record.ID)
	}
	return topicID, nil
}
