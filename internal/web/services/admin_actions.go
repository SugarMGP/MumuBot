package services

import "context"

func (s *AdminService) DeleteSticker(ctx context.Context, id uint) error {
	return s.memory.DeleteSticker(ctx, id, s.stickerDir)
}
