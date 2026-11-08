package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mumu-bot/internal/filemeta"
	"mumu-bot/internal/media"
	"mumu-bot/internal/refstore"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ensureReadyRef 在首次读取/发送/查看时把 QQ pending ref 固定成快照
func ensureReadyRef(ctx context.Context, tc *ToolContext, token string) (*refstore.Ref, error) {
	if tc == nil || tc.MemoryMgr == nil || tc.MemoryMgr.RefStore() == nil {
		return nil, NewTerminalToolError(fmt.Errorf("文件快照能力未初始化"))
	}
	store := tc.MemoryMgr.RefStore()
	ref, err := store.Get(token)
	if err != nil {
		return nil, err
	}
	if ref.State == refstore.StateReady {
		return ref, nil
	}
	if ref.Locator == nil || (tc.Bot == nil && tc.ResolvePendingFile == nil) {
		return nil, refstore.ErrNotReady
	}
	return store.EnsureReady(ctx, token, func(fetchCtx context.Context, locator refstore.Locator) (io.ReadCloser, error) {
		resolve := tc.ResolvePendingFile
		if resolve == nil {
			resolve = func(callCtx context.Context, item refstore.Locator) (string, error) {
				return tc.Bot.ResolveFileURL(callCtx, item.ConversationKind, item.TargetID, filemeta.Ref{
					FileID: item.FileID, Name: item.Name, Size: item.Size,
					FileHash: item.FileHash, BusID: item.BusID,
				})
			}
		}
		url, err := resolve(fetchCtx, locator)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		client := &http.Client{Timeout: 120 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			return nil, fmt.Errorf("下载文件返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		return resp.Body, nil
	})
}

type ChatReadFileInput struct {
	FileRef string `json:"file_ref" jsonschema:"description=消息正文里的 file_ref 或 image_ref"`
	Offset  int    `json:"offset,omitempty" jsonschema:"description=从第几个 Unicode 字符开始，默认 0"`
	Length  int    `json:"length,omitempty" jsonschema:"description=本段最多读取多少字符，默认 4000，最大 8000"`
}

type ChatReadFileOutput struct {
	Success    bool   `json:"success"`
	FileRef    string `json:"file_ref"`
	ImageRef   string `json:"image_ref,omitempty"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset,omitempty"`
	TotalChars int    `json:"total_chars"`
	Size       int64  `json:"size"`
	Binary     bool   `json:"binary,omitempty"`
	Message    string `json:"message,omitempty"`
}

func chatReadFile(ctx context.Context, input *ChatReadFileInput) (*ChatReadFileOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || input == nil || strings.TrimSpace(input.FileRef) == "" {
		return nil, fmt.Errorf("请提供 file_ref")
	}
	if _, err := ensureReadyRef(ctx, tc, input.FileRef); err != nil {
		return nil, err
	}
	store := tc.MemoryMgr.RefStore()
	got, content, offset, next, total, binary, err := store.ReadText(input.FileRef, input.Offset, input.Length)
	if err != nil {
		return nil, err
	}
	message := ""
	if binary {
		message = "这不是 UTF-8 文本文件"
	}
	return &ChatReadFileOutput{
		Success: !binary, FileRef: got.FileRef(), ImageRef: got.ImageRef(), Name: got.Name, Content: content,
		Offset: offset, NextOffset: next, TotalChars: total, Size: got.Size, Binary: binary, Message: message,
	}, nil
}

func NewChatReadFileTool() (tool.InvokableTool, error) {
	return utils.InferTool("readFile", "读取当前会话文件引用的 UTF-8 文本窗口，默认 4000、最大 8000 字符；不能浏览个人 home。", chatReadFile)
}

type ChatSendFileInput struct {
	FileRef string `json:"file_ref" jsonschema:"description=要发到当前会话的 file_ref 或 image_ref"`
	Name    string `json:"name,omitempty" jsonschema:"description=可选发送文件名，不改变旧 ref 的元数据"`
}

type ChatSendFileOutput struct {
	Success  bool   `json:"success"`
	FileID   string `json:"file_id,omitempty"`
	FileRef  string `json:"file_ref"`
	ImageRef string `json:"image_ref,omitempty"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
}

func chatSendFile(ctx context.Context, input *ChatSendFileInput) (*ChatSendFileOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.SendFileCallback == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("文件发送能力未初始化"))
	}
	if input == nil || strings.TrimSpace(input.FileRef) == "" {
		return nil, fmt.Errorf("请提供 file_ref")
	}
	if _, err := ensureReadyRef(ctx, tc, input.FileRef); err != nil {
		return nil, err
	}
	store := tc.MemoryMgr.RefStore()
	ref, file, err := store.Open(input.FileRef)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 32*1024*1024+1))
	if err != nil || len(data) > 32*1024*1024 {
		return nil, fmt.Errorf("读取文件失败或超过 32 MiB")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = ref.Name
	}
	result, err := tc.SendFileCallback(ctx, tc.ConversationKind, tc.TargetID, name, data, false)
	if err != nil {
		return nil, err
	}
	imageRef := ""
	if ref.Image && media.ImageMIME(data) != "" {
		imageRef = ref.ImageRef()
	}
	return &ChatSendFileOutput{Success: true, FileID: result.FileID, FileRef: ref.FileRef(), ImageRef: imageRef, Name: result.Name, Size: result.Size}, nil
}

func NewChatSendFileTool() (tool.InvokableTool, error) {
	return utils.InferTool("sendFile", "把可用的 file_ref/image_ref 作为文件发到当前 QQ 会话，也可以发送 life 转来的 worker 产物；不接受路径或跨会话目标。首次使用待取引用会真实下载，成功回执只有 file_id，不能伪造 QQ 消息 ID。", chatSendFile)
}

// refImageBytes 读取并验证图片资格，file_ref 也不能绕过实际位图检查
func refImageBytes(ctx context.Context, tc *ToolContext, token string) ([]byte, string, string, error) {
	ref, err := ensureReadyRef(ctx, tc, token)
	if err != nil {
		return nil, "", "", err
	}
	if !ref.Image {
		return nil, "", "", fmt.Errorf("这个引用不是可查看的图片")
	}
	data, mimeType, name, err := refBytes(ctx, tc, token)
	if err != nil {
		return nil, "", "", err
	}
	if media.ImageMIME(data) == "" {
		return nil, "", "", fmt.Errorf("这个引用不是可查看的图片")
	}
	return data, mimeType, name, nil
}

// refBytes 读取 ready ref 的真实字节，供图片发送等需要 Base64 的边界使用
func refBytes(ctx context.Context, tc *ToolContext, token string) ([]byte, string, string, error) {
	if _, err := ensureReadyRef(ctx, tc, token); err != nil {
		return nil, "", "", err
	}
	store := tc.MemoryMgr.RefStore()
	ref, file, err := store.Open(token)
	if err != nil {
		return nil, "", "", err
	}
	if _, imagePrefix, parseErr := refstore.Parse(token); parseErr != nil || (imagePrefix && !ref.Image) {
		file.Close()
		return nil, "", "", fmt.Errorf("这个引用不是可查看的图片")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 32*1024*1024+1))
	if err != nil || len(data) > 32*1024*1024 {
		return nil, "", "", fmt.Errorf("读取图片失败或超过 32 MiB")
	}
	return data, ref.MIME, ref.Name, nil
}
