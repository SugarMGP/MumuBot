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

// SearchStickers 搜索表情包
func (m *Manager) SearchStickers(keyword string, limit int) ([]Sticker, error) {
	var stickers []Sticker
	q := m.db.Model(&Sticker{})
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
	err := q.Order("use_count DESC, updated_at DESC").Limit(limit).Find(&stickers).Error
	return stickers, err
}

// UpdateStickerUsage 更新表情包使用记录
func (m *Manager) UpdateStickerUsage(id uint) error {
	return m.db.Model(&Sticker{}).Where("id = ?", id).Updates(map[string]any{
		"use_count": gorm.Expr("use_count + 1"),
	}).Error
}
