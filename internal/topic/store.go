package topic

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/vector"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DBStore struct {
	db              *gorm.DB
	embedding       memory.EmbeddingProvider
	topicMilvus     memory.VectorStore
	memoryCandidate memory.MemoryCandidateWriter
}

func NewDBStore(db *gorm.DB, embedding memory.EmbeddingProvider, topicMilvus memory.VectorStore, memoryCandidate memory.MemoryCandidateWriter) *DBStore {
	return &DBStore{
		db:              db,
		embedding:       embedding,
		topicMilvus:     topicMilvus,
		memoryCandidate: memoryCandidate,
	}
}

const summaryVectorType = "topic_summary"

func (s *DBStore) ArchiveTopicThreadForRepair(ctx context.Context, groupID int64, topicID uint) error {
	if topicID == 0 {
		return nil
	}
	return s.db.WithContext(ctx).
		Model(&memory.TopicThread{}).
		Where("id = ? AND group_id = ? AND status = ?", topicID, groupID, memory.TopicThreadStatusActive).
		Updates(map[string]any{
			"status":     memory.TopicThreadStatusArchived,
			"updated_at": time.Now(),
		}).Error
}

func (s *DBStore) UpdateTopicSummary(ctx context.Context, topicID uint, summary memory.TopicSummary, summaryUntil uint, capturedAt time.Time) error {
	if topicID == 0 {
		return nil
	}

	summaryJSON, err := MarshalSummary(summary)
	if err != nil {
		return err
	}
	updated := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var topic memory.TopicThread
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&topic, topicID).Error; err != nil {
			return err
		}
		if summaryUntil <= topic.SummaryUntilMessageLogID {
			return nil
		}

		historyJSON, err := AppendSummaryHistory(topic.SummaryHistoryJSON, summary, capturedAt)
		if err != nil {
			return err
		}

		if err := tx.Model(&memory.TopicThread{}).
			Where("id = ?", topicID).
			Updates(map[string]any{
				"summary_json":                 summaryJSON,
				"summary_history_json":         historyJSON,
				"summary_until_message_log_id": summaryUntil,
				"updated_at":                   time.Now(),
			}).Error; err != nil {
			return err
		}
		updated = true
		return nil
	})
	if err != nil || !updated {
		return err
	}

	topic, fetchErr := s.GetTopicThread(ctx, topicID)
	if fetchErr == nil && topic != nil && s.memoryCandidate != nil {
		parsed := ParseSummary(topic.SummaryJSON)
		participants, participantsErr := s.ListRecentTopicParticipants(ctx, topic.ID, TailKeepMessages)
		if participantsErr != nil {
			zap.L().Warn("读取话题参与者失败，长期记忆主体解析可能退化", zap.Uint("topic_id", topic.ID), zap.Error(participantsErr))
		}
		if len(parsed.Facts) > 0 {
			_, upsertErr := s.memoryCandidate.UpsertTopicMemoryCandidate(ctx, memory.TopicMemoryCandidateInput{
				GroupID:      topic.GroupID,
				TopicID:      topic.ID,
				SelfID:       config.Get().Persona.QQ,
				Claims:       parsed.Facts,
				Participants: participants,
			})
			if upsertErr != nil {
				zap.L().Warn("话题事实下沉长期记忆失败", zap.Uint("topic_id", topic.ID), zap.Error(upsertErr))
			}
		}
		if len(parsed.OpenLoops) > 0 {
			_, upsertErr := s.memoryCandidate.UpsertTopicMemoryCandidate(ctx, memory.TopicMemoryCandidateInput{
				GroupID:               topic.GroupID,
				TopicID:               topic.ID,
				SelfID:                config.Get().Persona.QQ,
				Claims:                parsed.OpenLoops,
				Participants:          participants,
				AllowedCanonicalTypes: []memory.CanonicalMemoryType{memory.CanonicalMemoryTypeGoal},
			})
			if upsertErr != nil {
				zap.L().Warn("话题待办下沉长期目标失败", zap.Uint("topic_id", topic.ID), zap.Error(upsertErr))
			}
		}
	}

	if err := s.SyncTopicThreadVector(ctx, topicID); err != nil {
		zap.L().Warn("更新话题摘要后同步向量失败", zap.Uint("topic_id", topicID), zap.Error(err))
	}
	return nil
}

