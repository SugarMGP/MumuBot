package services

import (
	"context"
	"errors"
	"fmt"
	"mumu-bot/internal/memory"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Page[T any] struct {
	Items    []T
	Total    int64
	Page     int
	PageSize int
}

type StyleCardFilter struct {
	GroupID  int64
	Status   string
	Keyword  string
	Sort     string
	Order    string
	Page     int
	PageSize int
}

type JargonFilter struct {
	GroupID  int64
	Status   string
	Keyword  string
	Sort     string
	Order    string
	Page     int
	PageSize int
}

type MemoryFilter struct {
	GroupID       int64
	Type          string
	Status        string
	CanonicalType string
	SourceKind    string
	Keyword       string
	Sort          string
	Order         string
	Page          int
	PageSize      int
}

type TopicFilter struct {
	GroupID  int64
	Status   string
	Keyword  string
	Sort     string
	Order    string
	Page     int
	PageSize int
}

type StickerFilter struct {
	Keyword  string
	Sort     string
	Order    string
	Page     int
	PageSize int
}

type MemberFilter struct {
	Keyword  string
	Sort     string
	Order    string
	Page     int
	PageSize int
}

type AdminService struct {
	db         *gorm.DB
	stickerDir string
	memDeleter interface {
		DeleteMemory(ctx context.Context, id uint) error
		ArchiveMemory(ctx context.Context, id uint) error
		RestoreMemoryToCandidate(ctx context.Context, id uint) error
	}
	jargonReloader interface {
		ReloadJargons()
	}
}

type OverviewStats struct {
	MemoryCount    int64
	MemberCount    int64
	JargonCount    int64
	StyleCardCount int64
	StickerCount   int64
}

func NewAdminService(db *gorm.DB, stickerDir string) *AdminService {
	return &AdminService{
		db:         db,
		stickerDir: stickerDir,
	}
}

func (s *AdminService) DB() *gorm.DB {
	return s.db
}

func (s *AdminService) StickerDir() string {
	return s.stickerDir
}

func (s *AdminService) WithMemoryDeleter(deleter interface {
	DeleteMemory(ctx context.Context, id uint) error
	ArchiveMemory(ctx context.Context, id uint) error
	RestoreMemoryToCandidate(ctx context.Context, id uint) error
},
) *AdminService {
	s.memDeleter = deleter
	return s
}

func (s *AdminService) WithJargonReloader(reloader interface {
	ReloadJargons()
},
) *AdminService {
	s.jargonReloader = reloader
	return s
}

func normalizePage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

// normalizeSort 只返回白名单内的页面排序键，SQL 列名仍由 apply*Sort 中的固定映射决定。
func normalizeSort(rawSort, rawOrder, defaultSort string, allowed map[string]struct{}) (string, string) {
	sortKey := strings.TrimSpace(strings.ToLower(rawSort))
	if _, ok := allowed[sortKey]; !ok {
		sortKey = defaultSort
	}

	order := strings.TrimSpace(strings.ToLower(rawOrder))
	if order != "asc" && order != "desc" {
		order = "desc"
	}

	return sortKey, order
}

func sortDirection(order string) string {
	if strings.EqualFold(strings.TrimSpace(order), "asc") {
		return "ASC"
	}
	return "DESC"
}

func orderClause(column string, order string) string {
	return fmt.Sprintf("%s %s", column, sortDirection(order))
}

func applyOrder(q *gorm.DB, clauses ...string) *gorm.DB {
	for _, clause := range clauses {
		if strings.TrimSpace(clause) == "" {
			continue
		}
		q = q.Order(clause)
	}
	return q
}

func NormalizeStyleCardSort(rawSort, rawOrder string) (string, string) {
	return normalizeSort(rawSort, rawOrder, "updated", map[string]struct{}{
		"updated":  {},
		"created":  {},
		"use":      {},
		"evidence": {},
	})
}

func NormalizeJargonSort(rawSort, rawOrder string) (string, string) {
	return normalizeSort(rawSort, rawOrder, "updated", map[string]struct{}{
		"updated": {},
		"created": {},
		"group":   {},
	})
}

func NormalizeMemorySort(rawSort, rawOrder string) (string, string) {
	return normalizeSort(rawSort, rawOrder, "updated", map[string]struct{}{
		"updated":    {},
		"created":    {},
		"access":     {},
		"importance": {},
		"evidence":   {},
	})
}

func NormalizeTopicSort(rawSort, rawOrder string) (string, string) {
	return normalizeSort(rawSort, rawOrder, "recent", map[string]struct{}{
		"recent":  {},
		"updated": {},
		"created": {},
		"group":   {},
	})
}

func NormalizeStickerSort(rawSort, rawOrder string) (string, string) {
	return normalizeSort(rawSort, rawOrder, "use", map[string]struct{}{
		"use":     {},
		"updated": {},
		"created": {},
	})
}

func NormalizeMemberSort(rawSort, rawOrder string) (string, string) {
	return normalizeSort(rawSort, rawOrder, "messages", map[string]struct{}{
		"messages": {},
		"activity": {},
		"intimacy": {},
		"recent":   {},
		"updated":  {},
	})
}

func applyStyleCardSort(q *gorm.DB, rawSort, rawOrder string) *gorm.DB {
	sortKey, order := NormalizeStyleCardSort(rawSort, rawOrder)
	switch sortKey {
	case "created":
		return applyOrder(q, orderClause("created_at", order), orderClause("id", order))
	case "use":
		return applyOrder(q, orderClause("use_count", order), "updated_at DESC", "id DESC")
	case "evidence":
		return applyOrder(q, orderClause("evidence_count", order), "updated_at DESC", "id DESC")
	default:
		return applyOrder(q, orderClause("updated_at", order), orderClause("id", order))
	}
}

func applyJargonSort(q *gorm.DB, rawSort, rawOrder string) *gorm.DB {
	sortKey, order := NormalizeJargonSort(rawSort, rawOrder)
	switch sortKey {
	case "created":
		return applyOrder(q, orderClause("created_at", order), orderClause("id", order))
	case "group":
		return applyOrder(q, orderClause("group_id", order), "updated_at DESC", "id DESC")
	default:
		return applyOrder(q, orderClause("updated_at", order), orderClause("id", order))
	}
}

func applyMemorySort(q *gorm.DB, rawSort, rawOrder string) *gorm.DB {
	sortKey, order := NormalizeMemorySort(rawSort, rawOrder)
	switch sortKey {
	case "created":
		return applyOrder(q, orderClause("created_at", order), orderClause("id", order))
	case "access":
		return applyOrder(q, orderClause("access_count", order), "updated_at DESC", "id DESC")
	case "importance":
		return applyOrder(q, orderClause("importance", order), "updated_at DESC", "id DESC")
	case "evidence":
		return applyOrder(q, orderClause("evidence_count", order), "updated_at DESC", "id DESC")
	default:
		return applyOrder(q, orderClause("updated_at", order), orderClause("id", order))
	}
}

func applyStickerSort(q *gorm.DB, rawSort, rawOrder string) *gorm.DB {
	sortKey, order := NormalizeStickerSort(rawSort, rawOrder)
	switch sortKey {
	case "updated":
		return applyOrder(q, orderClause("updated_at", order), orderClause("id", order))
	case "created":
		return applyOrder(q, orderClause("created_at", order), orderClause("id", order))
	default:
		return applyOrder(q, orderClause("use_count", order), "updated_at DESC", "id DESC")
	}
}

func applyTopicSort(q *gorm.DB, rawSort, rawOrder string) *gorm.DB {
	sortKey, order := NormalizeTopicSort(rawSort, rawOrder)
	switch sortKey {
	case "updated":
		return applyOrder(q, orderClause("updated_at", order), orderClause("id", order))
	case "created":
		return applyOrder(q, orderClause("created_at", order), orderClause("id", order))
	case "group":
		return applyOrder(q, orderClause("group_id", order), "last_message_log_id DESC", "id DESC")
	default:
		return applyOrder(q, orderClause("last_message_log_id", order), "updated_at DESC", "id DESC")
	}
}

func applyMemberSort(q *gorm.DB, rawSort, rawOrder string) *gorm.DB {
	sortKey, order := NormalizeMemberSort(rawSort, rawOrder)
	switch sortKey {
	case "activity":
		return applyOrder(q, orderClause("activity", order), "updated_at DESC", "id DESC")
	case "intimacy":
		return applyOrder(q, orderClause("intimacy", order), "updated_at DESC", "id DESC")
	case "recent":
		return applyOrder(q, orderClause("last_speak", order), "updated_at DESC", "id DESC")
	case "updated":
		return applyOrder(q, orderClause("updated_at", order), orderClause("id", order))
	default:
		return applyOrder(q, orderClause("msg_count", order), "updated_at DESC", "id DESC")
	}
}

func (s *AdminService) OverviewStats() (OverviewStats, error) {
	var stats OverviewStats

	counts := []struct {
		model any
		dest  *int64
	}{
		{model: &memory.Memory{}, dest: &stats.MemoryCount},
		{model: &memory.MemberProfile{}, dest: &stats.MemberCount},
		{model: &memory.Jargon{}, dest: &stats.JargonCount},
		{model: &memory.StyleCard{}, dest: &stats.StyleCardCount},
		{model: &memory.Sticker{}, dest: &stats.StickerCount},
	}

	for _, item := range counts {
		if err := s.db.Model(item.model).Count(item.dest).Error; err != nil {
			return stats, err
		}
	}

	return stats, nil
}

func (s *AdminService) ListStyleCards(filter StyleCardFilter) (Page[memory.StyleCard], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	result := Page[memory.StyleCard]{Page: page, PageSize: pageSize}

	q := s.db.Model(&memory.StyleCard{})
	if filter.GroupID > 0 {
		q = q.Where("group_id = ?", filter.GroupID)
	}
	if strings.TrimSpace(filter.Status) != "" {
		q = q.Where("status = ?", strings.TrimSpace(filter.Status))
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		q = q.Where("intent LIKE ? OR tone LIKE ? OR trigger_rule LIKE ? OR avoid_rule LIKE ? OR example LIKE ? OR source_excerpt LIKE ?",
			pattern, pattern, pattern, pattern, pattern, pattern)
	}

	if err := q.Count(&result.Total).Error; err != nil {
		return result, err
	}
	q = applyStyleCardSort(q, filter.Sort, filter.Order)
	if err := q.Offset((page - 1) * pageSize).Limit(pageSize).Find(&result.Items).Error; err != nil {
		return result, err
	}
	return result, nil
}

func (s *AdminService) GetStyleCard(id uint) (memory.StyleCard, error) {
	var item memory.StyleCard
	err := s.db.First(&item, id).Error
	return item, err
}

func (s *AdminService) ListJargons(filter JargonFilter) (Page[memory.Jargon], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	result := Page[memory.Jargon]{Page: page, PageSize: pageSize}

	q := s.db.Model(&memory.Jargon{})
	if filter.GroupID > 0 {
		q = q.Where("group_id = ?", filter.GroupID)
	}
	switch strings.TrimSpace(filter.Status) {
	case "pending":
		q = q.Where("checked = ? AND rejected = ?", false, false)
	case "approved":
		q = q.Where("checked = ? AND rejected = ?", true, false)
	case "rejected":
		q = q.Where("checked = ? AND rejected = ?", true, true)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		q = q.Where("content LIKE ? OR meaning LIKE ? OR context LIKE ?", pattern, pattern, pattern)
	}

	if err := q.Count(&result.Total).Error; err != nil {
		return result, err
	}
	q = applyJargonSort(q, filter.Sort, filter.Order)
	if err := q.Offset((page - 1) * pageSize).Limit(pageSize).Find(&result.Items).Error; err != nil {
		return result, err
	}
	return result, nil
}

func (s *AdminService) GetJargon(id uint) (memory.Jargon, error) {
	var item memory.Jargon
	err := s.db.First(&item, id).Error
	return item, err
}

func (s *AdminService) ListMemories(filter MemoryFilter) (Page[memory.Memory], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	result := Page[memory.Memory]{Page: page, PageSize: pageSize}

	q := s.db.Model(&memory.Memory{})
	if filter.GroupID > 0 {
		q = q.Where("group_id = ?", filter.GroupID)
	}
	if strings.TrimSpace(filter.Type) != "" {
		q = q.Where("type = ?", strings.TrimSpace(filter.Type))
	}
	if strings.TrimSpace(filter.CanonicalType) != "" {
		q = q.Where("canonical_type = ?", strings.TrimSpace(filter.CanonicalType))
	}
	if clause, args := memoryStatusFilterClause(filter.Status); clause != "" {
		q = q.Where(clause, args...)
	}
	if clause, args := memorySourceFilterClause(filter.SourceKind); clause != "" {
		q = q.Where(clause, args...)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		q = q.Where("content LIKE ? OR source_ref LIKE ? OR fact_key LIKE ?", pattern, pattern, pattern)
	}

	if err := q.Count(&result.Total).Error; err != nil {
		return result, err
	}
	q = applyMemorySort(q, filter.Sort, filter.Order)
	if err := q.Offset((page - 1) * pageSize).Limit(pageSize).Find(&result.Items).Error; err != nil {
		return result, err
	}
	return result, nil
}

func memoryStatusFilterClause(raw string) (string, []any) {
	switch strings.TrimSpace(raw) {
	case "":
		return "", nil
	case string(memory.MemoryStatusLegacy):
		return "(status = ? OR status = '' OR status IS NULL)", []any{memory.MemoryStatusLegacy}
	case string(memory.MemoryStatusActive), string(memory.MemoryStatusCandidate), string(memory.MemoryStatusArchived):
		return "status = ?", []any{strings.TrimSpace(raw)}
	default:
		return "", nil
	}
}

func memorySourceFilterClause(raw string) (string, []any) {
	switch strings.TrimSpace(raw) {
	case "":
		return "", nil
	case string(memory.MemorySourceKindMessage):
		return "(source_kind = ? OR ((source_kind = '' OR source_kind IS NULL) AND source_ref LIKE ?))", []any{memory.MemorySourceKindMessage, "message:%"}
	case string(memory.MemorySourceKindTopic):
		return "(source_kind = ? OR ((source_kind = '' OR source_kind IS NULL) AND source_ref LIKE ?))", []any{memory.MemorySourceKindTopic, "topic:%"}
	default:
		return "source_kind = ?", []any{strings.TrimSpace(raw)}
	}
}

func (s *AdminService) ListTopicThreads(filter TopicFilter) (Page[memory.TopicThread], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	result := Page[memory.TopicThread]{Page: page, PageSize: pageSize}

	q := s.db.Model(&memory.TopicThread{})
	if filter.GroupID > 0 {
		q = q.Where("group_id = ?", filter.GroupID)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		q = q.Where("status = ?", status)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		q = q.Where("summary_json LIKE ?", pattern)
	}

	if err := q.Count(&result.Total).Error; err != nil {
		return result, err
	}
	q = applyTopicSort(q, filter.Sort, filter.Order)
	if err := q.Offset((page - 1) * pageSize).Limit(pageSize).Find(&result.Items).Error; err != nil {
		return result, err
	}
	return result, nil
}

func (s *AdminService) GetTopicThread(id uint) (memory.TopicThread, error) {
	var item memory.TopicThread
	err := s.db.First(&item, id).Error
	return item, err
}

func (s *AdminService) ListTopicMessages(topicID uint, limit int) ([]memory.MessageLog, error) {
	var items []memory.MessageLog
	q := s.db.Model(&memory.MessageLog{}).
		Where("topic_thread_id = ?", topicID).
		Order("id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&items).Error; err != nil {
		return nil, err
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, nil
}

func (s *AdminService) GetMemory(id uint) (memory.Memory, error) {
	var item memory.Memory
	err := s.db.First(&item, id).Error
	return item, err
}

func (s *AdminService) ListStickers(filter StickerFilter) (Page[memory.Sticker], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	result := Page[memory.Sticker]{Page: page, PageSize: pageSize}

	q := s.db.Model(&memory.Sticker{})
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		q = q.Where("description LIKE ? OR file_name LIKE ? OR file_hash LIKE ?", pattern, pattern, pattern)
	}

	if err := q.Count(&result.Total).Error; err != nil {
		return result, err
	}
	q = applyStickerSort(q, filter.Sort, filter.Order)
	if err := q.Offset((page - 1) * pageSize).Limit(pageSize).Find(&result.Items).Error; err != nil {
		return result, err
	}
	return result, nil
}

func (s *AdminService) GetSticker(id uint) (memory.Sticker, error) {
	var item memory.Sticker
	err := s.db.First(&item, id).Error
	return item, err
}

func (s *AdminService) ListMemberProfiles(filter MemberFilter) (Page[memory.MemberProfile], error) {
	page, pageSize := normalizePage(filter.Page, filter.PageSize)
	result := Page[memory.MemberProfile]{Page: page, PageSize: pageSize}

	q := s.db.Model(&memory.MemberProfile{})
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		q = q.Where(
			"nickname LIKE ? OR name_records LIKE ? OR speak_style LIKE ? OR interests LIKE ? OR common_words LIKE ? OR CAST(user_id AS CHAR) LIKE ?",
			pattern, pattern, pattern, pattern, pattern, pattern,
		)
	}

	if err := q.Count(&result.Total).Error; err != nil {
		return result, err
	}
	q = applyMemberSort(q, filter.Sort, filter.Order)
	if err := q.Offset((page - 1) * pageSize).Limit(pageSize).Find(&result.Items).Error; err != nil {
		return result, err
	}
	return result, nil
}

