package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
)

type TopicSearchQuery struct {
	Keywords         []string
	ConversationKind string
	TargetID         int64
	Status           string
	Limit            int
	Offset           int
}

type TopicHit struct {
	TopicID          uint      `json:"topic_id"`
	ConversationKind string    `json:"conversation_kind"`
	TargetID         int64     `json:"target_id"`
	Status           string    `json:"status"`
	Title            string    `json:"title"`
	GistPreview      string    `json:"gist_preview"`
	TimeFrom         time.Time `json:"time_from,omitempty"`
	TimeTo           time.Time `json:"time_to,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
	OpenLoops        []string  `json:"open_loops,omitempty"`
	AnchorMessageIDs []uint    `json:"anchor_message_ids,omitempty"`
	LatestSummaryID  uint      `json:"latest_summary_id"`
	LatestSummaryVer uint64    `json:"latest_summary_version"`
	MatchedKeywords  []string  `json:"matched_keywords,omitempty"`
	TitleHits        int       `json:"-"`
	Similarity       float64   `json:"-"`
}

type TopicSummaryVersion struct {
	ID        uint      `json:"summary_id"`
	Version   uint64    `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

type TopicDetail struct {
	Topic            TopicThreadRow        `json:"topic"`
	Summary          TopicSummary          `json:"summary"`
	SummaryID        uint                  `json:"summary_id"`
	SummaryVersion   uint64                `json:"summary_version"`
	SourceMessageIDs []uint                `json:"source_message_ids,omitempty"`
	AnchorMessageIDs []uint                `json:"anchor_message_ids,omitempty"`
	SummaryHistory   []TopicSummaryVersion `json:"summary_history,omitempty"`
}

type CompactionTopicInput struct {
	Ref        string
	TopicID    uint
	MessageIDs []uint
	AnchorIDs  []uint
	SourceIDs  []uint
	Summary    TopicSummary
}

type CompactionCommit struct {
	ConversationKind  string
	TargetID          int64
	ExpectedVersion   uint64
	ThroughVisibleSeq uint64
	ThroughMessageID  uint
	Overview          string
	ActiveTopicRefs   []string
	Topics            []CompactionTopicInput
	NoTopicIDs        []uint
	CoveredMessageIDs []uint
	ReferenceBaseline string
	ReferenceVersions []string
	BatchID           string
	OutputTokens      int
	MaxInputTokens    int
}

type CompactionCommitResult struct {
	TopicIDs          map[string]uint `json:"topic_ids"`
	CheckpointVersion uint64          `json:"checkpoint_version"`
}

func summaryTitle(summary TopicSummary, topicID uint) string {
	if title := strings.TrimSpace(summary.Title); title != "" {
		return title
	}
	return fmt.Sprintf("话题 %d", topicID)
}

func previewText(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "…"
}

func summaryMatches(summary TopicSummary, keywords []string) []string {
	if len(keywords) == 0 {
		return nil
	}
	text := strings.ToLower(strings.Join([]string{summary.Title, summary.Gist, strings.Join(summary.Keywords, " "), strings.Join(summary.OpenLoops, " ")}, " "))
	var matched []string
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		if strings.Contains(text, strings.ToLower(keyword)) {
			matched = append(matched, keyword)
		}
	}
	return matched
}

func normalizeKeywords(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	if len(result) > 6 {
		return result[:6]
	}
	return result
}

func titleHitCount(title string, keywords []string) int {
	title = strings.ToLower(title)
	count := 0
	for _, keyword := range keywords {
		if strings.Contains(title, strings.ToLower(keyword)) {
			count++
		}
	}
	return count
}

func keywordSimilarity(matched, keywords []string) float64 {
	if len(keywords) == 0 {
		return 0
	}
	return float64(len(matched)) / float64(len(keywords))
}

