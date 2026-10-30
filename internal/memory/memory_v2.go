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
	ExcludeKeywords []string
	SubjectType     string
	SubjectTarget   string
	MemoryType      string
	Status          string
	Cursor          string
	Limit           int
}

type MemorySearchFilter struct {
	Keywords        []string `json:"keywords"`
	Match           string   `json:"match"`
	ExcludeKeywords []string `json:"exclude_keywords"`
	SubjectType     string   `json:"subject_type"`
	SubjectTarget   string   `json:"subject_target"`
	MemoryType      string   `json:"memory_type"`
	Status          string   `json:"status"`
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
	NextCursor string      `json:"next_cursor,omitempty"`
	HasMore    bool        `json:"has_more"`
}

type MemoryDetail struct {
	Item      KnowledgeRecord     `json:"item"`
	Revisions []KnowledgeRevision `json:"revisions"`
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
		ExcludeKeywords: NormalizeKeywords(query.ExcludeKeywords), SubjectType: strings.ToLower(strings.TrimSpace(query.SubjectType)),
		SubjectTarget: strings.TrimSpace(query.SubjectTarget), MemoryType: strings.ToLower(strings.TrimSpace(query.MemoryType)),
		Status: strings.ToLower(strings.TrimSpace(query.Status)),
	}
}

func memorySearchTextExpr(alias string) string {
	return "lower(" + alias + ".content || ' ' || " + alias + ".label || ' ' || " + alias + ".subject_target)"
}

func memoryKeywordCondition(textExpr, keyword string) (string, []any) {
	pattern := "%" + strings.ToLower(keyword) + "%"
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

func (m *Manager) SearchMemories(ctx context.Context, query MemorySearchQuery) (MemoryPage, error) {
	match, err := NormalizeMatch(query.Match)
	if err != nil {
		return MemoryPage{}, err
	}
	filter := searchFilter(query)
	filter.Match = match
	limit := NormalizeLimit(query.Limit)
	status := filter.Status
	if status == "" {
		status = "active"
	}
	filter.Status = status
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

	q := m.db.WithContext(ctx).Table("knowledge_items ki").Where("ki.updated_at<=?", snapshot)
	if status != "all" {
		q = q.Where("ki.status=?", status)
	}
	if filter.SubjectType != "" {
		q = q.Where("ki.subject_type=?", filter.SubjectType)
	}
	if filter.SubjectTarget != "" {
		q = q.Where("ki.subject_target=?", filter.SubjectTarget)
	}
	if filter.MemoryType != "" {
		q = q.Where("ki.memory_type=?", filter.MemoryType)
	}

	keywords := NormalizeKeywords(query.Keywords)
	excludes := NormalizeKeywords(query.ExcludeKeywords)
	textExpr := memorySearchTextExpr("ki")
	scoreExpr := ""
	scoreArgs := []any{}
	if len(keywords) > 0 {
		parts := make([]string, 0, len(keywords))
		args := make([]any, 0, len(keywords)*2)
		scoreParts := make([]string, 0, len(keywords))
		for _, keyword := range keywords {
			condition, conditionArgs := memoryKeywordCondition(textExpr, keyword)
			parts = append(parts, condition)
			args = append(args, conditionArgs...)
			scoreParts = append(scoreParts, memoryKeywordScore(textExpr, keyword))
			scoreArgs = append(scoreArgs, strings.ToLower(keyword))
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
	page := MemoryPage{Items: items, HasMore: hasMore}
	if len(items) > 0 {
		last := items[len(items)-1]
		page.NextCursor = NewPageCursor("similarity", last.Similarity, last.UpdatedAt, last.ID, snapshot, filter)
	}
	return page, nil
}

func similarityScore(text, keyword string) float64 {
	text = strings.ToLower(text)
	keyword = strings.ToLower(keyword)
	if keyword == "" {
		return 0
	}
	if strings.Contains(text, keyword) {
		return 1
	}
	return 0
}

func (m *Manager) ReadMemory(ctx context.Context, id uint) (*MemoryDetail, error) {
	if id == 0 {
		return nil, fmt.Errorf("记忆编号不能为空")
	}
	var item KnowledgeRecord
	if err := m.db.WithContext(ctx).Where("id=?", id).Take(&item).Error; err != nil {
		return nil, err
	}
	var revisions []KnowledgeRevision
	if err := m.db.WithContext(ctx).Where("item_id=?", id).Order("version DESC,id DESC").Limit(20).Find(&revisions).Error; err != nil {
		return nil, err
	}
	return &MemoryDetail{Item: item, Revisions: revisions}, nil
}

func (m *Manager) ManageMemories(ctx context.Context, changes []MemoryChange) ([]MemoryChangeResult, error) {
	if len(changes) == 0 || len(changes) > 8 {
		return nil, fmt.Errorf("一次记忆变更必须为 1 到 8 项")
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
	case "update", "archive", "restore":
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
	var existing KnowledgeRecord
	err = tx.Where("subject_type=? AND subject_target=? AND memory_type=? AND content_hash=?", subjectType, subjectTarget, memoryType, hash).Order("id").Take(&existing).Error
	if err == nil {
		return MemoryChangeResult{ItemID: existing.ID, Action: "create", Version: existing.Version, Status: existing.Status}, nil
	}
	if err != gorm.ErrRecordNotFound {
		return MemoryChangeResult{}, err
	}
	item := KnowledgeRecord{
		SubjectType: subjectType, SubjectTarget: subjectTarget, MemoryType: memoryType,
		Label: strings.TrimSpace(change.Label), Content: content, Pinned: change.Pinned != nil && *change.Pinned,
		Status: "active", Version: 1, ContentHash: hash,
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
	switch change.Action {
	case "update":
		if content := strings.TrimSpace(change.Content); content != "" {
			item.Content = content
		}
		if label := strings.TrimSpace(change.Label); label != "" {
			item.Label = label
		}
		item.Pinned = pinnedAfterUpdate(item.Pinned, change.Pinned)
	case "archive":
		item.Status = "archived"
	case "restore":
		item.Status = "active"
	}
	content := strings.TrimSpace(item.Content)
	if content == "" || len([]rune(content)) > 8000 {
		return MemoryChangeResult{}, fmt.Errorf("记忆正文不能为空或超过 8000 字符")
	}
	item.Content = content
	item.ContentHash = memoryContentHash(item.SubjectType, item.SubjectTarget, item.MemoryType, item.Content)
	if change.Action == "update" || change.Action == "restore" {
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