func validateStyleCardTransition(current memory.StyleCardStatus, target memory.StyleCardStatus) error {
	switch target {
	case memory.StyleCardStatusCandidate, memory.StyleCardStatusActive, memory.StyleCardStatusRejected:
	default:
		return fmt.Errorf("invalid style card status: %s", target)
	}

	if current == target {
		return nil
	}

	switch current {
	case memory.StyleCardStatusCandidate:
		if target == memory.StyleCardStatusActive || target == memory.StyleCardStatusRejected {
			return nil
		}
	case memory.StyleCardStatusActive:
		if target == memory.StyleCardStatusRejected {
			return nil
		}
	case memory.StyleCardStatusRejected:
		if target == memory.StyleCardStatusActive {
			return nil
		}
	}

	return fmt.Errorf("invalid style card transition: %s -> %s", current, target)
}

func jargonStatusValue(current memory.Jargon) string {
	switch {
	case !current.Checked:
		return "pending"
	case current.Rejected:
		return "rejected"
	default:
		return "approved"
	}
}

func validateJargonTransition(current string, target string) error {
	switch target {
	case "pending", "approved", "rejected":
	default:
		return fmt.Errorf("invalid jargon status: %s", target)
	}

	if current == target {
		return nil
	}

	switch current {
	case "pending":
		if target == "approved" || target == "rejected" {
			return nil
		}
	case "approved":
		if target == "rejected" {
			return nil
		}
	case "rejected":
		if target == "approved" {
			return nil
		}
	}

	return fmt.Errorf("invalid jargon transition: %s -> %s", current, target)
}

