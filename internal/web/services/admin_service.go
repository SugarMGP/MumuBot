package services

import (
	"context"
	"fmt"
	"mumu-bot/internal/memory"
	"strings"
	"time"

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
	GroupID                                     int64
	Subject, Kind, Status, Keyword, Sort, Order string
	Page, PageSize                              int
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
	Names  []memory.MemberName
	Traits []memory.MemberTrait
}

type StylePatternView struct {
	memory.StylePattern
	Evidence []memory.MessageLog
}

type JargonView struct {
	memory.Jargon
	Evidence []memory.MessageLog
}

type MemoryView struct {
	memory.Memory
	Evidence []memory.MessageLog
}

type AdminService struct {
	db            *gorm.DB
	memory        *memory.Manager
	stickerDir    string
	reloadJargons func()
	selfID        func() int64
}

type OverviewStats struct{ MemoryCount, MemberCount, JargonCount, StyleCardCount, StickerCount int64 }

func NewAdminService(memoryManager *memory.Manager, stickerDir string, reloadJargons func(), selfID func() int64) *AdminService {
	return &AdminService{db: memoryManager.GetDB(), memory: memoryManager, stickerDir: stickerDir, reloadJargons: reloadJargons, selfID: selfID}
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

func NormalizeStyleCardSort(s, o string) (string, string) {
	return normalizeSort(s, o, "updated", "updated", "created")
}
func NormalizeJargonSort(s, o string) (string, string) {
	return normalizeSort(s, o, "updated", "updated", "created", "group")
}
func NormalizeMemorySort(s, o string) (string, string) {
	return normalizeSort(s, o, "updated", "updated", "created")
}
func NormalizeTopicSort(s, o string) (string, string) {
	return normalizeSort(s, o, "recent", "recent", "created", "group")
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
		{&memory.Memory{}, &out.MemoryCount}, {&memory.MemberProfile{}, &out.MemberCount},
		{&memory.Jargon{}, &out.JargonCount}, {&memory.StylePattern{}, &out.StyleCardCount},
		{&memory.Sticker{}, &out.StickerCount},
	}
	for _, item := range items {
		if err := s.db.Model(item.model).Count(item.count).Error; err != nil {
			return out, err
		}
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

func (s *AdminService) ListStyleCards(f ListFilter) (Page[StylePatternView], error) {
	var items []memory.StylePattern
	q := s.db.Model(&memory.StylePattern{})
	if f.GroupID > 0 {
		q = q.Where("group_id = ?", f.GroupID)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if k := strings.TrimSpace(f.Keyword); k != "" {
		p := "%" + k + "%"
		q = q.Where("situation ILIKE ? OR expression ILIKE ?", p, p)
	}
	q = order(q, f.Sort, f.Order, map[string]string{"updated": "updated_at", "created": "created_at", "default": "updated_at"})
	page, err := paginate(q, f.Page, f.PageSize, &items)
	if err != nil {
		return Page[StylePatternView]{}, err
	}
	evidence, err := s.stylePatternEvidence(items)
	if err != nil {
		return Page[StylePatternView]{}, err
	}
	views := make([]StylePatternView, 0, len(items))
	for _, item := range items {
		views = append(views, StylePatternView{StylePattern: item, Evidence: evidence[item.ID]})
	}
	return Page[StylePatternView]{Items: views, Total: page.Total, Page: page.Page, PageSize: page.PageSize}, nil
}

func (s *AdminService) GetStyleCard(id uint) (memory.StylePattern, error) {
	var v memory.StylePattern
	return v, s.db.First(&v, id).Error
}

func (s *AdminService) ListJargons(f ListFilter) (Page[JargonView], error) {
	var items []memory.Jargon
	q := s.db.Model(&memory.Jargon{})
	if f.GroupID > 0 {
		q = q.Where("group_id = ?", f.GroupID)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if k := strings.TrimSpace(f.Keyword); k != "" {
		p := "%" + k + "%"
		q = q.Where("term ILIKE ? OR meaning ILIKE ?", p, p)
	}
	q = order(q, f.Sort, f.Order, map[string]string{"updated": "updated_at", "created": "created_at", "group": "group_id", "default": "updated_at"})
	page, err := paginate(q, f.Page, f.PageSize, &items)
	if err != nil {
		return Page[JargonView]{}, err
	}
	evidence, err := s.jargonEvidence(items)
	if err != nil {
		return Page[JargonView]{}, err
	}
	views := make([]JargonView, 0, len(items))
	for _, item := range items {
		views = append(views, JargonView{Jargon: item, Evidence: evidence[item.ID]})
	}
	return Page[JargonView]{Items: views, Total: page.Total, Page: page.Page, PageSize: page.PageSize}, nil
}

func (s *AdminService) GetJargon(id uint) (memory.Jargon, error) {
	var v memory.Jargon
	return v, s.db.First(&v, id).Error
}

func (s *AdminService) stylePatternEvidence(items []memory.StylePattern) (map[uint][]memory.MessageLog, error) {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []struct {
		StylePatternID uint
		memory.MessageLog
	}
	err := s.db.Table("style_pattern_evidence e").Select("e.style_pattern_id, ml.*").
		Joins("JOIN message_logs ml ON ml.id = e.message_log_id").Where("e.style_pattern_id IN ?", ids).
		Order("e.style_pattern_id, ml.message_time, ml.id").Scan(&rows).Error
	result := make(map[uint][]memory.MessageLog, len(ids))
	for _, row := range rows {
		result[row.StylePatternID] = append(result[row.StylePatternID], row.MessageLog)
	}
	return result, err
}

func (s *AdminService) jargonEvidence(items []memory.Jargon) (map[uint][]memory.MessageLog, error) {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []struct {
		JargonID uint
		memory.MessageLog
	}
	err := s.db.Table("jargon_evidence e").Select("e.jargon_id, ml.*").
		Joins("JOIN message_logs ml ON ml.id = e.message_log_id").Where("e.jargon_id IN ?", ids).
		Order("e.jargon_id, ml.message_time, ml.id").Scan(&rows).Error
	result := make(map[uint][]memory.MessageLog, len(ids))
	for _, row := range rows {
		result[row.JargonID] = append(result[row.JargonID], row.MessageLog)
	}
	return result, err
}

func (s *AdminService) ListMemories(f MemoryFilter) (Page[MemoryView], error) {
	var items []memory.Memory
	q := s.db.Model(&memory.Memory{})
	if f.GroupID > 0 {
		q = q.Where("group_id = ?", f.GroupID)
	}
	if f.Subject != "" {
		selfID := int64(0)
		if s.selfID != nil {
			selfID = s.selfID()
		}
		switch f.Subject {
		case "group":
			q = q.Where("subject_user_id=0")
		case "self":
			if selfID <= 0 {
				q = q.Where("1=0")
			} else {
				q = q.Where("subject_user_id=?", selfID)
			}
		case "member":
			q = q.Where("subject_user_id<>0")
			if selfID > 0 {
				q = q.Where("subject_user_id<>?", selfID)
			}
		}
	}
	if f.Kind != "" {
		q = q.Where("kind = ?", f.Kind)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if k := strings.TrimSpace(f.Keyword); k != "" {
		q = q.Where("content ILIKE ?", "%"+k+"%")
	}
	q = order(q, f.Sort, f.Order, map[string]string{"updated": "updated_at", "created": "created_at", "default": "updated_at"})
	page, err := paginate(q, f.Page, f.PageSize, &items)
	if err != nil {
		return Page[MemoryView]{}, err
	}
	views := make([]MemoryView, 0, len(items))
	for _, item := range items {
		evidence, evidenceErr := s.memory.ListMemoryEvidence(context.Background(), item.ID)
		if evidenceErr != nil {
			return Page[MemoryView]{}, evidenceErr
		}
		views = append(views, MemoryView{Memory: item, Evidence: evidence})
	}
	return Page[MemoryView]{Items: views, Total: page.Total, Page: page.Page, PageSize: page.PageSize}, nil
}

func (s *AdminService) GetMemory(id uint) (memory.Memory, error) {
	var v memory.Memory
	return v, s.db.First(&v, id).Error
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
	if f.Sort == "group" {
		base = base.Order("group_id " + strings.ToUpper(f.Order))
	} else if f.Sort == "recent" {
		direction := strings.ToUpper(f.Order)
		base = base.Order("(SELECT MAX(ml.message_time) FROM topic_assignments ta JOIN message_logs ml ON ml.id = ta.message_log_id WHERE ta.topic_id = topic_threads.id) " + direction + " NULLS LAST").
			Order("(SELECT MAX(ml.id) FROM topic_assignments ta JOIN message_logs ml ON ml.id = ta.message_log_id WHERE ta.topic_id = topic_threads.id) " + direction + " NULLS LAST")
	} else {
		base = base.Order("id " + strings.ToUpper(f.Order))
	}
	var threads []memory.TopicThread
	if err := base.Offset((page - 1) * size).Limit(size).Find(&threads).Error; err != nil {
		return Page[TopicThreadView]{}, err
	}
	items := make([]TopicThreadView, 0, len(threads))
	for _, thread := range threads {
		view, err := s.loadTopicThread(thread)
		if err != nil {
			return Page[TopicThreadView]{}, err
		}
		items = append(items, view)
	}
	return Page[TopicThreadView]{Items: items, Total: total, Page: page, PageSize: size}, nil
}

func (s *AdminService) loadTopicThread(thread memory.TopicThread) (TopicThreadView, error) {
	view := TopicThreadView{ID: thread.ID, GroupID: thread.GroupID, CreatedAt: thread.CreatedAt, UpdatedAt: thread.CreatedAt}
	if err := s.db.Model(&memory.TopicAssignment{}).Where("topic_id = ?", thread.ID).Select("COALESCE(MAX(id), 0)").Scan(&view.LastAssignmentID).Error; err != nil {
		return view, err
	}
	err := s.db.Table("topic_summaries ts").Select("ts.*").Joins("JOIN topic_assignments ta ON ta.id = ts.through_topic_assignment_id").Where("ta.topic_id = ?", thread.ID).Order("ts.through_topic_assignment_id ASC").Scan(&view.Summaries).Error
	if err == nil && len(view.Summaries) > 0 {
		view.UpdatedAt = view.Summaries[len(view.Summaries)-1].CreatedAt
	}
	return view, err
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
		q = q.Where("nickname ILIKE ? OR CAST(user_id AS TEXT) ILIKE ? OR EXISTS (SELECT 1 FROM member_traits mt WHERE mt.user_id=member_profiles.user_id AND mt.value ILIKE ?)", p, p, p)
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
	items := make([]MemberProfileView, 0, len(profiles))
	for _, p := range profiles {
		v := MemberProfileView{MemberProfile: p}
		if err := s.db.Where("user_id = ?", p.UserID).Order("updated_at DESC").Find(&v.Names).Error; err != nil {
			return Page[MemberProfileView]{}, err
		}
		if err := s.db.Where("user_id = ?", p.UserID).Order("updated_at DESC").Find(&v.Traits).Error; err != nil {
			return Page[MemberProfileView]{}, err
		}
		items = append(items, v)
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

func (s *AdminService) validateStatus(raw string, allowed ...string) error {
	for _, v := range allowed {
		if raw == v {
			return nil
		}
	}
	return fmt.Errorf("invalid status: %s", raw)
}
