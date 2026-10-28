package services

import (
	"slices"
	"strings"
	"time"

	"mumu-bot/internal/memory"

	"gorm.io/gorm"
)

type Page[T any] struct {
	Items          []T
	Total          int64
	Page, PageSize int
}

type ListFilter struct {
	ConversationKind     string
	TargetID             int64
	Status               string
	Keyword, Sort, Order string
	Page, PageSize       int
}

type MemoryFilter struct {
	Kind, Status, Keyword, Sort, Order string
	Page, PageSize                     int
}

type TopicSummaryView struct {
	ID          uint
	Version     uint64
	SummaryJSON string
	CreatedAt   time.Time
}

type TopicThreadView struct {
	ID               uint
	ConversationKind string
	TargetID         int64
	Status           string
	UpdatedAt        time.Time
	MessageCount     int64
	Summaries        []TopicSummaryView
	AnchorMessageIDs []uint
	SourceMessageIDs []uint
}

func (t TopicThreadView) LatestSummary() *TopicSummaryView {
	if len(t.Summaries) == 0 {
		return nil
	}
	return &t.Summaries[len(t.Summaries)-1]
}

type MemberProfileView struct {
	memory.MemberProfile
	Names              []memory.MemberName
	KnowledgeCount     int64
	ParticipationCount int64
	IntimacyLogs       []memory.MemberIntimacyLog
}

type AdminService struct {
	db         *gorm.DB
	memory     *memory.Manager
	stickerDir string
}

type OverviewStats struct{ MemoryCount, MemberCount, ActiveCount, TopicCount, StickerCount int64 }

func NewAdminService(memoryManager *memory.Manager, stickerDir string) *AdminService {
	return &AdminService{db: memoryManager.GetDB(), memory: memoryManager, stickerDir: stickerDir}
}
func (s *AdminService) StickerDir() string { return s.stickerDir }

func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

func normalizeSort(rawSort, rawOrder, fallback string, allowed ...string) (string, string) {
	sortKey := strings.ToLower(strings.TrimSpace(rawSort))
	ok := false
	for _, candidate := range allowed {
		if candidate == sortKey {
			ok = true
			break
		}
	}
	if !ok {
		sortKey = fallback
	}
	order := strings.ToLower(strings.TrimSpace(rawOrder))
	if order != "asc" {
		order = "desc"
	}
	return sortKey, order
}

func NormalizeMemorySort(s, o string) (string, string) {
	return normalizeSort(s, o, "updated", "updated", "created")
}
func NormalizeStickerSort(s, o string) (string, string) {
	return normalizeSort(s, o, "use", "use", "updated", "created")
}
func NormalizeMemberSort(s, o string) (string, string) {
	return normalizeSort(s, o, "messages", "messages", "recent")
}

func order(q *gorm.DB, key, direction string, columns map[string]string) *gorm.DB {
	column := columns[key]
	if column == "" {
		column = columns["default"]
	}
	if direction == "asc" {
		return q.Order(column + " ASC").Order("id ASC")
	}
	return q.Order(column + " DESC").Order("id DESC")
}

func (s *AdminService) OverviewStats() (OverviewStats, error) {
	var out OverviewStats
	items := []struct {
		model any
		count *int64
	}{
		{&memory.KnowledgeRecord{}, &out.MemoryCount}, {&memory.MemberProfile{}, &out.MemberCount},
		{&memory.TopicThreadRow{}, &out.TopicCount}, {&memory.Sticker{}, &out.StickerCount},
	}
	for _, item := range items {
		if err := s.db.Model(item.model).Count(item.count).Error; err != nil {
			return out, err
		}
	}
	if err := s.db.Model(&memory.KnowledgeRecord{}).Where("status = ?", "active").Count(&out.ActiveCount).Error; err != nil {
		return out, err
	}
	return out, nil
}

