package services

import (
	"context"
	"errors"
	"fmt"
	"mumu-bot/internal/memory"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"
)

func (s *AdminService) UpdateStyleCardStatus(id uint, raw string) error {
	status := strings.TrimSpace(raw)
	if err := s.validateStatus(status, string(memory.StylePatternStatusCandidate), string(memory.StylePatternStatusActive), string(memory.StylePatternStatusRejected)); err != nil {
		return err
	}
	return s.db.Model(&memory.StylePattern{}).Where("id = ?", id).Update("status", status).Error
}

func (s *AdminService) UpdateJargonStatus(id uint, raw string) error {
	status := strings.TrimSpace(raw)
	if err := s.validateStatus(status, string(memory.CultureStatusCandidate), string(memory.CultureStatusActive), string(memory.CultureStatusRejected)); err != nil {
		return err
	}
	if err := s.db.Model(&memory.Jargon{}).Where("id = ?", id).Update("status", status).Error; err != nil {
		return err
	}
	if s.reloadJargons != nil {
		s.reloadJargons()
	}
	return nil
}

func (s *AdminService) DeleteMemory(id uint) error {
	return s.memory.DeleteMemory(context.Background(), id)
}
func (s *AdminService) ArchiveMemory(id uint) error {
	return s.memory.ArchiveMemory(context.Background(), id)
}
func (s *AdminService) RestoreMemoryToCandidate(id uint) error {
	return s.memory.RestoreMemoryToCandidate(context.Background(), id)
}

func (s *AdminService) DeleteSticker(id uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var item memory.Sticker
		if err := tx.First(&item, id).Error; err != nil {
			return err
		}
		filePath, err := s.stickerFilePath(item.FileName)
		if err != nil {
			return err
		}
		if err := tx.Delete(&item).Error; err != nil {
			return err
		}
		if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	})
}

func (s *AdminService) stickerFilePath(name string) (string, error) {
	base, err := filepath.Abs(s.stickerDir)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(base, filepath.Base(name)))
	if err != nil {
		return "", err
	}
	if filepath.Dir(target) != base {
		return "", fmt.Errorf("invalid sticker path")
	}
	return target, nil
}
