package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mumu-bot/internal/config"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"mumu-bot/internal/vector"
)

const topicSummaryVectorType = "topic_summary"

var ErrTopicStateChanged = errors.New("topic state changed")

type TopicAssignmentAction string

const (
	TopicAssignmentActionNoTopic TopicAssignmentAction = "no_topic"
	TopicAssignmentActionReuse   TopicAssignmentAction = "reuse"
	TopicAssignmentActionNew     TopicAssignmentAction = "new"
)

type TopicAssignmentBatchItem struct {
	MessageLogID uint
	Action       TopicAssignmentAction
	TopicID      uint
	NewTopicKey  string
	MatchReason  string
	MatchScore   float64
}

type TopicAssignmentBatchInput struct {
	GroupID int64
	Items   []TopicAssignmentBatchItem
}

type TopicAssignmentBatchResult struct {
	UpdatedTopicIDs   []uint
	ArchivedTopicIDs  []uint
	MessageLogIDs     []uint
	NoTopicMessageIDs []uint
}

type TopicThreadSearchHit struct {
	Topic TopicThread
	Score float64
}

func IsTopicAssignmentProcessed(msg MessageLog) bool {
	return msg.TopicThreadID != 0 || strings.TrimSpace(msg.TopicMatchReason) != ""
}

func EmptyTopicSummary() TopicSummaryV1 {
	return TopicSummaryV1{
		Version:      1,
		Facts:        []string{},
		Participants: []TopicSummaryParticipant{},
		OpenLoops:    []string{},
		RecentTurns:  []string{},
		Keywords:     []string{},
	}
}

func MarshalTopicSummary(summary TopicSummaryV1) (string, error) {
	if summary.Version == 0 {
		summary.Version = 1
	}
	if summary.Facts == nil {
		summary.Facts = []string{}
	}
	if summary.Participants == nil {
		summary.Participants = []TopicSummaryParticipant{}
	}
	if summary.OpenLoops == nil {
		summary.OpenLoops = []string{}
	}
	if summary.RecentTurns == nil {
		summary.RecentTurns = []string{}
	}
	if summary.Keywords == nil {
		summary.Keywords = []string{}
	}
	summary.ItemMeta = NormalizeTopicSummaryItemMeta(summary)

	raw, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func MustMarshalTopicSummary(summary TopicSummaryV1) string {
	raw, err := MarshalTopicSummary(summary)
	if err != nil {
		return `{"version":1,"title":"","gist":"","facts":[],"participants":[],"open_loops":[],"recent_turns":[],"keywords":[]}`
	}
	return raw
}

func ParseTopicSummary(raw string) TopicSummaryV1 {
	summary := EmptyTopicSummary()
	if strings.TrimSpace(raw) == "" {
		return summary
	}
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		return EmptyTopicSummary()
	}
	if summary.Version == 0 {
		summary.Version = 1
	}
	if summary.Facts == nil {
		summary.Facts = []string{}
	}
	if summary.Participants == nil {
		summary.Participants = []TopicSummaryParticipant{}
	}
	if summary.OpenLoops == nil {
		summary.OpenLoops = []string{}
	}
	if summary.RecentTurns == nil {
		summary.RecentTurns = []string{}
	}
	if summary.Keywords == nil {
		summary.Keywords = []string{}
	}
	summary.ItemMeta = normalizeExistingTopicSummaryItemMeta(summary)
	return summary
}

func normalizeExistingTopicSummaryItemMeta(summary TopicSummaryV1) []TopicSummaryItemMeta {
	if len(summary.ItemMeta) == 0 {
		return []TopicSummaryItemMeta{}
	}
	return NormalizeTopicSummaryItemMeta(summary)
}

