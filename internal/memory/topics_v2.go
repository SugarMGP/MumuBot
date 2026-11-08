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
	Match            string
	ExcludeKeywords  []string
	ConversationKind string
	TargetID         int64
	Status           string
	From             *time.Time
	To               *time.Time
	Cursor           string
	Limit            int
}

type TopicSearchFilter struct {
	Keywords         []string   `json:"keywords"`
	Match            string     `json:"match"`
	ExcludeKeywords  []string   `json:"exclude_keywords"`
	ConversationKind string     `json:"conversation_kind"`
	TargetID         int64      `json:"target_id"`
	Status           string     `json:"status"`
	From             *time.Time `json:"from,omitempty"`
	To               *time.Time `json:"to,omitempty"`
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
	AnchorMessageIDs []int64   `json:"anchor_message_ids,omitempty"`
	LatestSummaryID  uint      `json:"latest_summary_id"`
	LatestSummaryVer uint64    `json:"latest_summary_version"`
	Similarity       float64   `json:"-"`
}

type TopicPage struct {
	Items      []TopicHit `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
	HasMore    bool       `json:"has_more"`
}

type TopicSummaryVersion struct {
	ID        uint      `json:"summary_id"`
	Version   uint64    `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

type TopicDetail struct {
	Topic             TopicThreadRow        `json:"topic"`
	Summary           TopicSummary          `json:"summary"`
	SummaryID         uint                  `json:"summary_id"`
	SummaryVersion    uint64                `json:"summary_version"`
	SourceMessageIDs  []int64               `json:"source_message_ids,omitempty"`
	AnchorMessageIDs  []int64               `json:"anchor_message_ids,omitempty"`
	SourceTotal       int                   `json:"source_total"`
	SourceAvailable   int                   `json:"source_available"`
	SourceUnavailable int                   `json:"source_unavailable"`
	SummaryHistory    []TopicSummaryVersion `json:"summary_history,omitempty"`
}

type TopicMessageRef struct {
	MessageID int64 `json:"message_id" jsonschema:"description=当前会话真实 OneBot 消息 ID，可为负数；不能用 transcript 条目号替代"`
	IsAnchor  bool  `json:"is_anchor" jsonschema:"description=是否为关键原文依据：能直接支撑话题结论、决定、约定、纠正或重要转折才填 true；普通背景、寒暄和重复内容填 false。用于在 searchTopics/readTopic 返回关键消息 ID，供按原文核对，也在后台标记关键消息；不代表置顶，不改变遗忘或原文保留期。不凑数量，没有关键依据可以全为 false"`
}

type CompactionTopicInput struct {
	Ref      string
	TopicID  uint
	Messages []TopicMessageRef
	Summary  TopicSummary
}

type CompactionCommit struct {
	ConversationKind    string
	TargetID            int64
	ExpectedVersion     uint64
	ThroughVisibleSeq   uint64
	ThroughMessageID    uint
	Overview            string
	ActiveTopicRefs     []string
	Topics              []CompactionTopicInput
	CandidateMessageIDs []uint
	Coverage            float64
	UnassignedCount     int
	BatchID             string
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

func summaryText(summary TopicSummary) string {
	return strings.Join([]string{
		summary.Title, summary.Gist, strings.Join(summary.RecentTurns, " "),
		strings.Join(summary.OpenLoops, " "), strings.Join(summary.Keywords, " "),
	}, " ")
}

func topicKeywordMatch(summary TopicSummary, keywords []string, match string) bool {
	if len(keywords) == 0 {
		return true
	}
	text := summaryText(summary)
	matched := 0
	for _, keyword := range keywords {
		if textSimilarity(text, keyword) >= topicSimilarityThreshold {
			matched++
		}
	}
	if match == "and" {
		return matched == len(keywords)
	}
	return matched > 0
}

func topicKeywordScore(summary TopicSummary, keywords []string, match string) float64 {
	if len(keywords) == 0 {
		return 0
	}
	text := summaryText(summary)
	scores := make([]float64, 0, len(keywords))
	for _, keyword := range keywords {
		scores = append(scores, textSimilarity(text, keyword))
	}
	if len(scores) == 0 {
		return 0
	}
	score := scores[0]
	for _, current := range scores[1:] {
		if match == "and" && current < score {
			score = current
		}
		if match == "or" && current > score {
			score = current
		}
	}
	return score
}

func topicFilter(query TopicSearchQuery, match string) TopicSearchFilter {
	return TopicSearchFilter{
		Keywords: NormalizeKeywords(query.Keywords), Match: match,
		ExcludeKeywords:  NormalizeKeywords(query.ExcludeKeywords),
		ConversationKind: strings.TrimSpace(query.ConversationKind), TargetID: query.TargetID,
		Status: strings.TrimSpace(query.Status), From: query.From, To: query.To,
	}
}

type topicPeriod struct {
	From *time.Time
	To   *time.Time
}

// latestTopicSummaries 一次读取每个话题的最新摘要版本
func latestTopicSummaries(db *gorm.DB, topicIDs []uint) (map[uint]TopicSummaryRow, error) {
	result := make(map[uint]TopicSummaryRow, len(topicIDs))
	if len(topicIDs) == 0 {
		return result, nil
	}
	latest := db.Table("topic_summaries").Select("topic_id,MAX(version) AS version").
		Where("topic_id IN ?", topicIDs).Group("topic_id")
	var rows []TopicSummaryRow
	if err := db.Table("topic_summaries ts").Select("ts.*").
		Joins("JOIN (?) latest ON latest.topic_id=ts.topic_id AND latest.version=ts.version", latest).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.TopicID] = row
	}
	return result, nil
}