func (s *DBStore) ListArchivedTopicThreadsNeedingSummary(ctx context.Context, groupID int64) ([]memory.TopicThread, error) {
	var topics []memory.TopicThread
	err := s.db.WithContext(ctx).
		Where("group_id = ? AND status = ? AND summary_until_message_log_id < last_message_log_id", groupID, memory.TopicThreadStatusArchived).
		Order("last_message_log_id ASC").
		Order("id ASC").
		Find(&topics).Error
	return topics, err
}

func (s *DBStore) ListActiveTopicThreads(ctx context.Context, groupID int64) ([]memory.TopicThread, error) {
	var topics []memory.TopicThread
	err := s.db.WithContext(ctx).
		Where("group_id = ? AND status = ?", groupID, memory.TopicThreadStatusActive).
		Order("last_message_log_id DESC").
		Order("id DESC").
		Find(&topics).Error
	return topics, err
}

func (s *DBStore) GetTopicThread(ctx context.Context, topicID uint) (*memory.TopicThread, error) {
	var topic memory.TopicThread
	if err := s.db.WithContext(ctx).First(&topic, topicID).Error; err != nil {
		return nil, err
	}
	return &topic, nil
}

func (s *DBStore) SearchArchivedTopicThreadHits(ctx context.Context, query string, groupID int64, topK int, threshold float64) ([]ThreadSearchHit, error) {
	if s.embedding == nil || s.topicMilvus == nil {
		return nil, nil
	}
	if topK <= 0 {
		topK = 8
	}

	results, err := s.searchArchivedTopicSummaries(ctx, query, groupID, topK, threshold)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}

	ids := make([]uint, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.MemoryID)
	}

	var topics []memory.TopicThread
	if err := s.db.WithContext(ctx).
		Where("id IN ? AND group_id = ? AND status = ?", ids, groupID, memory.TopicThreadStatusArchived).
		Find(&topics).Error; err != nil {
		return nil, err
	}

	topicByID := make(map[uint]memory.TopicThread, len(topics))
	for _, topic := range topics {
		topicByID[topic.ID] = topic
	}

	hits := make([]ThreadSearchHit, 0, len(results))
	for _, result := range results {
		if topic, ok := topicByID[result.MemoryID]; ok {
			hits = append(hits, ThreadSearchHit{
				Topic: topic,
				Score: float64(result.Score),
			})
		}
	}
	return hits, nil
}