func NormalizeTopicSummaryItemMeta(summary TopicSummaryV1) []TopicSummaryItemMeta {
	result := make([]TopicSummaryItemMeta, 0, len(summary.Facts)+len(summary.OpenLoops))
	oldByText := make(map[string]TopicSummaryItemMeta, len(summary.ItemMeta))
	for _, meta := range summary.ItemMeta {
		text := strings.TrimSpace(meta.Text)
		if text == "" {
			continue
		}
		meta.Text = text
		meta.Kind = strings.TrimSpace(meta.Kind)
		meta.SlotKind = strings.TrimSpace(meta.SlotKind)
		meta.Signature = strings.TrimSpace(meta.Signature)
		oldByText[normalizeMemoryContentForKey(text)] = meta
	}
	push := func(kind string, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		key := normalizeMemoryContentForKey(text)
		meta := oldByText[key]
		meta.Text = text
		meta.Kind = kind
		if meta.Signature == "" {
			meta.Signature = buildFactKey(MemoryTypeGroupFact, CanonicalMemoryTypeFact, text)
		}
		result = append(result, meta)
	}
	for _, fact := range summary.Facts {
		push("fact", fact)
	}
	for _, loop := range summary.OpenLoops {
		push("open_loop", loop)
	}
	return result
}

func TopicSummaryItemMetaForPrompt(summary TopicSummaryV1) []TopicSummaryItemMeta {
	return NormalizeTopicSummaryItemMeta(summary)
}

func MergeTopicSummaryItemMeta(oldSummary TopicSummaryV1, nextSummary TopicSummaryV1) []TopicSummaryItemMeta {
	nextSummary.ItemMeta = append([]TopicSummaryItemMeta(nil), oldSummary.ItemMeta...)
	return NormalizeTopicSummaryItemMeta(nextSummary)
}

