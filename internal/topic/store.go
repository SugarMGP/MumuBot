package topic

import (
	"context"
	"sort"
	"strings"

	"mumu-bot/internal/memory"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DBStore struct{ db *gorm.DB }

func (s *DBStore) PersistMessageLog(ctx context.Context, item memory.MessageLog) (*memory.MessageLog, bool, error) {
	var stored memory.MessageLog
	created := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&item)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Where("group_id=? AND one_bot_message_id = ?", item.GroupID, item.OneBotMessageID).First(&stored).Error; err != nil {
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
	err := memory.LatestTopicSummaries(s.db.WithContext(ctx), 0, throughMessageLogID).Where("ts.topic_id=?", topicID).First(&row).Error
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

func (s *DBStore) SearchTopicHits(ctx context.Context, query memory.HybridQuery, groupID int64, throughMessageLogID uint, limit int) ([]memory.TopicThread, error) {
	if query.Empty() || limit <= 0 {
		return nil, nil
	}
	latest := memory.LatestTopicSummaries(s.db.WithContext(ctx), groupID, throughMessageLogID).Where(memory.TopicSummaryValiditySQL)
	var vectorRows []struct{ TopicID uint }
	if err := s.db.WithContext(ctx).Table("(?) ts", latest).Select("topic_id").Where("1-(embedding <=> ?)>=0.3", query.Vector()).Order(clause.OrderBy{Expression: clause.Expr{SQL: "embedding <=> ?", Vars: []any{query.Vector()}}}).Limit(20).Scan(&vectorRows).Error; err != nil {
		return nil, err
	}
	var textRows []struct{ TopicID uint }
	if err := s.db.WithContext(ctx).Raw(`SELECT topic_id FROM (SELECT ts.topic_id,(SELECT max(greatest(public.word_similarity(fragment,`+memory.TopicSummaryTextSQL+`),public.word_similarity(`+memory.TopicSummaryTextSQL+`,fragment))) FROM unnest(?::text[]) AS fragments(fragment)) score FROM (?) ts) ranked WHERE score>=0.1 ORDER BY score DESC,topic_id LIMIT 20`, query.FragmentArray(), latest).Scan(&textRows).Error; err != nil {
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