func (s *DBStore) GetTopicMessagesAfterSummary(ctx context.Context, topicID uint, limit int) ([]memory.MessageLog, error) {
	topic, err := s.GetTopicThread(ctx, topicID)
	if err != nil {
		return nil, err
	}

	query := s.db.WithContext(ctx).
		Where("group_id = ? AND topic_thread_id = ? AND id > ?", topic.GroupID, topic.ID, topic.SummaryUntilMessageLogID).
		Order("id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}

	var logs []memory.MessageLog
	if err := query.Find(&logs).Error; err != nil {
		return nil, err
	}
	return logs, nil
}

func (s *DBStore) CountTopicMessagesAfterSummary(ctx context.Context, topicID uint) (int, error) {
	topic, err := s.GetTopicThread(ctx, topicID)
	if err != nil {
		return 0, err
	}

	var count int64
	if err := s.db.WithContext(ctx).
		Model(&memory.MessageLog{}).
		Where("group_id = ? AND topic_thread_id = ? AND id > ?", topic.GroupID, topic.ID, topic.SummaryUntilMessageLogID).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

func (s *DBStore) ListRecentTopicMessages(ctx context.Context, topicID uint, limit int) ([]memory.MessageLog, error) {
	if limit <= 0 {
		limit = TailKeepMessages
	}

	var logs []memory.MessageLog
	if err := s.db.WithContext(ctx).
		Where("topic_thread_id = ?", topicID).
		Order("id DESC").
		Limit(limit).
		Find(&logs).Error; err != nil {
		return nil, err
	}

	for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
		logs[i], logs[j] = logs[j], logs[i]
	}
	return logs, nil
}

func (s *DBStore) ListRecentTopicMessagesByTopicIDs(ctx context.Context, topicIDs []uint, limit int) (map[uint][]memory.MessageLog, error) {
	result := make(map[uint][]memory.MessageLog, len(topicIDs))
	if len(topicIDs) == 0 {
		return result, nil
	}
	if limit <= 0 {
		limit = TailKeepMessages
	}

	parts := make([]string, 0, len(topicIDs))
	args := make([]any, 0, len(topicIDs)*2)
	tableName := memory.MessageLog{}.TableName()
	for i, topicID := range topicIDs {
		parts = append(parts, fmt.Sprintf(
			"SELECT * FROM (SELECT * FROM %s WHERE topic_thread_id = ? ORDER BY id DESC LIMIT ?) AS topic_logs_%d",
			tableName,
			i,
		))
		args = append(args, topicID, limit)
	}
	var logs []memory.MessageLog
	query := strings.Join(parts, " UNION ALL ") + " ORDER BY topic_thread_id ASC, id DESC"
	if err := s.db.WithContext(ctx).Raw(query, args...).Scan(&logs).Error; err != nil {
		return nil, err
	}

	for _, log := range logs {
		if len(result[log.TopicThreadID]) >= limit {
			continue
		}
		result[log.TopicThreadID] = append(result[log.TopicThreadID], log)
	}
	for topicID, topicLogs := range result {
		for i, j := 0, len(topicLogs)-1; i < j; i, j = i+1, j-1 {
			topicLogs[i], topicLogs[j] = topicLogs[j], topicLogs[i]
		}
		result[topicID] = topicLogs
	}
	return result, nil
}

func (s *DBStore) ListRecentTopicParticipants(ctx context.Context, topicID uint, limit int) ([]memory.TopicParticipantRef, error) {
	if limit <= 0 {
		limit = TailKeepMessages
	}

	participants := make([]memory.TopicParticipantRef, 0, limit)
	seen := make(map[int64]struct{}, limit)
	lastID := uint(0)
	pageSize := max(limit*3, 16)
	for len(participants) < limit {
		var logs []memory.MessageLog
		query := s.db.WithContext(ctx).
			Where("topic_thread_id = ?", topicID).
			Order("id DESC").
			Limit(pageSize)
		if lastID > 0 {
			query = query.Where("id < ?", lastID)
		}
		if err := query.Find(&logs).Error; err != nil {
			return nil, err
		}
		if len(logs) == 0 {
			break
		}
		for _, log := range logs {
			if _, ok := seen[log.UserID]; ok {
				continue
			}
			seen[log.UserID] = struct{}{}
			participants = append(participants, memory.TopicParticipantRef{
				UserID:   log.UserID,
				Nickname: log.Nickname,
			})
			if len(participants) >= limit {
				break
			}
		}
		lastID = logs[len(logs)-1].ID
		if len(logs) < pageSize {
			break
		}
	}
	return participants, nil
}

func (s *DBStore) CreateMessageLog(ctx context.Context, msg memory.MessageLog) (*memory.MessageLog, error) {
	if err := s.db.WithContext(ctx).Create(&msg).Error; err != nil {
		return nil, err
	}
	return &msg, nil
}

func (s *DBStore) ListPendingTopicAssignmentMessages(ctx context.Context, groupID int64, limit int) ([]memory.MessageLog, error) {
	var logs []memory.MessageLog
	query := s.db.WithContext(ctx).
		Where("group_id = ? AND topic_thread_id = 0 AND topic_match_reason = ''", groupID).
		Order("id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&logs).Error; err != nil {
		return nil, err
	}
	return logs, nil
}

func (s *DBStore) GetMessageLogByID(messageID string) (*memory.MessageLog, error) {
	var msg memory.MessageLog
	if err := s.db.Where("message_id = ?", messageID).First(&msg).Error; err != nil {
		return nil, err
	}
	return &msg, nil
}

func (s *DBStore) CountTopicMessagesAfterSummaryByTopicIDs(ctx context.Context, topics []memory.TopicThread) (map[uint]int, error) {
	counts := make(map[uint]int, len(topics))
	if len(topics) == 0 {
		return counts, nil
	}

	type row struct {
		TopicThreadID uint
		Count         int64
	}
	rows := make([]row, 0, len(topics))
	query := s.db.WithContext(ctx).Model(&memory.MessageLog{})
	for i, topic := range topics {
		if i == 0 {
			query = query.Where("(group_id = ? AND topic_thread_id = ? AND id > ?)", topic.GroupID, topic.ID, topic.SummaryUntilMessageLogID)
			continue
		}
		query = query.Or("(group_id = ? AND topic_thread_id = ? AND id > ?)", topic.GroupID, topic.ID, topic.SummaryUntilMessageLogID)
	}
	if err := query.
		Select("topic_thread_id, COUNT(*) AS count").
		Group("topic_thread_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.TopicThreadID] = int(row.Count)
	}
	return counts, nil
}

func (s *DBStore) ApplyTopicAssignmentBatch(ctx context.Context, input AssignmentBatchInput) (AssignmentBatchResult, error) {
	var result AssignmentBatchResult
	if len(input.Items) == 0 || input.GroupID == 0 {
		return result, nil
	}

	items := normalizeAssignmentItems(input.Items)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		activeTopics, err := lockActiveTopics(tx, input.GroupID)
		if err != nil {
			return err
		}
		activeByID := make(map[uint]memory.TopicThread, len(activeTopics))
		for _, topic := range activeTopics {
			activeByID[topic.ID] = topic
		}

		newTopicByKey := make(map[string]uint)
		protectedTopics := make(map[uint]struct{})
		updatedTopics := make(map[uint]struct{})
		archivedTopics := make(map[uint]struct{})
		processedMessages := make(map[uint]struct{})
		noTopicMessages := make(map[uint]struct{})
		messageIDs := make([]uint, 0, len(items))
		for _, item := range items {
			if item.MessageLogID != 0 {
				messageIDs = append(messageIDs, item.MessageLogID)
			}
		}
		lockedMessages := make(map[uint]memory.MessageLog, len(messageIDs))
		if len(messageIDs) > 0 {
			var msgs []memory.MessageLog
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("group_id = ? AND topic_thread_id = 0 AND id IN ?", input.GroupID, messageIDs).
				Find(&msgs).Error; err != nil {
				return err
			}
			for _, msg := range msgs {
				lockedMessages[msg.ID] = msg
			}
		}

		for _, item := range items {
			if item.MessageLogID == 0 {
				continue
			}
			msg, ok := lockedMessages[item.MessageLogID]
			if !ok {
				continue
			}

			targetTopicID, archivedTopicID, ok, err := applyAssignmentItemTx(tx, input.GroupID, item, activeByID, newTopicByKey, protectedTopics)
			if err != nil {
				return err
			}
			if !ok || targetTopicID == 0 {
				reason := strings.TrimSpace(item.MatchReason)
				if reason == "" {
					continue
				}
				if err := tx.Model(&memory.MessageLog{}).
					Where("id = ?", msg.ID).
					Updates(map[string]any{
						"topic_match_reason": reason,
						"topic_match_score":  item.MatchScore,
					}).Error; err != nil {
					return err
				}
				delete(lockedMessages, item.MessageLogID)
				noTopicMessages[msg.ID] = struct{}{}
				continue
			}

			topic, ok := activeByID[targetTopicID]
			if !ok {
				return fmt.Errorf("topic %d not active during assignment", targetTopicID)
			}
			if msg.ID <= topic.SummaryUntilMessageLogID {
				if err := tx.Model(&memory.MessageLog{}).
					Where("id = ?", msg.ID).
					Updates(map[string]any{
						"topic_match_reason": string(AssignmentActionNoTopic),
						"topic_match_score":  0,
					}).Error; err != nil {
					return err
				}
				delete(lockedMessages, item.MessageLogID)
				noTopicMessages[msg.ID] = struct{}{}
				continue
			}

			reason := strings.TrimSpace(item.MatchReason)
			if reason == "" {
				reason = "llm_batch_" + string(item.Action)
			}
			if err := tx.Model(&memory.MessageLog{}).
				Where("id = ?", msg.ID).
				Updates(map[string]any{
					"topic_thread_id":    targetTopicID,
					"topic_match_reason": reason,
					"topic_match_score":  item.MatchScore,
				}).Error; err != nil {
				return err
			}
			delete(lockedMessages, item.MessageLogID)

			if msg.ID > topic.LastMessageLogID {
				if err := tx.Model(&memory.TopicThread{}).
					Where("id = ?", targetTopicID).
					Updates(map[string]any{
						"last_message_log_id": msg.ID,
						"updated_at":          time.Now(),
					}).Error; err != nil {
					return err
				}
				topic.LastMessageLogID = msg.ID
				activeByID[targetTopicID] = topic
			}

			processedMessages[msg.ID] = struct{}{}
			updatedTopics[targetTopicID] = struct{}{}
			protectedTopics[targetTopicID] = struct{}{}
			if archivedTopicID != 0 {
				archivedTopics[archivedTopicID] = struct{}{}
				delete(activeByID, archivedTopicID)
			}
		}

		result.UpdatedTopicIDs = sortedTopicIDs(updatedTopics)
		result.ArchivedTopicIDs = sortedTopicIDs(archivedTopics)
		result.MessageLogIDs = sortedTopicIDs(processedMessages)
		result.NoTopicMessageIDs = sortedTopicIDs(noTopicMessages)
		return nil
	})
	if err != nil {
		return AssignmentBatchResult{}, err
	}
	return result, nil
}