// SearchTopics 按关键词和会话来源查询话题；没有关键词时用于后台浏览
func (m *Manager) SearchTopics(ctx context.Context, query TopicSearchQuery) ([]TopicHit, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 200 {
		limit = 200
	}
	offset := max(0, query.Offset)
	status := strings.TrimSpace(query.Status)
	if status == "all" {
		status = ""
	}
	var topics []TopicThreadRow
	q := m.db.WithContext(ctx).Where("1=1")
	if query.ConversationKind != "" {
		q = q.Where("conversation_kind=?", query.ConversationKind)
	}
	if query.TargetID > 0 {
		q = q.Where("target_id=?", query.TargetID)
	}
	if status != "" {
		q = q.Where("status=?", status)
	}
	keywords := normalizeKeywords(query.Keywords)
	if len(keywords) > 0 {
		conditions := make([]string, 0, len(keywords))
		args := make([]any, 0, len(keywords)*4)
		for _, keyword := range keywords {
			pattern := "%" + strings.ToLower(keyword) + "%"
			conditions = append(conditions, "(lower(ts.summary_json::text) LIKE ? OR (ts.summary_json->>'title') % ? OR (ts.summary_json->>'gist') % ? OR ((ts.summary_json->'keywords')::text) % ?)")
			args = append(args, pattern, keyword, keyword, keyword)
		}
		q = q.Where("EXISTS (SELECT 1 FROM topic_summaries ts WHERE ts.topic_id=topic_threads.id AND ("+strings.Join(conditions, " OR ")+"))", args...)
	}
	if len(keywords) == 0 {
		q = q.Order("updated_at DESC,id DESC").Offset(offset).Limit(limit)
	} else {
		q = q.Order("updated_at DESC,id DESC")
	}
	if err := q.Find(&topics).Error; err != nil {
		return nil, err
	}
	if len(keywords) == 0 {
		offset = 0
	}

	hits := make([]TopicHit, 0, min(limit, len(topics)))
	for _, topic := range topics {
		var row TopicSummaryRow
		err := m.db.WithContext(ctx).Where("topic_id=?", topic.ID).Order("version DESC,id DESC").Take(&row).Error
		if err == gorm.ErrRecordNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		var summary TopicSummary
		if err := sonic.UnmarshalString(row.SummaryJSON, &summary); err != nil {
			continue
		}
		matched := summaryMatches(summary, keywords)
		if len(keywords) > 0 && len(matched) == 0 {
			continue
		}
		var anchors []uint
		if err := m.db.WithContext(ctx).Model(&TopicSourceRow{}).Where("summary_id=? AND role='anchor'", row.ID).Order("message_log_id").Pluck("message_log_id", &anchors).Error; err != nil {
			return nil, err
		}
		var period struct {
			From *time.Time
			To   *time.Time
		}
		if err := m.db.WithContext(ctx).Raw(`SELECT min(message_time) AS "from",max(message_time) AS "to"
			FROM message_logs WHERE id IN (
				SELECT message_log_id FROM topic_assignments WHERE topic_id=?
				UNION
				SELECT src.message_log_id FROM topic_summary_sources src
				JOIN topic_summaries ts ON ts.id=src.summary_id WHERE ts.topic_id=?
			)`, topic.ID, topic.ID).Scan(&period).Error; err != nil {
			return nil, err
		}
		hit := TopicHit{
			TopicID:          topic.ID,
			ConversationKind: topic.ConversationKind,
			TargetID:         topic.TargetID,
			Status:           topic.Status,
			UpdatedAt:        topic.UpdatedAt,
			Title:            summaryTitle(summary, topic.ID),
			GistPreview:      previewText(summary.Gist, 180),
			OpenLoops:        append([]string(nil), summary.OpenLoops...),
			AnchorMessageIDs: anchors,
			LatestSummaryID:  row.ID,
			LatestSummaryVer: row.Version,
			MatchedKeywords:  matched,
			TitleHits:        titleHitCount(summary.Title, keywords),
			Similarity:       keywordSimilarity(matched, keywords),
		}
		if period.From != nil {
			hit.TimeFrom = *period.From
		}
		if period.To != nil {
			hit.TimeTo = *period.To
		}
		hits = append(hits, hit)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if len(hits[i].MatchedKeywords) != len(hits[j].MatchedKeywords) {
			return len(hits[i].MatchedKeywords) > len(hits[j].MatchedKeywords)
		}
		if hits[i].TitleHits != hits[j].TitleHits {
			return hits[i].TitleHits > hits[j].TitleHits
		}
		if hits[i].Similarity != hits[j].Similarity {
			return hits[i].Similarity > hits[j].Similarity
		}
		if !hits[i].UpdatedAt.Equal(hits[j].UpdatedAt) {
			return hits[i].UpdatedAt.After(hits[j].UpdatedAt)
		}
		return hits[i].TopicID > hits[j].TopicID
	})
	if offset >= len(hits) {
		return nil, nil
	}
	hits = hits[offset:]
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// ReadTopic 读取最新摘要、anchor、source 和版本历史
func (m *Manager) ReadTopic(ctx context.Context, topicID uint) (*TopicDetail, error) {
	if topicID == 0 {
		return nil, fmt.Errorf("话题编号不能为空")
	}
	var topic TopicThreadRow
	if err := m.db.WithContext(ctx).Where("id=?", topicID).Take(&topic).Error; err != nil {
		return nil, err
	}
	var history []TopicSummaryRow
	if err := m.db.WithContext(ctx).Where("topic_id=?", topicID).Order("version DESC,id DESC").Limit(20).Find(&history).Error; err != nil {
		return nil, err
	}
	if len(history) == 0 {
		return &TopicDetail{Topic: topic}, nil
	}
	latest := history[0]
	var summary TopicSummary
	if err := sonic.UnmarshalString(latest.SummaryJSON, &summary); err != nil {
		return nil, err
	}
	var sources []TopicSourceRow
	if err := m.db.WithContext(ctx).Where("summary_id=?", latest.ID).Order("message_log_id,role").Find(&sources).Error; err != nil {
		return nil, err
	}
	detail := &TopicDetail{Topic: topic, Summary: summary, SummaryID: latest.ID, SummaryVersion: latest.Version}
	for _, version := range history {
		detail.SummaryHistory = append(detail.SummaryHistory, TopicSummaryVersion{ID: version.ID, Version: version.Version, CreatedAt: version.CreatedAt})
	}
	for _, source := range sources {
		if source.Role == "anchor" {
			detail.AnchorMessageIDs = append(detail.AnchorMessageIDs, source.MessageLogID)
		} else {
			detail.SourceMessageIDs = append(detail.SourceMessageIDs, source.MessageLogID)
		}
	}
	return detail, nil
}
