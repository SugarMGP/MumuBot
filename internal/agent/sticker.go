package agent

import (
	"context"
	"os"
	"strings"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/utils"

	"go.uber.org/zap"
)

func (a *Agent) autoSaveSticker(ctx context.Context, url string, description string) {
	if url == "" || ctx.Err() != nil {
		return
	}
	description = strings.TrimSpace(description)
	if description == "" {
		zap.L().Debug("跳过自动保存表情包：图片识别失败", zap.String("url", url))
		return
	}

	cfg := config.Get()
	storagePath := cfg.Sticker.StoragePath
	maxSizeMB := cfg.Sticker.MaxSizeMB
	if maxSizeMB <= 0 {
		maxSizeMB = 2
	}

	result, err := utils.DownloadImage(ctx, url, storagePath, maxSizeMB)
	if err != nil {
		zap.L().Debug("下载表情包失败", zap.String("url", url), zap.Error(err))
		return
	}
	if ctx.Err() != nil {
		_ = os.Remove(result.FilePath)
		return
	}

	sticker := &memory.Sticker{FileName: result.FileName, FileHash: result.FileHash, Description: description}
	isDuplicate, err := a.memory.SaveSticker(sticker)
	if err != nil {
		_ = os.Remove(result.FilePath)
		zap.L().Warn("保存表情包失败", zap.Error(err))
		return
	}
	if isDuplicate {
		_ = os.Remove(result.FilePath)
		zap.L().Debug("表情包已存在，跳过保存", zap.String("hash", result.FileHash))
		return
	}
	zap.L().Info("自动保存表情包", zap.Uint("id", sticker.ID), zap.String("desc", description))
}
