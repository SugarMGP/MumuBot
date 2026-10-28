package memory

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MemorySearchQuery struct {
	Keywords         []string
	ConversationKind string
	TargetID         int64
	SubjectUserID    *int64
	Kind             string
	Status           string
	Limit            int
	Offset           int
}

type MemoryHit struct {
	ID               uint      `json:"id"`
	ConversationKind string    `json:"conversation_kind"`
	TargetID         int64     `json:"target_id"`
	SubjectUserID    int64     `json:"subject_user_id"`
	Kind             string    `json:"kind"`
	Label            string    `json:"label"`
	ContentPreview   string    `json:"content_preview"`
	Status           string    `json:"status"`
	Version          uint64    `json:"version"`
	IsCore           bool      `json:"is_core"`
	SourceMeta       string    `json:"source_meta,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
	MatchedKeywords  []string  `json:"matched_keywords,omitempty"`
	LabelHits        int       `json:"-"`
	Similarity       float64   `json:"-"`
}

type MemoryDetail struct {
	Item      KnowledgeRecord     `json:"item"`
	Revisions []KnowledgeRevision `json:"revisions"`
}

type MemoryChange struct {
	Action             string
	ItemID             uint
	SubjectUserID      *int64
	Kind               string
	Label              string
	Content            string
	IsCore             *bool
	Reason             string
	SourceMeta         string
	SourceConversation string
	SourceTargetID     int64
	ExpectedVersion    uint64
}

type MemoryChangeResult struct {
	ItemID  uint   `json:"item_id"`
	Action  string `json:"action"`
	Version uint64 `json:"version"`
	Status  string `json:"status"`
}

var memoryKinds = map[string]bool{
	"fact": true, "preference": true, "constraint": true, "goal": true, "term": true, "expression": true, "alias": true,
}

func normalizeMemoryKind(raw string) (string, error) {
	kind := strings.TrimSpace(raw)
	if !memoryKinds[kind] {
		return "", fmt.Errorf("记忆类型无效：%s", raw)
	}
	return kind, nil
}

func memoryContentHash(subject int64, kind, content string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\n%s\n%s", subject, kind, NormalizeContent(content))))
	return fmt.Sprintf("%x", sum[:])
}

func (m *Manager) SearchMemories(ctx context.Context, query MemorySearchQuery) ([]MemoryHit, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	offset := max(0, query.Offset)
	q := m.db.WithContext(ctx).Table("knowledge_items ki")
	if query.ConversationKind != "" {
		q = q.Where("ki.conversation_kind=?", query.ConversationKind)
	}
	if query.TargetID > 0 {
		q = q.Where("ki.target_id=?", query.TargetID)
	}
	if query.SubjectUserID != nil {
		q = q.Where("ki.subject_user_id=?", *query.SubjectUserID)
	}
	if query.Kind != "" {
		q = q.Where("ki.kind=?", query.Kind)
	}
	status := strings.TrimSpace(query.Status)
	if status == "" {
		status = "active"
	}
	q = q.Where("ki.status=?", status)
	keywords := normalizeKeywords(query.Keywords)
	if len(keywords) > 0 {
		var clauses []string
		var args []any
		for _, keyword := range keywords {
			pattern := "%" + strings.ToLower(keyword) + "%"
			clauses = append(clauses, "(lower(ki.content) LIKE ? OR lower(ki.label) LIKE ? OR ki.content % ? OR ki.label % ?)")
			args = append(args, pattern, pattern, keyword, keyword)
		}
		q = q.Where("("+strings.Join(clauses, " OR ")+")", args...)
	}
	var rows []KnowledgeRecord
	if err := q.Order("ki.updated_at DESC,ki.id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	hits := make([]MemoryHit, 0, len(rows))
	for _, row := range rows {
		matched := matchMemoryKeywords(row, keywords)
		if len(keywords) > 0 && len(matched) == 0 {
			continue
		}
		hits = append(hits, MemoryHit{
			ID: row.ID, ConversationKind: row.ConversationKind, TargetID: row.TargetID,
			SubjectUserID: row.SubjectUserID, Kind: row.Kind, Label: row.Label,
			ContentPreview: previewText(row.Content, 500), Status: row.Status, Version: row.Version,
			IsCore: row.IsCore, SourceMeta: row.SourceMeta, UpdatedAt: row.UpdatedAt, MatchedKeywords: matched,
			LabelHits: labelHitCount(row.Label, keywords), Similarity: keywordSimilarity(matched, keywords),
		})
	}
	sortMemoryHits(hits)
	if offset >= len(hits) {
		return nil, nil
	}
	hits = hits[offset:]
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func matchMemoryKeywords(row KnowledgeRecord, keywords []string) []string {
	if len(keywords) == 0 {
		return nil
	}
	text := strings.ToLower(row.Label + " " + row.Content)
	var matched []string
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword != "" && strings.Contains(text, strings.ToLower(keyword)) {
			matched = append(matched, keyword)
		}
	}
	return matched
}

func labelHitCount(label string, keywords []string) int {
	label = strings.ToLower(label)
	count := 0
	for _, keyword := range keywords {
		if strings.Contains(label, strings.ToLower(keyword)) {
			count++
		}
	}
	return count
}

func sortMemoryHits(hits []MemoryHit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if len(hits[i].MatchedKeywords) != len(hits[j].MatchedKeywords) {
			return len(hits[i].MatchedKeywords) > len(hits[j].MatchedKeywords)
		}
		if hits[i].LabelHits != hits[j].LabelHits {
			return hits[i].LabelHits > hits[j].LabelHits
		}
		if hits[i].Similarity != hits[j].Similarity {
			return hits[i].Similarity > hits[j].Similarity
		}
		if !hits[i].UpdatedAt.Equal(hits[j].UpdatedAt) {
			return hits[i].UpdatedAt.After(hits[j].UpdatedAt)
		}
		return hits[i].ID > hits[j].ID
	})
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

// ManageMemories 批量执行记忆变更，全部成功或全部回滚
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
	if change.SubjectUserID == nil || *change.SubjectUserID < 0 {
		return MemoryChangeResult{}, fmt.Errorf("记忆主体无效")
	}
	content := strings.TrimSpace(change.Content)
	if content == "" {
		return MemoryChangeResult{}, fmt.Errorf("记忆正文不能为空")
	}
	kind, err := normalizeMemoryKind(change.Kind)
	if err != nil {
		return MemoryChangeResult{}, err
	}
	if len([]rune(content)) > 8000 {
		return MemoryChangeResult{}, fmt.Errorf("记忆正文不能超过 8000 字符")
	}
	hash := memoryContentHash(*change.SubjectUserID, kind, content)
	var existing KnowledgeRecord
	err = tx.Where("subject_user_id=? AND kind=? AND content_hash=?", *change.SubjectUserID, kind, hash).Order("id").Take(&existing).Error
	if err == nil {
		return MemoryChangeResult{ItemID: existing.ID, Action: "create", Version: existing.Version, Status: existing.Status}, nil
	}
	if err != gorm.ErrRecordNotFound {
		return MemoryChangeResult{}, err
	}
	item := KnowledgeRecord{
		ConversationKind: change.SourceConversation, TargetID: change.SourceTargetID, SubjectUserID: *change.SubjectUserID,
		Kind: kind, Label: strings.TrimSpace(change.Label), Content: content, Status: "active", Version: 1,
		IsCore: change.IsCore != nil && *change.IsCore, SourceMeta: normalizedSourceMeta(change.SourceMeta), ContentHash: hash,
	}
	if err := tx.Create(&item).Error; err != nil {
		return MemoryChangeResult{}, err
	}
	if err := tx.Create(&KnowledgeRevision{ItemID: item.ID, Version: item.Version, AfterContent: item.Content, AfterStatus: item.Status, AfterCore: item.IsCore, Reason: change.Reason, SourceMeta: item.SourceMeta}).Error; err != nil {
		return MemoryChangeResult{}, err
	}
	return MemoryChangeResult{ItemID: item.ID, Action: "create", Version: item.Version, Status: item.Status}, nil
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
	if change.ExpectedVersion != 0 && item.Version != change.ExpectedVersion {
		return MemoryChangeResult{}, fmt.Errorf("记忆 %d 已被其他请求修改，请重新读取后再试", item.ID)
	}
	before := item
	changeSource := normalizedSourceMeta(change.SourceMeta)
	revisionSource := changeSource
	if strings.TrimSpace(change.SourceMeta) == "" && strings.TrimSpace(item.SourceMeta) != "" {
		revisionSource = item.SourceMeta
	}
	item.Version++
	switch change.Action {
	case "update":
		if content := strings.TrimSpace(change.Content); content != "" {
			item.Content = content
		}
		if strings.TrimSpace(change.Kind) != "" {
			kind, err := normalizeMemoryKind(change.Kind)
			if err != nil {
				return MemoryChangeResult{}, err
			}
			item.Kind = kind
		}
		if label := strings.TrimSpace(change.Label); label != "" {
			item.Label = label
		}
		if change.IsCore != nil {
			item.IsCore = *change.IsCore
		}
		if change.SubjectUserID != nil {
			if *change.SubjectUserID < 0 {
				return MemoryChangeResult{}, fmt.Errorf("记忆主体无效")
			}
			item.SubjectUserID = *change.SubjectUserID
		}
	case "archive":
		item.Status = "archived"
	case "restore":
		item.Status = "active"
	}
	if change.Action == "update" {
		if change.SourceMeta != "" {
			item.SourceMeta = changeSource
		}
		if change.SourceConversation != "" {
			item.ConversationKind = change.SourceConversation
		}
		if change.SourceTargetID > 0 {
			item.TargetID = change.SourceTargetID
		}
	}
	contentText := strings.TrimSpace(item.Content)
	if contentText == "" || len([]rune(contentText)) > 8000 {
		return MemoryChangeResult{}, fmt.Errorf("记忆正文不能为空或超过 8000 字符")
	}
	item.Content = contentText
	item.ContentHash = memoryContentHash(item.SubjectUserID, item.Kind, item.Content)
	if change.Action == "update" {
		var duplicate int64
		if err := tx.Model(&KnowledgeRecord{}).Where("subject_user_id=? AND kind=? AND content_hash=? AND id<>?", item.SubjectUserID, item.Kind, item.ContentHash, item.ID).Count(&duplicate).Error; err != nil {
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
		BeforeStatus: before.Status, AfterStatus: item.Status, BeforeCore: before.IsCore, AfterCore: item.IsCore,
		Reason: change.Reason, SourceMeta: revisionSource,
	}).Error; err != nil {
		return MemoryChangeResult{}, err
	}
	return MemoryChangeResult{ItemID: item.ID, Action: change.Action, Version: item.Version, Status: item.Status}, nil
}

func normalizedSourceMeta(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	if sonic.ValidString(raw) {
		return raw
	}
	encoded, _ := sonic.MarshalString(map[string]string{"source": raw})
	return encoded
}
