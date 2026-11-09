package tools

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/workerproto"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// SessionContext 会话上下文能力：消息编号与媒体引用都由会话维护
type SessionContext interface {
	BeginToolTranscript(ctx context.Context, runID, toolName, callID, arguments string) (id uint, existingResult string, resumed bool, err error)
	FinishToolTranscript(ctx context.Context, id uint, status, resultText string, enhanced *schema.ToolResult, resultRef string) (string, *schema.ToolResult, error)
	RegisterMediaRefs(refs []media.Ref)
	ResolveMediaByHash(hash string) (media.Ref, bool)
	PendingAfter(order uint64) []PendingMessage
}

// PendingMessage 待注入模型上下文的消息
type PendingMessage struct {
	Order uint64
	Msg   *schema.AgenticMessage
}

// enhancedTool 返回多模态结果的工具
type enhancedTool struct {
	info *schema.ToolInfo
	run  func(ctx context.Context, argument *schema.ToolArgument) (*schema.ToolResult, error)
}

// Info 返回工具描述
func (t *enhancedTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

// InvokableRun 执行工具并返回多模态结果
func (t *enhancedTool) InvokableRun(ctx context.Context, argument *schema.ToolArgument, _ ...tool.Option) (*schema.ToolResult, error) {
	return t.run(ctx, argument)
}

// ViewImageInput 查看图片的参数
type ViewImageInput struct {
	Hash     string `json:"hash,omitempty" jsonschema:"description=旧消息里的图片/表情包短哈希标识"`
	ImageRef string `json:"image_ref,omitempty" jsonschema:"description=消息里的 image_ref，与 hash 二选一"`
}

// ViewImageOutput 查看图片的结果，工具实际把图片本体交给模型
type ViewImageOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// NewViewImageTool 返回可以查看会话内图片的工具
func NewViewImageTool() (tool.EnhancedInvokableTool, error) {
	plain, err := utils.InferTool("viewImage", "查看一张图片或表情包的内容。新消息用 image_ref，旧工作集仍可用短哈希；两者恰选一个。", viewImageFunc)
	if err != nil {
		return nil, err
	}
	info, err := plain.Info(context.Background())
	if err != nil {
		return nil, err
	}
	return &enhancedTool{info: info, run: viewImageRun}, nil
}

// viewImageFunc 解析媒体引用
func viewImageFunc(ctx context.Context, input *ViewImageInput) (*ViewImageOutput, error) {
	if _, _, err := resolveViewImage(ctx, input); err != nil {
		return nil, err
	}
	return &ViewImageOutput{Success: true, Message: "已取到图片内容"}, nil
}

// viewImageRun 读取媒体并把图片本体返回给模型
func viewImageRun(ctx context.Context, argument *schema.ToolArgument) (*schema.ToolResult, error) {
	input := &ViewImageInput{}
	if argument != nil && strings.TrimSpace(argument.Text) != "" {
		if err := sonic.UnmarshalString(argument.Text, input); err != nil {
			return nil, fmt.Errorf("参数解析失败: %w", err)
		}
	}
	data, mime, err := resolveViewImage(ctx, input)
	if err != nil {
		return nil, err
	}
	return imagePreviewResult(data, mime)
}

// resolveViewImage 同时兼容新 image_ref 与旧会话短哈希
func resolveViewImage(ctx context.Context, input *ViewImageInput) ([]byte, string, error) {
	if input == nil {
		return nil, "", fmt.Errorf("请提供图片标识")
	}
	hasHash := strings.TrimSpace(input.Hash) != ""
	hasRef := strings.TrimSpace(input.ImageRef) != ""
	if hasHash == hasRef {
		return nil, "", fmt.Errorf("hash 与 image_ref 必须且只能提供一个")
	}
	if hasRef {
		tc := GetToolContext(ctx)
		data, mimeType, _, err := refImageBytes(ctx, tc, input.ImageRef)
		if err != nil {
			return nil, "", err
		}
		if len(data) > workerproto.MaxImageBytes {
			return nil, "", fmt.Errorf("图片超过 10 MiB")
		}
		return data, mimeType, nil
	}
	item, err := resolveMedia(ctx, input)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(item.Path)
	if err != nil {
		return nil, "", fmt.Errorf("读取图片失败: %w", err)
	}
	mime := item.Mime
	if mime == "" {
		mime = "image/jpeg"
	}
	return data, mime, nil
}

// resolveMedia 从会话里按哈希取媒体
func resolveMedia(ctx context.Context, input *ViewImageInput) (media.Ref, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.Session == nil {
		return media.Ref{}, fmt.Errorf("媒体读取能力未初始化")
	}
	if input == nil || strings.TrimSpace(input.Hash) == "" {
		return media.Ref{}, fmt.Errorf("请提供图片哈希标识")
	}
	item, ok := tc.Session.ResolveMediaByHash(input.Hash)
	if !ok {
		return media.Ref{}, fmt.Errorf("没有找到标识为 %s 的图片，可能已被清理", input.Hash)
	}
	return item, nil
}

// SaveStickerInput 收藏表情包的参数
type SaveStickerInput struct {
	Hash        string `json:"hash,omitempty" jsonschema:"description=旧消息里的表情包短哈希"`
	ImageRef    string `json:"image_ref,omitempty" jsonschema:"description=来自表情包的新 image_ref，与 hash 二选一"`
	Description string `json:"description" jsonschema:"description=表情含义的一句话备注"`
}

// SaveStickerOutput 收藏表情包的结果
type SaveStickerOutput struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	StickerID uint   `json:"sticker_id"`
}

