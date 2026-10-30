package memory

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// SaveSticker 保存表情包（通过哈希去重）
func (m *Manager) SaveSticker(sticker *Sticker) (bool, error) {
	var existing Sticker
	err := m.db.Where("file_hash = ?", sticker.FileHash).First(&existing).Error
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}

	if err := m.db.Create(sticker).Error; err != nil {
		return false, err
	}
	return false, nil
}

// GetStickerByID 根据ID获取表情包
func (m *Manager) GetStickerByID(id uint) (*Sticker, error) {
	var sticker Sticker
	err := m.db.First(&sticker, id).Error
	if err != nil {
		return nil, err
	}
	return &sticker, nil
}

func (m *Manager) DeleteSticker(ctx context.Context, id uint) (*Sticker, error) {
	var sticker Sticker
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&sticker, id).Error; err != nil {
			return err
		}
		return requireAffected(tx.Delete(&sticker))
	})
	if err != nil {
		return nil, err
	}
	return &sticker, nil
}

// CollectedStickerHashes 查询哪些表情哈希已经在收藏库
func (m *Manager) CollectedStickerHashes(hashes []string) (map[string]bool, error) {
	collected := map[string]bool{}
	if len(hashes) == 0 {
		return collected, nil
	}
	var rows []Sticker
	if err := m.db.Model(&Sticker{}).Where("file_hash IN ?", hashes).Find(&rows).Error; err != nil {
		return collected, err
	}
	for _, row := range rows {
		collected[row.FileHash] = true
	}
	return collected, nil
}

type StickerPage struct {
	Items      []Sticker `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
	HasMore    bool      `json:"has_more"`
}

type StickerFilter struct {
	Keyword string `json:"keyword"`
}

func stickerCursorCondition(cursor PageCursor) (string, []any) {
	useCount := int64(cursor.LastKey)
	return "(use_count < ? OR (use_count = ? AND (updated_at < ? OR (updated_at = ? AND id < ?))))",
		[]any{useCount, useCount, cursor.LastTime, cursor.LastTime, cursor.LastID}
}

func (m *Manager) SearchStickerPage(ctx context.Context, keyword, cursorRaw string, limit int) (StickerPage, error) {
	limit = NormalizeLimit(limit)
	filter := StickerFilter{Keyword: strings.TrimSpace(keyword)}
	cursor, err := DecodeCursor(cursorRaw)
	if err != nil {
		return StickerPage{}, err
	}
	if cursor.FilterHash == "" {
		cursor.FilterHash = CursorFilterHash(filter)
	}
	if err := ValidateCursorFilter(cursor, filter); err != nil {
		return StickerPage{}, err
	}
	snapshot := CursorTimeOrNow(cursor)
	var rows []Sticker
	q := m.db.WithContext(ctx).Model(&Sticker{}).Where("updated_at<=?", snapshot)
	if keyword != "" {
		keywords := strings.Fields(keyword)
		likeConditions := make([]string, 0, len(keywords))
		args := make([]interface{}, 0, len(keywords))
		for _, kw := range keywords {
			likeConditions = append(likeConditions, "description LIKE ?")
			args = append(args, "%"+kw+"%")
		}
		q = q.Where(strings.Join(likeConditions, " OR "), args...)
	}
	if cursor.LastID != 0 {
		condition, args := stickerCursorCondition(cursor)
		q = q.Where(condition, args...)
	}
	if err := q.Order("use_count DESC,updated_at DESC,id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return StickerPage{}, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	page := StickerPage{Items: rows, HasMore: hasMore}
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		page.NextCursor = NewPageCursor("sticker", float64(last.UseCount), last.UpdatedAt, last.ID, snapshot, filter)
	}
	return page, nil
}

// UpdateStickerUsage 更新表情包使用记录
func (m *Manager) UpdateStickerUsage(id uint) error {
	return m.db.Model(&Sticker{}).Where("id = ?", id).Updates(map[string]any{
		"use_count": gorm.Expr("use_count + 1"),
	}).Error
}