func (s *AdminService) UpdateStyleCardStatus(id uint, status string) error {
	target := memory.StyleCardStatus(strings.TrimSpace(status))

	var current memory.StyleCard
	if err := s.db.Select("id", "status").First(&current, id).Error; err != nil {
		return err
	}
	if err := validateStyleCardTransition(current.Status, target); err != nil {
		return err
	}

	return s.db.Model(&memory.StyleCard{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     target,
			"updated_at": time.Now(),
		}).Error
}

func (s *AdminService) UpdateJargonStatus(id uint, status string) error {
	status = strings.TrimSpace(status)

	var current memory.Jargon
	if err := s.db.Select("id", "checked", "rejected").First(&current, id).Error; err != nil {
		return err
	}
	if err := validateJargonTransition(jargonStatusValue(current), status); err != nil {
		return err
	}

	updates := map[string]any{}
	switch status {
	case "pending":
		updates["checked"] = false
		updates["rejected"] = false
	case "approved":
		updates["checked"] = true
		updates["rejected"] = false
	case "rejected":
		updates["checked"] = true
		updates["rejected"] = true
	default:
		return fmt.Errorf("invalid jargon status: %s", status)
	}

	if err := s.db.Model(&memory.Jargon{}).
		Where("id = ?", id).
		Updates(updates).Error; err != nil {
		return err
	}

	if s.jargonReloader != nil {
		s.jargonReloader.ReloadJargons()
	}

	return nil
}

