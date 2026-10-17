package services

import (
	"context"
	"errors"
	"os"

	"go.uber.org/zap"
)

func (s *AdminService) DeleteSticker(ctx context.Context, id uint) error {
	item, err := s.memory.DeleteSticker(ctx, id)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(s.stickerDir)
	if err != nil {
		zap.L().Warn("表情包记录已删除，但目录无法打开", zap.String("file_name", item.FileName), zap.Error(err))
		return nil
	}
	defer root.Close()
	if err := root.Remove(item.FileName); err != nil && !errors.Is(err, os.ErrNotExist) {
		zap.L().Warn("清理已删除表情包文件失败", zap.String("file_name", item.FileName), zap.Error(err))
	}
	return nil
}