func paginate[T any](q *gorm.DB, page, size int, items *[]T) (Page[T], error) {
	page, size = normalizePage(page, size)
	out := Page[T]{Page: page, PageSize: size}
	if err := q.Count(&out.Total).Error; err != nil {
		return out, err
	}
	if err := q.Offset((page - 1) * size).Limit(size).Find(items).Error; err != nil {
		return out, err
	}
	out.Items = *items
	return out, nil
}

func (s *AdminService) ListTopicThreads(f ListFilter) (Page[TopicThreadView], error) {
	page, size := normalizePage(f.Page, f.PageSize)
	base := s.db.Model(&memory.TopicThreadRow{})
	if f.ConversationKind != "" {
		base = base.Where("conversation_kind=?", f.ConversationKind)
	}
	targetID := f.TargetID
	if targetID > 0 {
		base = base.Where("target_id=?", targetID)
	}
	if f.Status != "" && f.Status != "all" {
		base = base.Where("status=?", f.Status)
	}
	if k := strings.TrimSpace(f.Keyword); k != "" {
		p := "%" + k + "%"
		base = base.Where("EXISTS (SELECT 1 FROM topic_summaries ts WHERE ts.topic_id=topic_threads.id AND (COALESCE(ts.summary_json->>'title','') ILIKE ? OR COALESCE(ts.summary_json->>'gist','') ILIKE ? OR ts.summary_json::text ILIKE ?))", p, p, p)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return Page[TopicThreadView]{}, err
	}
	var threads []memory.TopicThreadRow
	if err := base.Order("updated_at DESC,id DESC").Offset((page - 1) * size).Limit(size).Find(&threads).Error; err != nil {
		return Page[TopicThreadView]{}, err
	}
	items, err := s.loadTopicThreads(threads)
	if err != nil {
		return Page[TopicThreadView]{}, err
	}
	return Page[TopicThreadView]{Items: items, Total: total, Page: page, PageSize: size}, nil
}

func (s *AdminService) GetTopicThread(id uint) (TopicThreadView, error) {
	var thread memory.TopicThreadRow
	if err := s.db.First(&thread, id).Error; err != nil {
		return TopicThreadView{}, err
	}
	items, err := s.loadTopicThreads([]memory.TopicThreadRow{thread})
	if err != nil {
		return TopicThreadView{}, err
	}
	return items[0], nil
}

