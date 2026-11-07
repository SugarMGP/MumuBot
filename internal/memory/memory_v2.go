package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MemorySearchQuery struct {
	Keywords        []string
	Match           string
	Mode            string
	ExcludeKeywords []string
	SubjectType     string
	SubjectTarget   string
	MemoryType      string
	UpdatedFrom     *time.Time
	UpdatedTo       *time.Time
	Cursor          string
	Limit           int
}

type MemorySearchFilter struct {
	Keywords        []string   `json:"keywords"`
	Match           string     `json:"match"`
	Mode            string     `json:"mode"`
	ExcludeKeywords []string   `json:"exclude_keywords"`
	SubjectType     string     `json:"subject_type"`
	SubjectTarget   string     `json:"subject_target"`
	MemoryType      string     `json:"memory_type"`
	UpdatedFrom     *time.Time `json:"updated_from,omitempty"`
	UpdatedTo       *time.Time `json:"updated_to,omitempty"`
}

type MemoryHit struct {
	ID             uint      `json:"id"`
	SubjectType    string    `json:"subject_type"`
	SubjectTarget  string    `json:"subject_target"`
	MemoryType     string    `json:"memory_type"`
	Label          string    `json:"label"`
	ContentPreview string    `json:"content_preview"`
	Status         string    `json:"status"`
	Version        uint64    `json:"version"`
	Pinned         bool      `json:"pinned"`
	UpdatedAt      time.Time `json:"updated_at"`
	Similarity     float64   `json:"-"`
}

type MemoryPage struct {
	Items      []MemoryHit `json:"items"`
	Mode       string      `json:"mode"`
	NextCursor string      `json:"next_cursor,omitempty"`
	HasMore    bool        `json:"has_more"`
}

type MemoryChange struct {
	Action          string
	ItemID          uint
	SubjectType     string
	SubjectTarget   string
	MemoryType      string
	Label           string
	Content         string
	Pinned          *bool
	Reason          string
	ExpectedVersion uint64
}

type MemoryChangeResult struct {
	ItemID  uint   `json:"item_id"`
	Action  string `json:"action"`
	Version uint64 `json:"version"`
	Status  string `json:"status"`
}

type memorySearchRow struct {
	KnowledgeRecord
	SearchScore float64 `gorm:"column:search_score"`
}

func NormalizeSubjectTarget(subjectType, target string) (string, error) {
	subjectType = strings.ToLower(strings.TrimSpace(subjectType))
	if subjectType == SubjectSelf {
		return SubjectSelf, nil
	}
	target = strings.Join(strings.Fields(strings.TrimSpace(target)), " ")
	if target == "" {
		return "", fmt.Errorf("subject_target 不能为空")
	}
	switch subjectType {
	case SubjectGroup, SubjectUser:
		id, err := strconv.ParseInt(target, 10, 64)
		if err != nil || id <= 0 {
			return "", fmt.Errorf("群组或用户主体必须填写正整数 ID")
		}
		return strconv.FormatInt(id, 10), nil
	case SubjectObject:
		return target, nil
	default:
		return "", fmt.Errorf("subject_type 无效：%s", subjectType)
	}
}

func ValidateMemoryType(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw != MemoryProfile && raw != MemoryExperience {
		return "", fmt.Errorf("memory_type 只能是 profile 或 experience")
	}
	return raw, nil
}

func memoryContentHash(subjectType, subjectTarget, memoryType, content string) string {
	raw := strings.ToLower(subjectType) + "\n" + strings.ToLower(subjectTarget) + "\n" + strings.ToLower(memoryType) + "\n" + NormalizeContent(content)
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum[:])
}

func searchFilter(query MemorySearchQuery) MemorySearchFilter {
	return MemorySearchFilter{
		Keywords: NormalizeKeywords(query.Keywords), Match: strings.ToLower(strings.TrimSpace(query.Match)),
		Mode:            strings.ToLower(strings.TrimSpace(query.Mode)),
		ExcludeKeywords: NormalizeKeywords(query.ExcludeKeywords), SubjectType: strings.ToLower(strings.TrimSpace(query.SubjectType)),
		SubjectTarget: strings.TrimSpace(query.SubjectTarget), MemoryType: strings.ToLower(strings.TrimSpace(query.MemoryType)),
		UpdatedFrom: query.UpdatedFrom, UpdatedTo: query.UpdatedTo,
	}
}

