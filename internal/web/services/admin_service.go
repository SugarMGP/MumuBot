package services

import (
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
	GroupID                      int64
	Status, Keyword, Sort, Order string
	Page, PageSize               int
}

type MemoryFilter struct {
	GroupID                            int64
	Kind, Status, Keyword, Sort, Order string
	Page, PageSize                     int
}

type TopicThreadView struct {
	ID                   uint
	GroupID              int64
	CreatedAt, UpdatedAt time.Time
	LastAssignmentID     uint
	Summaries            []memory.TopicSummaryRecord
}

func (t TopicThreadView) LatestSummary() *memory.TopicSummaryRecord {
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
}

type AdminService struct {
	db         *gorm.DB
	memory     *memory.Manager
	stickerDir string
}

type OverviewStats struct{ MemoryCount, MemberCount, CandidateCount, RelationCount, StickerCount int64 }

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
		{&memory.KnowledgeItem{}, &out.MemoryCount}, {&memory.MemberProfile{}, &out.MemberCount},
		{&memory.KnowledgeRelation{}, &out.RelationCount},
		{&memory.Sticker{}, &out.StickerCount},
	}
	for _, item := range items {
		if err := s.db.Model(item.model).Count(item.count).Error; err != nil {
			return out, err
		}
	}
	if err := s.db.Model(&memory.KnowledgeItem{}).Where("status = ?", "candidate").Count(&out.CandidateCount).Error; err != nil {
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
	base := s.db.Model(&memory.TopicThread{})
	if f.GroupID > 0 {
		base = base.Where("group_id = ?", f.GroupID)
	}
	if k := strings.TrimSpace(f.Keyword); k != "" {
		base = base.Where("EXISTS (SELECT 1 FROM topic_assignments ta JOIN topic_summaries ts ON ts.through_topic_assignment_id = ta.id WHERE ta.topic_id = topic_threads.id AND ts.summary_json::text ILIKE ?)", "%"+k+"%")
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return Page[TopicThreadView]{}, err
	}
	base = base.Order("(SELECT MAX(ml.message_time) FROM topic_assignments ta JOIN message_logs ml ON ml.id = ta.message_log_id WHERE ta.topic_id = topic_threads.id) DESC NULLS LAST").
		Order("(SELECT MAX(ml.id) FROM topic_assignments ta JOIN message_logs ml ON ml.id = ta.message_log_id WHERE ta.topic_id = topic_threads.id) DESC NULLS LAST").
		Order("id DESC")
	var threads []memory.TopicThread
	if err := base.Offset((page - 1) * size).Limit(size).Find(&threads).Error; err != nil {
		return Page[TopicThreadView]{}, err
	}
	items, err := s.loadTopicThreads(threads)
	if err != nil {
		return Page[TopicThreadView]{}, err
	}
	return Page[TopicThreadView]{Items: items, Total: total, Page: page, PageSize: size}, nil
}

func (s *AdminService) loadTopicThread(thread memory.TopicThread) (TopicThreadView, error) {
	view := TopicThreadView{ID: thread.ID, GroupID: thread.GroupID, CreatedAt: thread.CreatedAt, UpdatedAt: thread.CreatedAt}
	var activity struct {
		LastAssignmentID uint
		UpdatedAt        *time.Time
	}
	if err := s.db.Table("topic_assignments ta").Select("COALESCE(MAX(ta.id), 0) AS last_assignment_id, MAX(ml.message_time) AS updated_at").Joins("JOIN message_logs ml ON ml.id=ta.message_log_id").Where("ta.topic_id = ?", thread.ID).Scan(&activity).Error; err != nil {
		return view, err
	}
	view.LastAssignmentID = activity.LastAssignmentID
	if activity.UpdatedAt != nil {
		view.UpdatedAt = *activity.UpdatedAt
	}
	err := s.db.Table("topic_summaries ts").Select("ts.*, ("+memory.TopicSummaryValiditySQL+") AS sources_valid").Joins("JOIN topic_assignments ta ON ta.id = ts.through_topic_assignment_id").Where("ta.topic_id = ?", thread.ID).Order("ts.id ASC").Scan(&view.Summaries).Error
	return view, err
}