func (s *DBStore) SyncTopicThreadVector(ctx context.Context, topicID uint) error {
	if topicID == 0 || s.topicMilvus == nil {
		return nil
	}

	var topic memory.TopicThread
	if err := s.db.WithContext(ctx).First(&topic, topicID).Error; err != nil {
		return err
	}

	if topic.Status != memory.TopicThreadStatusArchived || topic.SummaryUntilMessageLogID < topic.LastMessageLogID || s.embedding == nil {
		if err := s.topicMilvus.Delete(ctx, []uint{topicID}); err != nil {
			return fmt.Errorf("删除话题向量失败: %w", err)
		}
		return nil
	}

	embedding, err := s.embedding.Embed(ctx, summaryVectorText(ParseSummary(topic.SummaryJSON)))
	if err != nil {
		return err
	}
	if _, err := s.topicMilvus.Upsert(ctx, topic.ID, topic.GroupID, summaryVectorType, embedding); err != nil {
		return fmt.Errorf("更新话题向量失败: %w", err)
	}
	return nil
}

func (s *DBStore) searchArchivedTopicSummaries(ctx context.Context, query string, groupID int64, topK int, threshold float64) ([]vector.SearchResult, error) {
	if s.embedding == nil || s.topicMilvus == nil {
		return nil, nil
	}
	embedding, err := s.embedding.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	return s.topicMilvus.Search(ctx, embedding, groupID, summaryVectorType, topK, threshold)
}

