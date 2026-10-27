package memory

import (
	"context"
	"fmt"
	"sort"

	"github.com/bytedance/sonic"
)

// TopicDigestSummary 一条话题摘要的展示内容
type TopicDigestSummary struct {
	TopicID      uint     `json:"topic_id"`
	Title        string   `json:"title"`
	Gist         string   `json:"gist"`
	Participants []string `json:"participants,omitempty"`
	OpenLoops    []string `json:"open_loops,omitempty"`
	Keywords     []string `json:"keywords,omitempty"`
}

// TopicCoverage 指定消息的话题覆盖情况与对应摘要
type TopicCoverage struct {
	Covered   map[uint]bool
	Summaries []TopicDigestSummary
}

// TopicCoverage 计算一批消息是否已被有效话题摘要覆盖，并返回对应摘要
func (m *Manager) TopicCoverage(ctx context.Context, kind string, targetID int64, logIDs []uint) (TopicCoverage, error) {
	coverage := TopicCoverage{Covered: map[uint]bool{}}
	if len(logIDs) == 0 {
		return coverage, nil
	}
	if kind == ConversationKindPrivate {
		state, err := m.GetPrivateTopicStateAt(ctx, targetID, 0)
		if err != nil {
			return coverage, err
		}
		if state == nil {
			return coverage, nil
		}
		// 摘要来源失效时不能标记覆盖，否则原文被丢弃又没有摘要顶上
		if state.ThroughMessageLogID > 0 && state.SourcesValid {
			for _, id := range logIDs {
				coverage.Covered[id] = id <= state.ThroughMessageLogID
			}
			var summary TopicSummary
			if err := sonic.UnmarshalString(state.SummaryJSON, &summary); err != nil {
				return coverage, err
			}
			coverage.Summaries = append(coverage.Summaries, topicDigestSummary(0, summary))
		}
		return coverage, nil
	}

	topicOf := make(map[uint]uint, len(logIDs))
	rows, err := m.db.WithContext(ctx).Table("topic_assignments ta").
		Select("ta.message_log_id, ta.topic_id").
		Joins("JOIN message_logs ml ON ml.id = ta.message_log_id").
		Where("ml.conversation_kind=? AND ml.target_id=? AND ml.id IN ?", kind, targetID, logIDs).
		Rows()
	if err != nil {
		return coverage, err
	}
	defer rows.Close()
	topicIDs := make([]uint, 0, len(logIDs))
	for rows.Next() {
		var messageID uint
		var topicID *uint
		if err := rows.Scan(&messageID, &topicID); err != nil {
			return coverage, err
		}
		if topicID == nil {
			// 无话题价值的原文视为已覆盖，压缩时直接丢弃
			coverage.Covered[messageID] = true
			continue
		}
		topicOf[messageID] = *topicID
		topicIDs = append(topicIDs, *topicID)
	}
	if err := rows.Err(); err != nil {
		return coverage, err
	}
	summaries, err := m.topicSummaries(ctx, topicIDs)
	if err != nil {
		return coverage, err
	}
	validTopics := make(map[uint]bool, len(summaries))
	for _, item := range summaries {
		validTopics[item.TopicID] = true
	}
	for messageID, topicID := range topicOf {
		coverage.Covered[messageID] = validTopics[topicID]
	}
	return coverage, nil
}

// topicSummaries 读取指定话题的最新有效摘要
func (m *Manager) topicSummaries(ctx context.Context, topicIDs []uint) ([]TopicDigestSummary, error) {
	if len(topicIDs) == 0 {
		return nil, nil
	}
	unique := make([]uint, 0, len(topicIDs))
	seen := map[uint]bool{}
	for _, id := range topicIDs {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })
	rows, err := m.db.WithContext(ctx).Table("topic_summaries ts").
		Select("DISTINCT ON (ta.topic_id) ts.*, ta.topic_id, ("+TopicSummaryValiditySQL+") AS sources_valid").
		Joins("JOIN topic_assignments ta ON ta.id = ts.through_topic_assignment_id").
		Where("ta.topic_id IN ?", unique).
		Order("ta.topic_id, ts.id DESC").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TopicDigestSummary, 0, len(unique))
	for rows.Next() {
		var record TopicSummaryRecord
		var topicID uint
		var sourcesValid bool
		if err := rows.Scan(&record.ID, &record.ThroughTopicAssignmentID, &record.SummaryJSON, &record.Embedding, &record.CreatedAt, &topicID, &sourcesValid); err != nil {
			return nil, err
		}
		if !sourcesValid {
			continue
		}
		var summary TopicSummary
		if err := sonic.UnmarshalString(record.SummaryJSON, &summary); err != nil {
			return nil, err
		}
		items = append(items, topicDigestSummary(topicID, summary))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// topicDigestSummary 把话题摘要转成展示结构
func topicDigestSummary(topicID uint, summary TopicSummary) TopicDigestSummary {
	item := TopicDigestSummary{
		TopicID:   topicID,
		Title:     summary.Title,
		Gist:      summary.Gist,
		OpenLoops: summary.OpenLoops,
		Keywords:  summary.Keywords,
	}
	for _, participant := range summary.Participants {
		name := participant.Nickname
		if name == "" {
			name = fmt.Sprintf("%d", participant.UserID)
		}
		item.Participants = append(item.Participants, name)
	}
	return item
}