func MarshalTopicSummaryItemMetaForPrompt(summary TopicSummaryV1) (string, error) {
	raw, err := json.Marshal(TopicSummaryItemMetaForPrompt(summary))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func ParseTopicSummaryHistory(raw string) []TopicSummarySnapshot {
	if strings.TrimSpace(raw) == "" {
		return []TopicSummarySnapshot{}
	}
	var snapshots []TopicSummarySnapshot
	if err := json.Unmarshal([]byte(raw), &snapshots); err != nil {
		return []TopicSummarySnapshot{}
	}
	return snapshots
}

func DefaultTopicSummaryJSON() string {
	return MustMarshalTopicSummary(EmptyTopicSummary())
}

func DefaultTopicSummaryHistoryJSON() string {
	return "[]"
}

func AppendTopicSummaryHistory(raw string, summary TopicSummaryV1, capturedAt time.Time) (string, error) {
	history := ParseTopicSummaryHistory(raw)
	history = append(history, TopicSummarySnapshot{
		CapturedAt: capturedAt.Format(time.RFC3339),
		Summary:    ParseTopicSummary(MustMarshalTopicSummary(summary)),
	})
	if len(history) > TopicSummaryHistoryLimit {
		history = history[len(history)-TopicSummaryHistoryLimit:]
	}
	encoded, err := json.Marshal(history)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (m *Manager) ArchiveTopicThreadForRepair(ctx context.Context, groupID int64, topicID uint) error {
	if topicID == 0 {
		return nil
	}
	return m.db.WithContext(ctx).
		Model(&TopicThread{}).
		Where("id = ? AND group_id = ? AND status = ?", topicID, groupID, TopicThreadStatusActive).
		Updates(map[string]any{
			"status":     TopicThreadStatusArchived,
			"updated_at": time.Now(),
		}).Error
}

func (m *Manager) UpdateTopicSummary(ctx context.Context, topicID uint, summary TopicSummaryV1, summaryUntil uint, capturedAt time.Time) error {
	if topicID == 0 {
		return nil
	}

	summaryJSON, err := MarshalTopicSummary(summary)
	if err != nil {
		return err
	}
	updated := false
	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var topic TopicThread
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&topic, topicID).Error; err != nil {
			return err
		}
		if summaryUntil <= topic.SummaryUntilMessageLogID {
			return nil
		}

		historyJSON, err := AppendTopicSummaryHistory(topic.SummaryHistoryJSON, summary, capturedAt)
		if err != nil {
			return err
		}

		if err := tx.Model(&TopicThread{}).
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

	topic, fetchErr := m.GetTopicThread(ctx, topicID)
	if fetchErr == nil && topic != nil {
		summary := ParseTopicSummary(topic.SummaryJSON)
		participants, participantsErr := m.ListRecentTopicParticipants(ctx, topic.ID, TopicTailKeepMessages)
		if participantsErr != nil {
			zap.L().Warn("读取话题参与者失败，长期记忆主体解析可能退化", zap.Uint("topic_id", topic.ID), zap.Error(participantsErr))
		}
		if len(summary.Facts) > 0 {
			_, upsertErr := m.UpsertTopicMemoryCandidate(ctx, TopicMemoryCandidateInput{
				GroupID:      topic.GroupID,
				TopicID:      topic.ID,
				SelfID:       config.Get().Persona.QQ,
				Claims:       summary.Facts,
				Participants: participants,
			})
			if upsertErr != nil {
				zap.L().Warn("话题事实下沉长期记忆失败", zap.Uint("topic_id", topic.ID), zap.Error(upsertErr))
			}
		}
		if len(summary.OpenLoops) > 0 {
			_, upsertErr := m.UpsertTopicMemoryCandidate(ctx, TopicMemoryCandidateInput{
				GroupID:               topic.GroupID,
				TopicID:               topic.ID,
				SelfID:                config.Get().Persona.QQ,
				Claims:                summary.OpenLoops,
				Participants:          participants,
				AllowedCanonicalTypes: []CanonicalMemoryType{CanonicalMemoryTypeGoal},
			})
			if upsertErr != nil {
				zap.L().Warn("话题待办下沉长期目标失败", zap.Uint("topic_id", topic.ID), zap.Error(upsertErr))
			}
		}
	}

	if err := m.SyncTopicThreadVector(ctx, topicID); err != nil {
		zap.L().Warn("更新话题摘要后同步向量失败", zap.Uint("topic_id", topicID), zap.Error(err))
	}
	return nil
}

func (m *Manager) ListArchivedTopicThreadsNeedingSummary(ctx context.Context, groupID int64) ([]TopicThread, error) {
	var topics []TopicThread
	err := m.db.WithContext(ctx).
		Where("group_id = ? AND status = ? AND summary_until_message_log_id < last_message_log_id", groupID, TopicThreadStatusArchived).
		Order("last_message_log_id ASC").
		Order("id ASC").
		Find(&topics).Error
	return topics, err
}

func (m *Manager) ListActiveTopicThreads(ctx context.Context, groupID int64) ([]TopicThread, error) {
	var topics []TopicThread
	err := m.db.WithContext(ctx).
		Where("group_id = ? AND status = ?", groupID, TopicThreadStatusActive).
		Order("last_message_log_id DESC").
		Order("id DESC").
		Find(&topics).Error
	return topics, err
}

func (m *Manager) GetTopicThread(ctx context.Context, topicID uint) (*TopicThread, error) {
	var topic TopicThread
	if err := m.db.WithContext(ctx).First(&topic, topicID).Error; err != nil {
		return nil, err
	}
	return &topic, nil
}

func (m *Manager) SearchArchivedTopicThreads(ctx context.Context, query string, groupID int64, topK int, threshold float64) ([]TopicThread, error) {
	hits, err := m.SearchArchivedTopicThreadHits(ctx, query, groupID, topK, threshold)
	if err != nil {
		return nil, err
	}
	topics := make([]TopicThread, 0, len(hits))
	for _, hit := range hits {
		topics = append(topics, hit.Topic)
	}
	return topics, nil
}

func (m *Manager) SearchArchivedTopicThreadHits(ctx context.Context, query string, groupID int64, topK int, threshold float64) ([]TopicThreadSearchHit, error) {
	if m.embedding == nil || m.topicMilvus == nil {
		return nil, nil
	}
	if topK <= 0 {
		topK = 8
	}

	results, err := m.searchArchivedTopicSummaries(ctx, query, groupID, topK, threshold)
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

	var topics []TopicThread
	if err := m.db.WithContext(ctx).
		Where("id IN ? AND group_id = ? AND status = ?", ids, groupID, TopicThreadStatusArchived).
		Find(&topics).Error; err != nil {
		return nil, err
	}

	topicByID := make(map[uint]TopicThread, len(topics))
	for _, topic := range topics {
		topicByID[topic.ID] = topic
	}

	hits := make([]TopicThreadSearchHit, 0, len(results))
	for _, result := range results {
		if topic, ok := topicByID[result.MemoryID]; ok {
			hits = append(hits, TopicThreadSearchHit{
				Topic: topic,
				Score: float64(result.Score),
			})
		}
	}
	return hits, nil
}

func (m *Manager) GetTopicMessagesAfterSummary(ctx context.Context, topicID uint, limit int) ([]MessageLog, error) {
	topic, err := m.GetTopicThread(ctx, topicID)
	if err != nil {
		return nil, err
	}

	query := m.db.WithContext(ctx).
		Where("group_id = ? AND topic_thread_id = ? AND id > ?", topic.GroupID, topic.ID, topic.SummaryUntilMessageLogID).
		Order("id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}

	var logs []MessageLog
	if err := query.Find(&logs).Error; err != nil {
		return nil, err
	}
	return logs, nil
}

func (m *Manager) CountTopicMessagesAfterSummary(ctx context.Context, topicID uint) (int, error) {
	topic, err := m.GetTopicThread(ctx, topicID)
	if err != nil {
		return 0, err
	}

	var count int64
	if err := m.db.WithContext(ctx).
		Model(&MessageLog{}).
		Where("group_id = ? AND topic_thread_id = ? AND id > ?", topic.GroupID, topic.ID, topic.SummaryUntilMessageLogID).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

func (m *Manager) ListRecentTopicMessages(ctx context.Context, topicID uint, limit int) ([]MessageLog, error) {
	if limit <= 0 {
		limit = TopicTailKeepMessages
	}

	var logs []MessageLog
	if err := m.db.WithContext(ctx).
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

func (m *Manager) ListRecentTopicParticipants(ctx context.Context, topicID uint, limit int) ([]TopicParticipantRef, error) {
	if limit <= 0 {
		limit = TopicTailKeepMessages
	}

	var logs []MessageLog
	if err := m.db.WithContext(ctx).
		Where("topic_thread_id = ?", topicID).
		Order("id DESC").
		Find(&logs).Error; err != nil {
		return nil, err
	}

	participants := make([]TopicParticipantRef, 0, limit)
	seen := make(map[int64]struct{}, limit)
	for _, log := range logs {
		if _, ok := seen[log.UserID]; ok {
			continue
		}
		seen[log.UserID] = struct{}{}
		participants = append(participants, TopicParticipantRef{
			UserID:   log.UserID,
			Nickname: log.Nickname,
		})
		if len(participants) >= limit {
			break
		}
	}
	return participants, nil
}

func (m *Manager) CreateMessageLog(ctx context.Context, msg MessageLog) (*MessageLog, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := m.db.WithContext(ctx).Create(&msg).Error; err != nil {
		return nil, err
	}
	return &msg, nil
}

func (m *Manager) ListPendingTopicAssignmentMessages(ctx context.Context, groupID int64, limit int) ([]MessageLog, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var logs []MessageLog
	query := m.db.WithContext(ctx).
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

func (m *Manager) ApplyTopicAssignmentBatch(ctx context.Context, input TopicAssignmentBatchInput) (TopicAssignmentBatchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var result TopicAssignmentBatchResult
	if len(input.Items) == 0 || input.GroupID == 0 {
		return result, nil
	}

	items := normalizeTopicAssignmentItems(input.Items)
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		activeTopics, err := lockActiveTopics(tx, input.GroupID)
		if err != nil {
			return err
		}
		activeByID := make(map[uint]TopicThread, len(activeTopics))
		for _, topic := range activeTopics {
			activeByID[topic.ID] = topic
		}

		newTopicByKey := make(map[string]uint)
		protectedTopics := make(map[uint]struct{})
		updatedTopics := make(map[uint]struct{})
		archivedTopics := make(map[uint]struct{})
		processedMessages := make(map[uint]struct{})
		noTopicMessages := make(map[uint]struct{})

		for _, item := range items {
			if item.MessageLogID == 0 {
				continue
			}
			var msg MessageLog
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ? AND group_id = ? AND topic_thread_id = 0", item.MessageLogID, input.GroupID).
				First(&msg).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return err
			}

			targetTopicID, archivedTopicID, ok, err := applyTopicAssignmentItemTx(tx, input.GroupID, item, activeByID, newTopicByKey, protectedTopics)
			if err != nil {
				return err
			}
			if !ok || targetTopicID == 0 {
				reason := strings.TrimSpace(item.MatchReason)
				if reason == "" {
					continue
				}
				if err := tx.Model(&MessageLog{}).
					Where("id = ?", msg.ID).
					Updates(map[string]any{
						"topic_match_reason": reason,
						"topic_match_score":  item.MatchScore,
					}).Error; err != nil {
					return err
				}
				noTopicMessages[msg.ID] = struct{}{}
				continue
			}

			var topic TopicThread
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&topic, targetTopicID).Error; err != nil {
				return err
			}
			if msg.ID <= topic.SummaryUntilMessageLogID {
				if err := tx.Model(&MessageLog{}).
					Where("id = ?", msg.ID).
					Updates(map[string]any{
						"topic_match_reason": string(TopicAssignmentActionNoTopic),
						"topic_match_score":  0,
					}).Error; err != nil {
					return err
				}
				noTopicMessages[msg.ID] = struct{}{}
				continue
			}

			reason := strings.TrimSpace(item.MatchReason)
			if reason == "" {
				reason = "llm_batch_" + string(item.Action)
			}
			if err := tx.Model(&MessageLog{}).
				Where("id = ?", msg.ID).
				Updates(map[string]any{
					"topic_thread_id":    targetTopicID,
					"topic_match_reason": reason,
					"topic_match_score":  item.MatchScore,
				}).Error; err != nil {
				return err
			}

			if msg.ID > topic.LastMessageLogID {
				if err := tx.Model(&TopicThread{}).
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
		return TopicAssignmentBatchResult{}, err
	}
	return result, nil
}

func (m *Manager) SyncTopicThreadVector(ctx context.Context, topicID uint) error {
	if topicID == 0 || m.topicMilvus == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var topic TopicThread
	if err := m.db.WithContext(ctx).First(&topic, topicID).Error; err != nil {
		return err
	}

	if err := m.topicMilvus.Delete(ctx, []uint{topicID}); err != nil {
		return fmt.Errorf("删除旧话题向量失败: %w", err)
	}

	if topic.Status != TopicThreadStatusArchived || topic.SummaryUntilMessageLogID < topic.LastMessageLogID || m.embedding == nil {
		return nil
	}

	embedding, err := m.embedding.Embed(ctx, topicSummaryVectorText(ParseTopicSummary(topic.SummaryJSON)))
	if err != nil {
		return err
	}
	if _, err := m.topicMilvus.Insert(ctx, topic.ID, topic.GroupID, topicSummaryVectorType, embedding); err != nil {
		return fmt.Errorf("插入话题向量失败: %w", err)
	}
	return nil
}

func (m *Manager) protectedTopicMessageIDs(groupID int64, keepLatest int) ([]uint, error) {
	keepSet := make(map[uint]struct{})

	if keepLatest > 0 {
		var latestIDs []uint
		if err := m.db.Model(&MessageLog{}).
			Where("group_id = ?", groupID).
			Order("created_at DESC").
			Limit(keepLatest).
			Pluck("id", &latestIDs).Error; err != nil {
			return nil, err
		}
		for _, id := range latestIDs {
			keepSet[id] = struct{}{}
		}
	}

	var assignedIDs []uint
	if err := m.db.Model(&MessageLog{}).
		Where("group_id = ? AND topic_thread_id != 0", groupID).
		Pluck("id", &assignedIDs).Error; err != nil {
		return nil, err
	}
	for _, id := range assignedIDs {
		keepSet[id] = struct{}{}
	}

	state, err := m.GetLearningState(groupID)
	if err != nil {
		return nil, err
	}

	var learnerPendingIDs []uint
	if err := m.db.Model(&MessageLog{}).
		Where("group_id = ? AND id > ?", groupID, state.LastMessageID).
		Pluck("id", &learnerPendingIDs).Error; err != nil {
		return nil, err
	}
	for _, id := range learnerPendingIDs {
		keepSet[id] = struct{}{}
	}

	ids := make([]uint, 0, len(keepSet))
	for id := range keepSet {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func topicSummaryCollectionName(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "mumu_memories"
	}
	return base + "_topic_summaries"
}

func topicSummaryVectorText(summary TopicSummaryV1) string {
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

func lockActiveTopics(tx *gorm.DB, groupID int64) ([]TopicThread, error) {
	var topics []TopicThread
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("group_id = ? AND status = ?", groupID, TopicThreadStatusActive).
		Order("last_message_log_id ASC").
		Order("id ASC").
		Find(&topics).Error
	return topics, err
}

func normalizeTopicAssignmentItems(items []TopicAssignmentBatchItem) []TopicAssignmentBatchItem {
	normalized := make([]TopicAssignmentBatchItem, 0, len(items))
	for _, item := range items {
		if item.MessageLogID == 0 {
			continue
		}
		if item.Action == "" {
			item.Action = TopicAssignmentActionNoTopic
		}
		item.NewTopicKey = strings.TrimSpace(item.NewTopicKey)
		item.MatchReason = strings.TrimSpace(item.MatchReason)
		if item.Action == TopicAssignmentActionNoTopic && item.MatchReason == "" {
			item.MatchReason = string(TopicAssignmentActionNoTopic)
		}
		normalized = append(normalized, item)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		return normalized[i].MessageLogID < normalized[j].MessageLogID
	})
	return normalized
}

func applyTopicAssignmentItemTx(tx *gorm.DB, groupID int64, item TopicAssignmentBatchItem, activeByID map[uint]TopicThread, newTopicByKey map[string]uint, protectedTopics map[uint]struct{}) (uint, uint, bool, error) {
	switch item.Action {
	case TopicAssignmentActionNoTopic:
		return 0, 0, false, nil
	case TopicAssignmentActionReuse:
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
			if errors.Is(err, ErrTopicStateChanged) {
				return 0, 0, false, nil
			}
			return 0, 0, false, err
		}
		var reopened TopicThread
		if err := tx.First(&reopened, item.TopicID).Error; err != nil {
			return 0, 0, false, err
		}
		activeByID[reopened.ID] = reopened
		protectedTopics[reopened.ID] = struct{}{}
		return reopened.ID, archivedTopicID, true, nil
	case TopicAssignmentActionNew:
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

func archiveVictimForAssignmentTx(tx *gorm.DB, groupID int64, activeByID map[uint]TopicThread, protectedTopics map[uint]struct{}) (uint, bool, error) {
	if len(activeByID) < MaxActiveTopicThreadsPerGroup {
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

func oldestUnprotectedActiveTopicID(activeByID map[uint]TopicThread, protectedTopics map[uint]struct{}) uint {
	topics := make([]TopicThread, 0, len(activeByID))
	for _, topic := range activeByID {
		if _, protected := protectedTopics[topic.ID]; protected {
			continue
		}
		topics = append(topics, topic)
	}
	return OldestActiveTopicID(topics)
}

func createTopicThreadTx(tx *gorm.DB, groupID int64) (*TopicThread, error) {
	topic := &TopicThread{
		GroupID:                  groupID,
		Status:                   TopicThreadStatusActive,
		SummaryJSON:              DefaultTopicSummaryJSON(),
		SummaryHistoryJSON:       DefaultTopicSummaryHistoryJSON(),
		SummaryUntilMessageLogID: 0,
		LastMessageLogID:         0,
	}
	if err := tx.Create(topic).Error; err != nil {
		return nil, err
	}
	return topic, nil
}

func reopenTopicThreadTx(tx *gorm.DB, groupID int64, topicID uint) error {
	result := tx.Model(&TopicThread{}).
		Where("id = ? AND group_id = ? AND status = ?", topicID, groupID, TopicThreadStatusArchived).
		Updates(map[string]any{
			"status":     TopicThreadStatusActive,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTopicStateChanged
	}
	return nil
}

func archiveTopicThreadTx(tx *gorm.DB, groupID int64, topicID uint) error {
	result := tx.Model(&TopicThread{}).
		Where("id = ? AND group_id = ? AND status = ?", topicID, groupID, TopicThreadStatusActive).
		Updates(map[string]any{
			"status":     TopicThreadStatusArchived,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTopicStateChanged
	}
	return nil
}

func OldestActiveTopicID(topics []TopicThread) uint {
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
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (m *Manager) searchArchivedTopicSummaries(ctx context.Context, query string, groupID int64, topK int, threshold float64) ([]vector.SearchResult, error) {
	if m.embedding == nil || m.topicMilvus == nil {
		return nil, nil
	}
	embedding, err := m.embedding.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	return m.topicMilvus.Search(ctx, embedding, groupID, topicSummaryVectorType, topK, threshold)
}