func summaryVectorText(summary memory.TopicSummary) string {
	parts := []string{
		strings.TrimSpace(summary.Title),
		strings.TrimSpace(summary.Gist),
		strings.Join(summary.Facts, "\n"),
	}
	if len(summary.OpenLoops) > 0 {
		parts = append(parts, strings.Join(summary.OpenLoops, "\n"))
	}
	if len(summary.RecentTurns) > 0 {
		parts = append(parts, strings.Join(summary.RecentTurns, "\n"))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func lockActiveTopics(tx *gorm.DB, groupID int64) ([]memory.TopicThread, error) {
	var topics []memory.TopicThread
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("group_id = ? AND status = ?", groupID, memory.TopicThreadStatusActive).
		Order("last_message_log_id ASC").
		Order("id ASC").
		Find(&topics).Error
	return topics, err
}

func normalizeAssignmentItems(items []AssignmentBatchItem) []AssignmentBatchItem {
	normalized := make([]AssignmentBatchItem, 0, len(items))
	for _, item := range items {
		if item.MessageLogID == 0 {
			continue
		}
		item.NewTopicKey = strings.TrimSpace(item.NewTopicKey)
		item.MatchReason = strings.TrimSpace(item.MatchReason)
		if item.Action == AssignmentActionNoTopic && item.MatchReason == "" {
			item.MatchReason = string(AssignmentActionNoTopic)
		}
		normalized = append(normalized, item)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		return normalized[i].MessageLogID < normalized[j].MessageLogID
	})
	return normalized
}

func applyAssignmentItemTx(tx *gorm.DB, groupID int64, item AssignmentBatchItem, activeByID map[uint]memory.TopicThread, newTopicByKey map[string]uint, protectedTopics map[uint]struct{}) (uint, uint, bool, error) {
	switch item.Action {
	case AssignmentActionNoTopic:
		return 0, 0, false, nil
	case AssignmentActionReuse:
		if item.TopicID == 0 {
			return 0, 0, false, nil
		}
		if topic, ok := activeByID[item.TopicID]; ok {
			return topic.ID, 0, true, nil
		}
		archivedTopicID, ok, err := archiveVictimForAssignmentTx(tx, groupID, activeByID, protectedTopics)
		if err != nil || !ok {
			return 0, 0, false, err
		}
		if err := reopenTopicThreadTx(tx, groupID, item.TopicID); err != nil {
			if err == ErrStateChanged {
				return 0, 0, false, nil
			}
			return 0, 0, false, err
		}
		var reopened memory.TopicThread
		if err := tx.First(&reopened, item.TopicID).Error; err != nil {
			return 0, 0, false, err
		}
		activeByID[reopened.ID] = reopened
		protectedTopics[reopened.ID] = struct{}{}
		return reopened.ID, archivedTopicID, true, nil
	case AssignmentActionNew:
		key := item.NewTopicKey
		if key == "" {
			key = fmt.Sprintf("message_%d", item.MessageLogID)
		}
		if topicID := newTopicByKey[key]; topicID != 0 {
			if _, ok := activeByID[topicID]; ok {
				return topicID, 0, true, nil
			}
			return 0, 0, false, nil
		}
		archivedTopicID, ok, err := archiveVictimForAssignmentTx(tx, groupID, activeByID, protectedTopics)
		if err != nil || !ok {
			return 0, 0, false, err
		}
		topic, err := createTopicThreadTx(tx, groupID)
		if err != nil {
			return 0, 0, false, err
		}
		activeByID[topic.ID] = *topic
		newTopicByKey[key] = topic.ID
		protectedTopics[topic.ID] = struct{}{}
		return topic.ID, archivedTopicID, true, nil
	default:
		return 0, 0, false, nil
	}
}

func archiveVictimForAssignmentTx(tx *gorm.DB, groupID int64, activeByID map[uint]memory.TopicThread, protectedTopics map[uint]struct{}) (uint, bool, error) {
	if len(activeByID) < MaxActiveThreadsPerGroup {
		return 0, true, nil
	}
	victimID := oldestUnprotectedActiveTopicID(activeByID, protectedTopics)
	if victimID == 0 {
		return 0, false, nil
	}
	if err := archiveTopicThreadTx(tx, groupID, victimID); err != nil {
		return 0, false, err
	}
	delete(activeByID, victimID)
	return victimID, true, nil
}

func oldestUnprotectedActiveTopicID(activeByID map[uint]memory.TopicThread, protectedTopics map[uint]struct{}) uint {
	topics := make([]memory.TopicThread, 0, len(activeByID))
	for _, topic := range activeByID {
		if _, protected := protectedTopics[topic.ID]; protected {
			continue
		}
		topics = append(topics, topic)
	}
	return OldestActiveTopicID(topics)
}

func createTopicThreadTx(tx *gorm.DB, groupID int64) (*memory.TopicThread, error) {
	topic := &memory.TopicThread{
		GroupID:                  groupID,
		Status:                   memory.TopicThreadStatusActive,
		SummaryJSON:              DefaultSummaryJSON(),
		SummaryHistoryJSON:       DefaultSummaryHistoryJSON(),
		SummaryUntilMessageLogID: 0,
		LastMessageLogID:         0,
	}
	if err := tx.Create(topic).Error; err != nil {
		return nil, err
	}
	return topic, nil
}

func reopenTopicThreadTx(tx *gorm.DB, groupID int64, topicID uint) error {
	result := tx.Model(&memory.TopicThread{}).
		Where("id = ? AND group_id = ? AND status = ?", topicID, groupID, memory.TopicThreadStatusArchived).
		Updates(map[string]any{
			"status":     memory.TopicThreadStatusActive,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrStateChanged
	}
	return nil
}

func archiveTopicThreadTx(tx *gorm.DB, groupID int64, topicID uint) error {
	result := tx.Model(&memory.TopicThread{}).
		Where("id = ? AND group_id = ? AND status = ?", topicID, groupID, memory.TopicThreadStatusActive).
		Updates(map[string]any{
			"status":     memory.TopicThreadStatusArchived,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrStateChanged
	}
	return nil
}

func OldestActiveTopicID(topics []memory.TopicThread) uint {
	if len(topics) == 0 {
		return 0
	}
	oldest := topics[0]
	for _, topic := range topics[1:] {
		if topic.LastMessageLogID < oldest.LastMessageLogID {
			oldest = topic
			continue
		}
		if topic.LastMessageLogID == oldest.LastMessageLogID && topic.ID < oldest.ID {
			oldest = topic
		}
	}
	return oldest.ID
}

func sortedTopicIDs(set map[uint]struct{}) []uint {
	ids := make([]uint, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