func (s *AdminService) DeleteSticker(id uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var sticker memory.Sticker
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&sticker, id).Error; err != nil {
			return err
		}

		filePath, err := s.stickerFilePath(sticker.FileName)
		if err != nil {
			return err
		}
		if filePath != "" {
			if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}

		return tx.Delete(&memory.Sticker{}, id).Error
	})
}

func (s *AdminService) DeleteMemory(id uint) error {
	if s.memDeleter != nil {
		return s.memDeleter.DeleteMemory(context.Background(), id)
	}
	return s.db.Delete(&memory.Memory{}, id).Error
}

func (s *AdminService) ArchiveMemory(id uint) error {
	if s.memDeleter != nil {
		return s.memDeleter.ArchiveMemory(context.Background(), id)
	}
	return s.db.Model(&memory.Memory{}).Where("id = ?", id).Updates(map[string]any{
		"status":     memory.MemoryStatusArchived,
		"updated_at": time.Now(),
	}).Error
}

func (s *AdminService) RestoreMemoryToCandidate(id uint) error {
	if s.memDeleter != nil {
		return s.memDeleter.RestoreMemoryToCandidate(context.Background(), id)
	}
	return s.db.Model(&memory.Memory{}).Where("id = ?", id).Updates(map[string]any{
		"status":     memory.MemoryStatusCandidate,
		"updated_at": time.Now(),
	}).Error
}

func (s *AdminService) stickerFilePath(fileName string) (string, error) {
	baseDir := strings.TrimSpace(s.stickerDir)
	fileName = strings.TrimSpace(fileName)
	if baseDir == "" || fileName == "" {
		return "", nil
	}
	if strings.Contains(fileName, `\`) {
		return "", fmt.Errorf("invalid sticker file path")
	}

	cleanPath := path.Clean("/" + filepath.ToSlash(fileName))
	if cleanPath == "/" || strings.HasPrefix(cleanPath, "/../") || strings.HasSuffix(fileName, "/") {
		return "", fmt.Errorf("invalid sticker file path")
	}

	relativePath := strings.TrimPrefix(cleanPath, "/")
	candidate := filepath.Join(baseDir, filepath.FromSlash(relativePath))

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	absFile, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	if absFile != absBase && !strings.HasPrefix(absFile, absBase+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid sticker file path")
	}

	return absFile, nil
}
