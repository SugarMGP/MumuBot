package agent

import (
	"context"
	"fmt"

	"mumu-bot/internal/filemeta"
	"mumu-bot/internal/refstore"
)

// resolvePendingFileURL 让 worker 通过主服务解析待取附件，不把 QQ 客户端交给 worker 工具
func (a *Agent) resolvePendingFileURL(ctx context.Context, locator refstore.Locator) (string, error) {
	if a == nil || a.bot == nil {
		return "", fmt.Errorf("QQ 文件定位能力未初始化")
	}
	return a.bot.ResolveFileURL(ctx, locator.ConversationKind, locator.TargetID, filemeta.Ref{
		FileID: locator.FileID, Name: locator.Name, Size: locator.Size,
		FileHash: locator.FileHash, BusID: locator.BusID,
	})
}
