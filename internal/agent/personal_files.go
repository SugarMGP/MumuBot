package agent

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/refstore"
	"mumu-bot/internal/tools"
	"mumu-bot/internal/workerproto"
)

type workerFileAdapter struct {
	exec *workerExecutor
	refs *refstore.Store
}

func (a workerFileAdapter) ListFiles(ctx context.Context, req workerproto.FileListRequest) (*workerproto.FileListResponse, error) {
	return a.exec.listFiles(ctx, req)
}

func (a workerFileAdapter) ReadFile(ctx context.Context, req workerproto.FileReadRequest) (*workerproto.FileReadResponse, error) {
	return a.exec.readFile(ctx, req)
}

func (a workerFileAdapter) WriteFile(ctx context.Context, req workerproto.FileWriteRequest) (*workerproto.FileWriteResponse, error) {
	return a.exec.writeFile(ctx, req)
}

func (a workerFileAdapter) SaveFile(ctx context.Context, req workerproto.FileSaveRequest) (*workerproto.FileSaveResponse, error) {
	return a.exec.saveFile(ctx, req)
}

func (a workerFileAdapter) FileContent(ctx context.Context, filePath string) (*tools.FileContent, error) {
	content, err := a.exec.fileContent(ctx, filePath)
	if err != nil {
		return nil, err
	}
	return &tools.FileContent{
		Data: content.Data, ContentType: content.ContentType,
	}, nil
}

// sendPersonalFile 把个人目录里的真实字节投递到可用会话；文件上传不伪造消息编号
func (a *Agent) sendPersonalFile(ctx context.Context, kind string, targetID int64, name string, data []byte, asImage bool) (*tools.SendFileResult, error) {
	return a.sendPersonalFileWithRef(ctx, kind, targetID, name, data, asImage, "")
}

// sendPersonalFileWithRef 在图片自身日志里保留来源 image_ref，普通文件不编造
func (a *Agent) sendPersonalFileWithRef(ctx context.Context, kind string, targetID int64, name string, data []byte, asImage bool, imageRef string) (*tools.SendFileResult, error) {
	if kind != memory.ConversationKindGroup && kind != memory.ConversationKindPrivate {
		return nil, fmt.Errorf("只能发送到群聊或好友私聊")
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("文件为空")
	}
	target, err := a.memory.WithContext(ctx).GetConversationTarget(ctx, kind, targetID)
	if err != nil || !target.Active || target.Blocked {
		return nil, fmt.Errorf("目标会话不可用")
	}
	if strings.TrimSpace(name) == "" {
		name = "file"
	}
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	if asImage {
		if len(data) > workerproto.MaxImageBytes {
			return nil, fmt.Errorf("图片超过 10 MiB")
		}
		messageID, seq, sendErr := a.bot.SendImageBytes(ctx, kind, targetID, data, false)
		if sendErr != nil {
			return nil, sendErr
		}
		placeholder := " [图片:" + name + "]"
		if imageRef != "" {
			placeholder += " [图片引用:" + imageRef + "]"
		}
		a.onMessage(&onebot.ConversationMessage{
			ConversationKind: kind, MessageID: messageID, TargetID: targetID,
			UserID: a.bot.GetSelfID(), Nickname: a.botName(),
			MessageParts: []onebot.MessagePart{{Kind: "text", Text: placeholder}},
			Time:         time.Now(), ReceivedAt: time.Now(), ArrivalSeq: seq,
		})
		return &tools.SendFileResult{Kind: "image", MessageID: messageID, Name: name, Size: int64(len(data))}, nil
	}
	if len(data) > workerproto.MaxFileBytes {
		return nil, fmt.Errorf("文件超过 32 MiB")
	}
	fileID, err := a.bot.UploadFileBytes(ctx, kind, targetID, name, data)
	if err != nil {
		return nil, err
	}
	return &tools.SendFileResult{Kind: "file", FileID: fileID, Name: name, Size: int64(len(data))}, nil
}

// CreateSnapshot 从 worker 受限字节流直接生成 ready ref
func (a workerFileAdapter) CreateSnapshot(ctx context.Context, filePath string, maxBytes int64) (*refstore.Ref, error) {
	if a.refs == nil {
		return nil, fmt.Errorf("文件快照仓库未初始化")
	}
	body, expected, err := a.exec.fileContentStream(ctx, filePath)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	ref, err := a.refs.CreateReadyWithSize(ctx, path.Base(filePath), body, maxBytes, expected, nil)
	if err != nil {
		return nil, err
	}
	if expected > 0 && ref.Size != expected {
		return nil, fmt.Errorf("worker 文件长度不符: 声明 %d，实际 %d", expected, ref.Size)
	}
	return ref, nil
}

// ImportSnapshot 把 ready ref 的真实字节流式写入 worker home
func (a workerFileAdapter) ImportSnapshot(ctx context.Context, token, filePath string, overwrite bool) error {
	if a.refs == nil {
		return fmt.Errorf("文件快照仓库未初始化")
	}
	ref, file, err := a.refs.Open(token)
	if err != nil {
		return err
	}
	defer file.Close()
	result, err := a.exec.putFileContent(ctx, filePath, overwrite, file, ref.Size)
	if err != nil {
		return err
	}
	if result.Size != ref.Size {
		return fmt.Errorf("worker 导入长度不符: 声明 %d，实际 %d", ref.Size, result.Size)
	}
	return nil
}