// NewSaveStickerTool 返回收藏表情包的工具，只接受消息里的表情
func NewSaveStickerTool() (tool.InvokableTool, error) {
	return utils.InferTool("saveSticker", "把一条表情包收藏进表情包库；hash 用于旧消息，image_ref 用于新消息，两者恰选一个且必须来自表情包。", saveStickerFunc)
}

// saveStickerFunc 收藏表情包
func saveStickerFunc(ctx context.Context, input *SaveStickerInput) (*SaveStickerOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.Session == nil {
		return nil, NewTerminalToolError(fmt.Errorf("表情包收藏能力未初始化"))
	}
	hashValue := ""
	refValue := ""
	if input != nil {
		hashValue = strings.TrimSpace(input.Hash)
		refValue = strings.TrimSpace(input.ImageRef)
	}
	if boolCount(hashValue != "", refValue != "") != 1 {
		return nil, fmt.Errorf("hash 与 image_ref 必须且只能提供一个")
	}
	if tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("表情包收藏能力未初始化"))
	}
	var item media.Ref
	var tempPath string
	if refValue != "" {
		ref, err := tc.MemoryMgr.RefStore().Get(refValue)
		if err != nil {
			return nil, err
		}
		if !ref.Sticker || !ref.Image {
			return nil, fmt.Errorf("这个 image_ref 不是来自表情包")
		}
		data, _, _, err := refImageBytes(ctx, tc, refValue)
		if err != nil {
			return nil, err
		}
		if len(data) > workerproto.MaxImageBytes {
			return nil, fmt.Errorf("表情包超过 10 MiB")
		}
		file, err := os.CreateTemp("", "mumu-sticker-*")
		if err != nil {
			return nil, err
		}
		tempPath = file.Name()
		defer os.Remove(tempPath)
		if _, err := file.Write(data); err != nil {
			file.Close()
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
		item = media.Ref{Path: tempPath, Mime: ref.MIME, Hash: fmt.Sprintf("%x", md5.Sum(data)), Sticker: true}
	} else {
		var ok bool
		item, ok = tc.Session.ResolveMediaByHash(hashValue)
		if !ok {
			return nil, fmt.Errorf("没有找到标识为 %s 的表情", hashValue)
		}
		if !item.Sticker {
			return nil, fmt.Errorf("只能收藏表情，标识 %s 是普通图片", hashValue)
		}
	}
	stored, err := storeStickerFile(item, tc.StickerDir)
	if err != nil {
		return nil, err
	}
	sticker := &memory.Sticker{FileName: stored, FileHash: item.Hash, Description: strings.TrimSpace(input.Description)}
	isDuplicate, err := tc.MemoryMgr.SaveSticker(sticker)
	if err != nil {
		return nil, err
	}
	if isDuplicate {
		return &SaveStickerOutput{Success: true, Message: "表情已收藏过，备注未变", StickerID: sticker.ID}, nil
	}
	return &SaveStickerOutput{Success: true, Message: "已收藏表情", StickerID: sticker.ID}, nil
}

// storeStickerFile 把媒体复制进表情包库目录
func storeStickerFile(item media.Ref, storagePath string) (string, error) {
	if strings.TrimSpace(storagePath) == "" {
		storagePath = "./stickers"
	}
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		return "", fmt.Errorf("创建表情包目录失败: %w", err)
	}
	ext := filepath.Ext(item.Path)
	if ext == "" {
		switch item.Mime {
		case "image/png":
			ext = ".png"
		case "image/gif":
			ext = ".gif"
		case "image/webp":
			ext = ".webp"
		default:
			ext = ".jpg"
		}
	}
	name := item.Hash + ext
	target := filepath.Join(storagePath, name)
	if _, err := os.Stat(target); err == nil {
		return name, nil
	}
	data, err := os.ReadFile(item.Path)
	if err != nil {
		return "", fmt.Errorf("读取表情失败: %w", err)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", fmt.Errorf("保存表情失败: %w", err)
	}
	return name, nil
}
