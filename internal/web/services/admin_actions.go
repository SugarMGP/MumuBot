package services

import (
	"context"
	"errors"
	"fmt"
	"mumu-bot/internal/memory"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

func (s *AdminService) UpdateStyleCardStatus(ctx context.Context, id uint, raw string) error {
	status := strings.TrimSpace(raw)
	if err := s.validateStatus(status, string(memory.StylePatternStatusCandidate), string(memory.StylePatternStatusActive), string(memory.StylePatternStatusRejected)); err != nil {
		return err
	}
	return s.memory.UpdateStylePatternStatus(ctx, id, memory.StylePatternStatus(status))
}

func (s *AdminService) UpdateJargonStatus(ctx context.Context, id uint, raw string) error {
	status := strings.TrimSpace(raw)
	if err := s.validateStatus(status, string(memory.CultureStatusCandidate), string(memory.CultureStatusActive), string(memory.CultureStatusRejected)); err != nil {
		return err
	}
	if err := s.memory.UpdateJargonStatus(ctx, id, memory.CultureStatus(status)); err != nil {
		return err
	}
	if s.reloadJargons != nil {
		s.reloadJargons()
	}
	return nil
}

func (s *AdminService) DeleteMemory(ctx context.Context, id uint) error {
	return s.memory.DeleteMemory(ctx, id)
}
func (s *AdminService) ArchiveMemory(ctx context.Context, id uint) error {
	return s.memory.ArchiveMemory(ctx, id)
}
func (s *AdminService) RestoreMemoryToCandidate(ctx context.Context, id uint) error {
	return s.memory.RestoreMemory(ctx, id)
}

func (s *AdminService) DeleteSticker(ctx context.Context, id uint) error {
	item, err := s.memory.DeleteSticker(ctx, id)
	if err != nil {
		return err
	}
	filePath, err := s.stickerFilePath(item.FileName)
	if err != nil {
		zap.L().Warn("表情包记录已删除，但文件路径无效", zap.String("file_name", item.FileName), zap.Error(err))
		return nil
	}
	if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		zap.L().Warn("清理已删除表情包文件失败", zap.String("path", filePath), zap.Error(err))
	}
	return nil
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
