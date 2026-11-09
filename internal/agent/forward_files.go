package agent

import (
	"context"
	"fmt"
	"os"

	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/refstore"

	"github.com/zjutjh/onebot-sdk/message"
)

func (a *Agent) prepareForwardFile(ctx context.Context, groupID int64, data message.FileData) memory.ForwardFile {
	file := memory.ForwardFile{Ref: onebot.FileInfoFromData(data)}
	if groupID > 0 && file.FileID != "" {
		return file
	}
	if data.URL == "" {
		file.Unavailable = "缺少可确认的文件来源与下载地址"
		return file
	}
	hash, size, err := media.CacheForwardFile(ctx, a.memory.MediaDir(), data.URL, file.Size)
	if err != nil {
		file.Unavailable = err.Error()
		return file
	}
	file.CacheHash = hash
	file.Size = &size
	return file
}

func (a *Agent) readForwardFile(ctx context.Context, groupID int64, file memory.ForwardFile) string {
	name := file.Name
	if name == "" {
		name = "未命名文件"
	}
	text := "[文件:" + name
	if file.Size != nil {
		text += "，" + formatFileSize(*file.Size)
	}
	ref, err := a.forwardFileRef(ctx, groupID, file)
	if err != nil {
		return text + "，不可取：" + err.Error() + "]"
	}
	state := "已固定"
	if ref.State == refstore.StatePending {
		state = "待取"
	}
	return text + "，file_ref=" + ref.FileRef() + "（" + state + "）]"
}

func (a *Agent) forwardFileRef(ctx context.Context, groupID int64, file memory.ForwardFile) (*refstore.Ref, error) {
	store := a.memory.RefStore()
	if file.CacheHash != "" {
		path, err := media.ForwardFilePath(a.memory.MediaDir(), file.CacheHash)
		if err != nil {
			return nil, err
		}
		input, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer input.Close()
		size := int64(0)
		if file.Size != nil {
			size = *file.Size
		}
		return store.CreateReadyWithSize(ctx, file.Name, input, media.MaxForwardFileBytes, size, nil)
	}
	if file.Unavailable != "" {
		return nil, fmt.Errorf("%s", file.Unavailable)
	}
	if groupID <= 0 || file.FileID == "" {
		return nil, fmt.Errorf("缺少可确认的文件来源")
	}
	return store.CreatePending(file.Name, refstore.Locator{ConversationKind: memory.ConversationKindGroup, TargetID: groupID, FileID: file.FileID, Name: file.Name, Size: file.Size, FileHash: file.FileHash, BusID: file.BusID})
}