// NormalizeMemorySearchMode 归一化记忆检索模式：默认精确子串，fuzzy 用 trigram 召回
func NormalizeMemorySearchMode(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return memorySearchExact, nil
	}
	if raw != memorySearchExact && raw != memorySearchFuzzy {
		return "", fmt.Errorf("mode 只能是 exact 或 fuzzy")
	}
	return raw, nil
}

func memorySearchTextExpr(alias string) string {
	return "lower(" + alias + ".content || ' ' || " + alias + ".label || ' ' || " + alias + ".subject_target)"
}

func memoryKeywordCondition(textExpr, keyword string) (string, []any) {
	pattern := LiteralContainsPattern(strings.ToLower(keyword))
	return textExpr + " LIKE ?", []any{pattern}
}

func memoryKeywordScore(textExpr, keyword string) string {
	return "similarity(" + textExpr + ", ?)"
}

func memoryCursorCondition(scoreExpr string, scoreArgs []any, cursor PageCursor) (string, []any) {
	if scoreExpr == "" {
		return "(ki.updated_at < ? OR (ki.updated_at = ? AND ki.id < ?))", []any{cursor.LastTime, cursor.LastTime, cursor.LastID}
	}
	args := make([]any, 0, len(scoreArgs)*2+5)
	args = append(args, scoreArgs...)
	args = append(args, cursor.LastKey)
	args = append(args, scoreArgs...)
	args = append(args, cursor.LastKey, cursor.LastTime, cursor.LastTime, cursor.LastID)
	return "(" + scoreExpr + " < ? OR (" + scoreExpr + " = ? AND (ki.updated_at < ? OR (ki.updated_at = ? AND ki.id < ?))))", args
}

const (
	memorySearchExact            = "exact"
	memorySearchFuzzy            = "fuzzy"
	fuzzySimilarityThreshold     = "0.20"
	fuzzyWordSimilarityThreshold = "0.40"
)

func memoryFuzzyKeywordCondition(textExpr, keyword string) (string, []any) {
	query := strings.ToLower(keyword)
	return "(" + textExpr + " % ? OR ? <% " + textExpr + ")", []any{query, query}
}

func memoryFuzzyKeywordScore(textExpr, keyword string) string {
	return "GREATEST(similarity(" + textExpr + ", ?), word_similarity(?, " + textExpr + "))"
}