func (s *AdminService) loadTopicThreads(threads []memory.TopicThreadRow) ([]TopicThreadView, error) {
	if len(threads) == 0 {
		return []TopicThreadView{}, nil
	}
	ids := make([]uint, len(threads))
	for i, thread := range threads {
		ids[i] = thread.ID
	}
	var summaries []memory.TopicSummaryRow
	if err := s.db.Raw(`SELECT id,topic_id,version,summary_json,created_at FROM (
		SELECT ts.*,row_number() OVER(PARTITION BY topic_id ORDER BY version DESC,id DESC) rn
		FROM topic_summaries ts WHERE topic_id IN ?
	) ranked WHERE rn<=10 ORDER BY topic_id,version ASC,id ASC`, ids).Scan(&summaries).Error; err != nil {
		return nil, err
	}
	summariesByTopic := make(map[uint][]TopicSummaryView, len(ids))
	latestByTopic := make(map[uint]uint, len(ids))
	for _, row := range summaries {
		view := TopicSummaryView{ID: row.ID, Version: row.Version, SummaryJSON: row.SummaryJSON, CreatedAt: row.CreatedAt}
		items := summariesByTopic[row.TopicID]
		items = append(items, view)
		summariesByTopic[row.TopicID] = items
		latestByTopic[row.TopicID] = row.ID
	}
	latestSummaryIDs := make([]uint, 0, len(latestByTopic))
	for _, id := range ids {
		if summaryID, ok := latestByTopic[id]; ok {
			latestSummaryIDs = append(latestSummaryIDs, summaryID)
		}
	}
	anchors := map[uint][]uint{}
	sources := map[uint][]uint{}
	var sourceRows []memory.TopicSourceRow
	if len(latestSummaryIDs) > 0 {
		if err := s.db.Where("summary_id IN ?", latestSummaryIDs).Order("message_log_id").Find(&sourceRows).Error; err != nil {
			return nil, err
		}
		for _, row := range sourceRows {
			if row.Role == "anchor" {
				anchors[row.SummaryID] = append(anchors[row.SummaryID], row.MessageLogID)
			} else {
				sources[row.SummaryID] = append(sources[row.SummaryID], row.MessageLogID)
			}
		}
	}
	var assignmentRows []struct {
		TopicID      uint
		MessageLogID uint
	}
	if err := s.db.Table("topic_assignments").Select("topic_id,message_log_id").Where("topic_id IN ?", ids).Scan(&assignmentRows).Error; err != nil {
		return nil, err
	}
	messagesByTopic := make(map[uint]map[uint]bool, len(ids))
	addMessage := func(topicID, messageID uint) {
		if topicID == 0 || messageID == 0 {
			return
		}
		if messagesByTopic[topicID] == nil {
			messagesByTopic[topicID] = map[uint]bool{}
		}
		messagesByTopic[topicID][messageID] = true
	}
	for _, row := range assignmentRows {
		addMessage(row.TopicID, row.MessageLogID)
	}
	latestTopicBySummary := make(map[uint]uint, len(latestByTopic))
	for topicID, summaryID := range latestByTopic {
		latestTopicBySummary[summaryID] = topicID
	}
	for _, row := range sourceRows {
		addMessage(latestTopicBySummary[row.SummaryID], row.MessageLogID)
	}
	countByTopic := make(map[uint]int64, len(messagesByTopic))
	for topicID, messages := range messagesByTopic {
		countByTopic[topicID] = int64(len(messages))
	}
	items := make([]TopicThreadView, 0, len(threads))
	for _, thread := range threads {
		view := TopicThreadView{ID: thread.ID, ConversationKind: thread.ConversationKind, TargetID: thread.TargetID, Status: thread.Status, UpdatedAt: thread.UpdatedAt, MessageCount: countByTopic[thread.ID], Summaries: summariesByTopic[thread.ID]}
		if latest := view.LatestSummary(); latest != nil {
			view.AnchorMessageIDs = anchors[latest.ID]
			view.SourceMessageIDs = sources[latest.ID]
		}
		items = append(items, view)
	}
	return items, nil
}

func (s *AdminService) ListTopicMessages(topicID uint, limit int) ([]memory.MessageLog, error) {
	var out []memory.MessageLog
	q := s.db.Table("message_logs ml").Select("DISTINCT ml.*").
		Joins("LEFT JOIN topic_assignments ta ON ta.message_log_id=ml.id").
		Where("ta.topic_id=? OR ml.id IN (SELECT src.message_log_id FROM topic_summary_sources src JOIN topic_summaries ts ON ts.id=src.summary_id WHERE ts.topic_id=? AND ts.version=(SELECT MAX(version) FROM topic_summaries WHERE topic_id=?))", topicID, topicID, topicID).
		Order("ml.id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Scan(&out).Error; err != nil {
		return nil, err
	}
	slices.Reverse(out)
	return out, nil
}