// topicAnchorOneBotIDs 直接从来源行读取关键消息的 OneBot ID，原文删除后仍然可用
func (m *Manager) topicAnchorOneBotIDs(ctx context.Context, summaryIDs []uint) (map[uint][]int64, error) {
	result := make(map[uint][]int64, len(summaryIDs))
	if len(summaryIDs) == 0 {
		return result, nil
	}
	var rows []TopicSourceRow
	if err := m.db.WithContext(ctx).Model(&TopicSourceRow{}).
		Where("summary_id IN ? AND is_anchor=true", summaryIDs).
		Order("summary_id,message_log_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.OneBotMessageID == 0 {
			continue
		}
		result[row.SummaryID] = append(result[row.SummaryID], row.OneBotMessageID)
	}
	return result, nil
}

func (m *Manager) topicPeriods(ctx context.Context, topicIDs []uint) (map[uint]topicPeriod, error) {
	result := make(map[uint]topicPeriod, len(topicIDs))
	if len(topicIDs) == 0 {
		return result, nil
	}
	var assigned []struct {
		TopicID uint
		From    *time.Time
		To      *time.Time
	}
	if err := m.db.WithContext(ctx).Table("topic_assignments ta").
		Select("ta.topic_id,MIN(ml.message_time) AS \"from\",MAX(ml.message_time) AS \"to\"").
		Joins("JOIN message_logs ml ON ml.id=ta.message_log_id").
		Where("ta.topic_id IN ?", topicIDs).Group("ta.topic_id").Scan(&assigned).Error; err != nil {
		return nil, err
	}
	var sourced []struct {
		TopicID uint
		From    *time.Time
		To      *time.Time
	}
	if err := m.db.WithContext(ctx).Table("topic_summary_sources src").
		Select("ts.topic_id,MIN(src.message_time) AS \"from\",MAX(src.message_time) AS \"to\"").
		Joins("JOIN topic_summaries ts ON ts.id=src.summary_id").
		Where("ts.topic_id IN ?", topicIDs).Group("ts.topic_id").Scan(&sourced).Error; err != nil {
		return nil, err
	}
	merge := func(topicID uint, from, to *time.Time) {
		if topicID == 0 {
			return
		}
		period := result[topicID]
		if from != nil && (period.From == nil || from.Before(*period.From)) {
			period.From = from
		}
		if to != nil && (period.To == nil || to.After(*period.To)) {
			period.To = to
		}
		result[topicID] = period
	}
	for _, row := range assigned {
		merge(row.TopicID, row.From, row.To)
	}
	for _, row := range sourced {
		merge(row.TopicID, row.From, row.To)
	}
	return result, nil
}