func (m *Manager) SearchMemories(ctx context.Context, query MemorySearchQuery) (MemoryPage, error) {
	match, err := NormalizeMatch(query.Match)
	if err != nil {
		return MemoryPage{}, err
	}
	mode, err := NormalizeMemorySearchMode(query.Mode)
	if err != nil {
		return MemoryPage{}, err
	}
	filter := searchFilter(query)
	filter.Match = match
	filter.Mode = mode
	if filter.UpdatedFrom != nil && filter.UpdatedTo != nil && filter.UpdatedFrom.After(*filter.UpdatedTo) {
		return MemoryPage{}, fmt.Errorf("updated_from 不能晚于 updated_to")
	}
	limit := NormalizeLimit(query.Limit)
	cursor, err := DecodeCursor(query.Cursor)
	if err != nil {
		return MemoryPage{}, err
	}
	if cursor.FilterHash == "" {
		cursor.FilterHash = CursorFilterHash(filter)
	}
	if err := ValidateCursorFilter(cursor, filter); err != nil {
		return MemoryPage{}, err
	}
	snapshot := CursorTimeOrNow(cursor)
	keywords := NormalizeKeywords(query.Keywords)
	excludes := NormalizeKeywords(query.ExcludeKeywords)

	db := m.db.WithContext(ctx)
	var tx *gorm.DB
	if mode == memorySearchFuzzy && len(keywords) > 0 {
		tx = db.Begin()
		if tx.Error != nil {
			return MemoryPage{}, tx.Error
		}
		defer func() {
			if tx != nil {
				_ = tx.Rollback().Error
			}
		}()
		if err := tx.Exec("SELECT set_config('pg_trgm.similarity_threshold', ?, true)", fuzzySimilarityThreshold).Error; err != nil {
			return MemoryPage{}, err
		}
		if err := tx.Exec("SELECT set_config('pg_trgm.word_similarity_threshold', ?, true)", fuzzyWordSimilarityThreshold).Error; err != nil {
			return MemoryPage{}, err
		}
		db = tx
	}

	lifecycleNow := time.Now()
	q := db.Table("knowledge_items ki").Where("ki.updated_at<=?", snapshot).
		Where("ki.status='active' AND (ki.pinned OR ki.forget_at IS NULL OR ki.forget_at>?)", lifecycleNow)
	if filter.SubjectType != "" {
		q = q.Where("ki.subject_type=?", filter.SubjectType)
	}
	if filter.SubjectTarget != "" {
		q = q.Where("ki.subject_target=?", filter.SubjectTarget)
	}
	if filter.MemoryType != "" {
		q = q.Where("ki.memory_type=?", filter.MemoryType)
	}
	if filter.UpdatedFrom != nil {
		q = q.Where("ki.updated_at>=?", *filter.UpdatedFrom)
	}
	if filter.UpdatedTo != nil {
		q = q.Where("ki.updated_at<?", *filter.UpdatedTo)
	}

	textExpr := memorySearchTextExpr("ki")
	scoreExpr := ""
	scoreArgs := []any{}
	if len(keywords) > 0 {
		parts := make([]string, 0, len(keywords))
		args := make([]any, 0, len(keywords)*2)
		scoreParts := make([]string, 0, len(keywords))
		for _, keyword := range keywords {
			var condition string
			var conditionArgs []any
			if mode == memorySearchFuzzy {
				condition, conditionArgs = memoryFuzzyKeywordCondition(textExpr, keyword)
				scoreParts = append(scoreParts, memoryFuzzyKeywordScore(textExpr, keyword))
				scoreArgs = append(scoreArgs, strings.ToLower(keyword), strings.ToLower(keyword))
			} else {
				condition, conditionArgs = memoryKeywordCondition(textExpr, keyword)
				scoreParts = append(scoreParts, memoryKeywordScore(textExpr, keyword))
				scoreArgs = append(scoreArgs, strings.ToLower(keyword))
			}
			parts = append(parts, condition)
			args = append(args, conditionArgs...)
		}
		join := " AND "
		if match == "or" {
			join = " OR "
		}
		q = q.Where("("+strings.Join(parts, join)+")", args...)
		if match == "and" {
			scoreExpr = "LEAST(" + strings.Join(scoreParts, ",") + ")"
		} else {
			scoreExpr = "GREATEST(" + strings.Join(scoreParts, ",") + ")"
		}
	}
	for _, keyword := range excludes {
		condition, args := memoryKeywordCondition(textExpr, keyword)
		q = q.Where("NOT ("+condition+")", args...)
	}

	if scoreExpr != "" {
		q = q.Select("ki.*, ("+scoreExpr+") AS search_score", scoreArgs...)
	} else {
		q = q.Select("ki.*")
	}
	if cursor.LastID != 0 {
		condition, args := memoryCursorCondition(scoreExpr, scoreArgs, cursor)
		q = q.Where(condition, args...)
	}
	order := "ki.updated_at DESC,ki.id DESC"
	if scoreExpr != "" {
		order = "search_score DESC,ki.updated_at DESC,ki.id DESC"
	}
	var rows []memorySearchRow
	if err := q.Order(order).Limit(limit + 1).Find(&rows).Error; err != nil {
		return MemoryPage{}, err
	}
	if tx != nil {
		if err := tx.Commit().Error; err != nil {
			return MemoryPage{}, err
		}
		tx = nil
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	items := make([]MemoryHit, 0, len(rows))
	for _, row := range rows {
		items = append(items, MemoryHit{
			ID: row.ID, SubjectType: row.SubjectType, SubjectTarget: row.SubjectTarget, MemoryType: row.MemoryType,
			Label: row.Label, ContentPreview: previewText(row.Content, 300), Status: row.Status, Version: row.Version,
			Pinned: row.Pinned, UpdatedAt: row.UpdatedAt, Similarity: row.SearchScore,
		})
	}
	page := MemoryPage{Items: items, Mode: mode, HasMore: hasMore}
	if len(items) > 0 {
		last := items[len(items)-1]
		page.NextCursor = NewPageCursor("similarity", last.Similarity, last.UpdatedAt, last.ID, snapshot, filter)
	}
	return page, nil
}

func (m *Manager) ReadMemory(ctx context.Context, id uint) (*KnowledgeRecord, error) {
	if id == 0 {
		return nil, fmt.Errorf("记忆编号不能为空")
	}
	now := time.Now()
	var item KnowledgeRecord
	err := m.db.WithContext(ctx).
		Where("id=? AND status='active' AND (pinned OR forget_at IS NULL OR forget_at>?)", id, now).
		Take(&item).Error
	if err == gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("记忆不存在或已归档")
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (m *Manager) ManageMemories(ctx context.Context, changes []MemoryChange) ([]MemoryChangeResult, error) {
	if len(changes) == 0 {
		return nil, fmt.Errorf("一次记忆变更至少需要 1 项")
	}
	results := make([]MemoryChangeResult, 0, len(changes))
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, change := range changes {
			result, err := applyMemoryChange(tx, change)
			if err != nil {
				return err
			}
			results = append(results, result)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func applyMemoryChange(tx *gorm.DB, change MemoryChange) (MemoryChangeResult, error) {
	switch change.Action {
	case "create":
		return createMemory(tx, change)
	case "update", "archive":
		return mutateMemory(tx, change)
	default:
		return MemoryChangeResult{}, fmt.Errorf("未知记忆操作 %s", change.Action)
	}
}

func createMemory(tx *gorm.DB, change MemoryChange) (MemoryChangeResult, error) {
	subjectType := strings.ToLower(strings.TrimSpace(change.SubjectType))
	subjectTarget, err := NormalizeSubjectTarget(subjectType, change.SubjectTarget)
	if err != nil {
		return MemoryChangeResult{}, err
	}
	memoryType, err := ValidateMemoryType(change.MemoryType)
	if err != nil {
		return MemoryChangeResult{}, err
	}
	content := strings.TrimSpace(change.Content)
	if content == "" {
		return MemoryChangeResult{}, fmt.Errorf("记忆正文不能为空")
	}
	if len([]rune(content)) > 8000 {
		return MemoryChangeResult{}, fmt.Errorf("记忆正文不能超过 8000 字符")
	}
	hash := memoryContentHash(subjectType, subjectTarget, memoryType, content)
	now := time.Now()
	if _, err := settleExpiredMemoryHashTx(tx, subjectType, subjectTarget, memoryType, hash, now); err != nil {
		return MemoryChangeResult{}, err
	}
	var existing KnowledgeRecord
	err = tx.Where("subject_type=? AND subject_target=? AND memory_type=? AND content_hash=? AND status='active' AND (pinned OR forget_at IS NULL OR forget_at>?)",
		subjectType, subjectTarget, memoryType, hash, now).Order("id").Take(&existing).Error
	if err == nil {
		return MemoryChangeResult{ItemID: existing.ID, Action: "create", Version: existing.Version, Status: existing.Status}, nil
	}
	if err != gorm.ErrRecordNotFound {
		return MemoryChangeResult{}, err
	}
	pinned := change.Pinned != nil && *change.Pinned
	policy := CurrentRetentionPolicy()
	stability, anchor, forgetAt := policy.MemoryState(pinned, now)
	item := KnowledgeRecord{
		SubjectType: subjectType, SubjectTarget: subjectTarget, MemoryType: memoryType,
		Label: strings.TrimSpace(change.Label), Content: content, Pinned: pinned,
		Status: "active", Version: 1, ContentHash: hash,
		StabilityDays: stability, RecallAnchorAt: anchor, ForgetAt: forgetAt,
	}
	if err := tx.Create(&item).Error; err != nil {
		return MemoryChangeResult{}, err
	}
	if err := tx.Create(&KnowledgeRevision{
		ItemID: item.ID, Version: item.Version, AfterContent: item.Content, AfterStatus: item.Status,
		AfterPinned: item.Pinned, Reason: change.Reason,
	}).Error; err != nil {
		return MemoryChangeResult{}, err
	}
	return MemoryChangeResult{ItemID: item.ID, Action: "create", Version: item.Version, Status: item.Status}, nil
}

func pinnedAfterUpdate(current bool, value *bool) bool {
	if value == nil {
		return current
	}
	return *value
}

func mutateMemory(tx *gorm.DB, change MemoryChange) (MemoryChangeResult, error) {
	if change.ItemID == 0 {
		return MemoryChangeResult{}, fmt.Errorf("记忆编号不能为空")
	}
	if change.ExpectedVersion == 0 {
		return MemoryChangeResult{}, fmt.Errorf("修改记忆前必须读取并提交当前版本")
	}
	var item KnowledgeRecord
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", change.ItemID).Take(&item).Error; err != nil {
		return MemoryChangeResult{}, err
	}
	if item.Version != change.ExpectedVersion {
		return MemoryChangeResult{}, fmt.Errorf("记忆 %d 已被其他请求修改，请重新读取后再试", item.ID)
	}
	before := item
	item.Version++
	if item.Status != "active" {
		return MemoryChangeResult{}, fmt.Errorf("记忆已归档，不能再修改")
	}
	if change.Action == "update" && !item.Pinned && item.ForgetAt != nil && !time.Now().Before(*item.ForgetAt) {
		return MemoryChangeResult{}, fmt.Errorf("记忆已到期，不能再修改")
	}
	switch change.Action {
	case "update":
		if content := strings.TrimSpace(change.Content); content != "" {
			item.Content = content
		}
		if label := strings.TrimSpace(change.Label); label != "" {
			item.Label = label
		}
		beforePinned := item.Pinned
		item.Pinned = pinnedAfterUpdate(item.Pinned, change.Pinned)
		if beforePinned && !item.Pinned {
			policy := CurrentRetentionPolicy()
			halfLife := item.StabilityDays
			if halfLife <= 0 {
				halfLife = policy.InitialHalfLifeDays
			}
			now := time.Now()
			item.StabilityDays = halfLife
			item.RecallAnchorAt = now
			forgetAt := policy.ForgetAt(now, halfLife)
			item.ForgetAt = &forgetAt
		} else if !beforePinned && item.Pinned {
			item.ForgetAt = nil
		}
	case "archive":
		item.Status = "archived"
	}
	content := strings.TrimSpace(item.Content)
	if content == "" || len([]rune(content)) > 8000 {
		return MemoryChangeResult{}, fmt.Errorf("记忆正文不能为空或超过 8000 字符")
	}
	item.Content = content
	item.ContentHash = memoryContentHash(item.SubjectType, item.SubjectTarget, item.MemoryType, item.Content)
	if change.Action == "update" {
		var duplicate int64
		if err := tx.Model(&KnowledgeRecord{}).Where("subject_type=? AND subject_target=? AND memory_type=? AND content_hash=? AND status='active' AND id<>?", item.SubjectType, item.SubjectTarget, item.MemoryType, item.ContentHash, item.ID).Count(&duplicate).Error; err != nil {
			return MemoryChangeResult{}, err
		}
		if duplicate > 0 {
			return MemoryChangeResult{}, fmt.Errorf("已有相同内容的记忆，请读取现有条目后再合并")
		}
	}
	if err := tx.Save(&item).Error; err != nil {
		return MemoryChangeResult{}, err
	}
	if err := tx.Create(&KnowledgeRevision{
		ItemID: item.ID, Version: item.Version, BeforeContent: before.Content, AfterContent: item.Content,
		BeforeStatus: before.Status, AfterStatus: item.Status, BeforePinned: before.Pinned,
		AfterPinned: item.Pinned, Reason: change.Reason,
	}).Error; err != nil {
		return MemoryChangeResult{}, err
	}
	return MemoryChangeResult{ItemID: item.ID, Action: change.Action, Version: item.Version, Status: item.Status}, nil
}
