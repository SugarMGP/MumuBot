package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
)

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