func (s *AdminService) ListMemberProfiles(f ListFilter) (Page[MemberProfileView], error) {
	page, size := normalizePage(f.Page, f.PageSize)
	q := s.db.Model(&memory.MemberProfile{})
	if k := strings.TrimSpace(f.Keyword); k != "" {
		p := "%" + k + "%"
		q = q.Where("nickname ILIKE ? OR CAST(user_id AS TEXT) ILIKE ? OR EXISTS (SELECT 1 FROM member_names mn WHERE mn.user_id=member_profiles.user_id AND mn.value ILIKE ?)", p, p, p)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return Page[MemberProfileView]{}, err
	}
	if f.Sort == "recent" {
		q = q.Order("last_seen_at " + strings.ToUpper(f.Order))
	} else {
		q = q.Order("message_count " + strings.ToUpper(f.Order))
	}
	var profiles []memory.MemberProfile
	if err := q.Offset((page - 1) * size).Limit(size).Find(&profiles).Error; err != nil {
		return Page[MemberProfileView]{}, err
	}
	if len(profiles) == 0 {
		return Page[MemberProfileView]{Items: []MemberProfileView{}, Total: total, Page: page, PageSize: size}, nil
	}
	userIDs := make([]int64, len(profiles))
	for i, p := range profiles {
		userIDs[i] = p.UserID
	}
	var names []memory.MemberName
	if err := s.db.Where("user_id IN ?", userIDs).Order("updated_at DESC").Find(&names).Error; err != nil {
		return Page[MemberProfileView]{}, err
	}
	namesByUser := make(map[int64][]memory.MemberName, len(userIDs))
	for _, name := range names {
		namesByUser[name.UserID] = append(namesByUser[name.UserID], name)
	}
	var knowledgeCounts []struct {
		UserID int64
		Count  int64
	}
	if err := s.filterKnowledge(s.db.Table("knowledge_items ki"), KnowledgeFilter{}).
		Select("ki.subject_user_id AS user_id, COUNT(*) AS count").Where("ki.subject_user_id IN ?", userIDs).
		Group("ki.subject_user_id").Scan(&knowledgeCounts).Error; err != nil {
		return Page[MemberProfileView]{}, err
	}
	knowledgeByUser := make(map[int64]int64, len(knowledgeCounts))
	for _, row := range knowledgeCounts {
		knowledgeByUser[row.UserID] = row.Count
	}
	var participationCounts []struct {
		UserID int64
		Count  int64
	}
	if err := s.filterKnowledge(s.db.Table("member_profiles mp").Joins("JOIN knowledge_items ki ON "+knowledgeParticipationSQL("mp.user_id")), KnowledgeFilter{}).
		Select("mp.user_id, COUNT(DISTINCT ki.id) AS count").Where("mp.user_id IN ?", userIDs).
		Group("mp.user_id").Scan(&participationCounts).Error; err != nil {
		return Page[MemberProfileView]{}, err
	}
	participationByUser := make(map[int64]int64, len(participationCounts))
	for _, row := range participationCounts {
		participationByUser[row.UserID] = row.Count
	}
	intimacyLogs, err := s.memory.RecentIntimacyLogs(userIDs, 8)
	if err != nil {
		return Page[MemberProfileView]{}, err
	}
	items := make([]MemberProfileView, 0, len(profiles))
	for _, p := range profiles {
		items = append(items, MemberProfileView{MemberProfile: p, Names: namesByUser[p.UserID], KnowledgeCount: knowledgeByUser[p.UserID], ParticipationCount: participationByUser[p.UserID], IntimacyLogs: intimacyLogs[p.UserID]})
	}
	return Page[MemberProfileView]{Items: items, Total: total, Page: page, PageSize: size}, nil
}

func (s *AdminService) ListStickers(f ListFilter) (Page[memory.Sticker], error) {
	var items []memory.Sticker
	q := s.db.Model(&memory.Sticker{})
	if k := strings.TrimSpace(f.Keyword); k != "" {
		p := "%" + k + "%"
		q = q.Where("description ILIKE ? OR file_name ILIKE ? OR file_hash ILIKE ?", p, p, p)
	}
	q = order(q, f.Sort, f.Order, map[string]string{"use": "use_count", "updated": "updated_at", "created": "created_at", "default": "use_count"})
	return paginate(q, f.Page, f.PageSize, &items)
}
func (s *AdminService) GetSticker(id uint) (memory.Sticker, error) {
	var v memory.Sticker
	return v, s.db.First(&v, id).Error
}