func (m *Manager) SearchTopics(ctx context.Context, query TopicSearchQuery) (TopicPage, error) {
	match, err := NormalizeMatch(query.Match)
	if err != nil {
		return TopicPage{}, err
	}
	filter := topicFilter(query, match)
	limit := NormalizeLimit(query.Limit)
	cursor, err := DecodeCursor(query.Cursor)
	if err != nil {
		return TopicPage{}, err
	}
	if cursor.FilterHash == "" {
		cursor.FilterHash = CursorFilterHash(filter)
	}
	if err := ValidateCursorFilter(cursor, filter); err != nil {
		return TopicPage{}, err
	}
	snapshot := CursorTimeOrNow(cursor)
	status := filter.Status
	if status == "all" {
		status = ""
	}
	now := time.Now()
	q := m.db.WithContext(ctx).Where("updated_at<=? AND forgotten_at IS NULL AND forget_at>?", snapshot, now)
	if filter.ConversationKind != "" {
		q = q.Where("conversation_kind=?", filter.ConversationKind)
	}
	if filter.TargetID > 0 {
		q = q.Where("target_id=?", filter.TargetID)
	}
	if status != "" {
		q = q.Where("status=?", status)
	}
	// 时间范围按来源消息判断：话题归属或摘要来源任一命中即可，不复制话题
	if filter.From != nil || filter.To != nil {
		condition := "(EXISTS (SELECT 1 FROM topic_assignments ta JOIN message_logs ml ON ml.id=ta.message_log_id" +
			" WHERE ta.topic_id=topic_threads.id"
		args := make([]any, 0, 2)
		if filter.From != nil {
			condition += " AND ml.message_time>=?"
			args = append(args, *filter.From)
		}
		if filter.To != nil {
			condition += " AND ml.message_time<?"
			args = append(args, *filter.To)
		}
		condition += ") OR EXISTS (SELECT 1 FROM topic_summary_sources src JOIN topic_summaries ts ON ts.id=src.summary_id" +
			" WHERE ts.topic_id=topic_threads.id"
		if filter.From != nil {
			condition += " AND src.message_time>=?"
			args = append(args, *filter.From)
		}
		if filter.To != nil {
			condition += " AND src.message_time<?"
			args = append(args, *filter.To)
		}
		condition += "))"
		q = q.Where(condition, args...)
	}
	var topics []TopicThreadRow
	if err := q.Order("updated_at DESC,id DESC").Find(&topics).Error; err != nil {
		return TopicPage{}, err
	}
	keywords := NormalizeKeywords(query.Keywords)
	excludes := NormalizeKeywords(query.ExcludeKeywords)
	topicIDs := make([]uint, 0, len(topics))
	for _, topic := range topics {
		topicIDs = append(topicIDs, topic.ID)
	}
	latest, err := latestTopicSummaries(m.db.WithContext(ctx), topicIDs)
	if err != nil {
		return TopicPage{}, err
	}
	summaryIDs := make([]uint, 0, len(latest))
	for _, row := range latest {
		summaryIDs = append(summaryIDs, row.ID)
	}
	anchors, err := m.topicAnchorOneBotIDs(ctx, summaryIDs)
	if err != nil {
		return TopicPage{}, err
	}
	periods, err := m.topicPeriods(ctx, topicIDs)
	if err != nil {
		return TopicPage{}, err
	}
	hits := make([]TopicHit, 0, len(topics))
	for _, topic := range topics {
		row, ok := latest[topic.ID]
		if !ok {
			continue
		}
		var summary TopicSummary
		if err := sonic.UnmarshalString(row.SummaryJSON, &summary); err != nil {
			continue
		}
		if !topicKeywordMatch(summary, keywords, match) {
			continue
		}
		excluded := false
		for _, keyword := range excludes {
			if strings.Contains(strings.ToLower(summaryText(summary)), strings.ToLower(keyword)) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		anchorMessageIDs := append([]int64(nil), anchors[row.ID]...)
		period := periods[topic.ID]
		hit := TopicHit{
			TopicID: topic.ID, ConversationKind: topic.ConversationKind, TargetID: topic.TargetID,
			Status: topic.Status, UpdatedAt: topic.UpdatedAt, Title: summaryTitle(summary, topic.ID),
			GistPreview: previewText(summary.Gist, 500), OpenLoops: append([]string(nil), summary.OpenLoops...),
			AnchorMessageIDs: anchorMessageIDs, LatestSummaryID: row.ID, LatestSummaryVer: row.Version,
			Similarity: topicKeywordScore(summary, keywords, match),
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
		if hits[i].Similarity != hits[j].Similarity {
			return hits[i].Similarity > hits[j].Similarity
		}
		if !hits[i].UpdatedAt.Equal(hits[j].UpdatedAt) {
			return hits[i].UpdatedAt.After(hits[j].UpdatedAt)
		}
		return hits[i].TopicID > hits[j].TopicID
	})
	if cursor.LastID != 0 {
		offset := sort.Search(len(hits), func(i int) bool {
			h := hits[i]
			if h.Similarity < cursor.LastKey {
				return true
			}
			return h.Similarity == cursor.LastKey && (h.UpdatedAt.Before(cursor.LastTime) || (h.UpdatedAt.Equal(cursor.LastTime) && uint64(h.TopicID) < cursor.LastID))
		})
		hits = hits[offset:]
	}
	hasMore := len(hits) > limit
	if hasMore {
		hits = hits[:limit]
	}
	page := TopicPage{Items: hits, HasMore: hasMore}
	if len(hits) > 0 {
		last := hits[len(hits)-1]
		page.NextCursor = NewPageCursor("similarity", last.Similarity, last.UpdatedAt, last.TopicID, snapshot, filter)
	}
	return page, nil
}

func (m *Manager) ReadTopic(ctx context.Context, topicID uint) (*TopicDetail, error) {
	if topicID == 0 {
		return nil, fmt.Errorf("话题编号不能为空")
	}
	now := time.Now()
	var topic TopicThreadRow
	err := m.db.WithContext(ctx).Where("id=? AND forgotten_at IS NULL AND forget_at>?", topicID, now).Take(&topic).Error
	if err == gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("话题不存在或已遗忘")
	}
	if err != nil {
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
	if err := m.db.WithContext(ctx).Where("summary_id=?", latest.ID).Order("message_log_id").Find(&sources).Error; err != nil {
		return nil, err
	}
	detail := &TopicDetail{Topic: topic, Summary: summary, SummaryID: latest.ID, SummaryVersion: latest.Version}
	for _, version := range history {
		detail.SummaryHistory = append(detail.SummaryHistory, TopicSummaryVersion{ID: version.ID, Version: version.Version, CreatedAt: version.CreatedAt})
	}
	detail.SourceTotal = len(sources)
	sourceIDs := make([]int64, 0)
	anchorIDs := make([]int64, 0)
	availableIDs := make([]uint, 0)
	for _, source := range sources {
		if source.OneBotMessageID != 0 {
			if source.IsAnchor {
				anchorIDs = append(anchorIDs, source.OneBotMessageID)
			} else {
				sourceIDs = append(sourceIDs, source.OneBotMessageID)
			}
		}
		availableIDs = append(availableIDs, source.MessageLogID)
	}
	if len(availableIDs) > 0 {
		var existing []uint
		if err := m.db.WithContext(ctx).Model(&MessageLog{}).Where("id IN ?", availableIDs).Pluck("id", &existing).Error; err != nil {
			return nil, err
		}
		exists := make(map[uint]bool, len(existing))
		for _, id := range existing {
			exists[id] = true
		}
		for _, source := range sources {
			if exists[source.MessageLogID] {
				detail.SourceAvailable++
			}
		}
	}
	detail.SourceUnavailable = detail.SourceTotal - detail.SourceAvailable
	detail.SourceMessageIDs = sourceIDs
	detail.AnchorMessageIDs = anchorIDs
	return detail, nil
}