func (s *AdminService) loadTopicThreads(threads []memory.TopicThread) ([]TopicThreadView, error) {
	if len(threads) == 0 {
		return []TopicThreadView{}, nil
	}
	ids := make([]uint, len(threads))
	for i, thread := range threads {
		ids[i] = thread.ID
	}
	type activityRow struct {
		TopicID          uint
		LastAssignmentID uint
		UpdatedAt        *time.Time
	}
	var assignments []activityRow
	if err := s.db.Table("topic_assignments ta").Select("ta.topic_id, COALESCE(MAX(ta.id), 0) AS last_assignment_id, MAX(ml.message_time) AS updated_at").Joins("JOIN message_logs ml ON ml.id=ta.message_log_id").Where("ta.topic_id IN ?", ids).Group("ta.topic_id").Scan(&assignments).Error; err != nil {
		return nil, err
	}
	activityByTopic := make(map[uint]activityRow, len(assignments))
	for _, row := range assignments {
		activityByTopic[row.TopicID] = row
	}
	var summaries []struct {
		memory.TopicSummaryRecord
		TopicID uint
	}
	if err := s.db.Table("topic_summaries ts").Select("ts.*, ta.topic_id, ("+memory.TopicSummaryValiditySQL+") AS sources_valid").Joins("JOIN topic_assignments ta ON ta.id = ts.through_topic_assignment_id").Where("ta.topic_id IN ?", ids).Order("ts.id ASC").Scan(&summaries).Error; err != nil {
		return nil, err
	}
	summariesByTopic := make(map[uint][]memory.TopicSummaryRecord, len(ids))
	for _, summary := range summaries {
		if summary.TopicID != 0 {
			summariesByTopic[summary.TopicID] = append(summariesByTopic[summary.TopicID], summary.TopicSummaryRecord)
		}
	}
	items := make([]TopicThreadView, 0, len(threads))
	for _, thread := range threads {
		activity := activityByTopic[thread.ID]
		view := TopicThreadView{ID: thread.ID, GroupID: thread.GroupID, CreatedAt: thread.CreatedAt, UpdatedAt: thread.CreatedAt, LastAssignmentID: activity.LastAssignmentID, Summaries: summariesByTopic[thread.ID]}
		if activity.UpdatedAt != nil {
			view.UpdatedAt = *activity.UpdatedAt
		}
		items = append(items, view)
	}
	return items, nil
}

func (s *AdminService) GetTopicThread(id uint) (TopicThreadView, error) {
	var t memory.TopicThread
	if err := s.db.First(&t, id).Error; err != nil {
		return TopicThreadView{}, err
	}
	return s.loadTopicThread(t)
}

func (s *AdminService) ListTopicMessages(topicID uint, limit int) ([]memory.MessageLog, error) {
	var out []memory.MessageLog
	q := s.db.Table("message_logs ml").Select("ml.*").Joins("JOIN topic_assignments ta ON ta.message_log_id = ml.id").Where("ta.topic_id = ?", topicID).Order("ml.id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Scan(&out).Error; err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
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
	if err := s.db.Model(&memory.KnowledgeItem{}).Select("subject_user_id AS user_id, COUNT(*) AS count").Where("subject_user_id IN ?", userIDs).Group("subject_user_id").Scan(&knowledgeCounts).Error; err != nil {
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
	participationSQL := strings.Replace(knowledgeParticipationSQL, "ml.user_id=?", "ml.user_id=mp.user_id", 1)
	if err := s.db.Table("member_profiles mp").Select("mp.user_id, COUNT(DISTINCT ki.id) AS count").
		Joins("JOIN knowledge_items ki ON "+participationSQL).Where("mp.user_id IN ?", userIDs).
		Group("mp.user_id").Scan(&participationCounts).Error; err != nil {
		return Page[MemberProfileView]{}, err
	}
	participationByUser := make(map[int64]int64, len(participationCounts))
	for _, row := range participationCounts {
		participationByUser[row.UserID] = row.Count
	}
	items := make([]MemberProfileView, 0, len(profiles))
	for _, p := range profiles {
		items = append(items, MemberProfileView{MemberProfile: p, Names: namesByUser[p.UserID], KnowledgeCount: knowledgeByUser[p.UserID], ParticipationCount: participationByUser[p.UserID]})
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
