package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mumu-bot/internal/config"
	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// SessionContext 会话上下文能力：消息编号与媒体引用都由会话维护
type SessionContext interface {
	RegisterMessage(messageID int64) string
	ResolveMessageRef(ref string) (int64, bool)
	ResolveMediaByHash(hash string) (media.Ref, bool)
	PendingAfter(seq uint64) []PendingMessage
	MarkInjected(seq uint64)
	CommitRound(messages []*schema.AgenticMessage)
}

// PendingMessage 待注入模型上下文的消息
type PendingMessage struct {
	Ref        string
	Content    string
	SenderID   int64
	Nickname   string
	ArrivalSeq uint64
	Msg        *schema.AgenticMessage
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
	Hash string `json:"hash" jsonschema:"description=图片或表情的短哈希标识，取自消息里的方括号标记"`
}

// ViewImageOutput 查看图片的结果，工具实际把图片本体交给模型
type ViewImageOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// NewViewImageTool 返回可以查看会话内图片的工具
func NewViewImageTool() (tool.EnhancedInvokableTool, error) {
	plain, err := utils.InferTool("viewImage", "查看一张图片或表情的内容。消息里的图片和表情都带短哈希标记，用那个标记调用，一次只看一张。", viewImageFunc)
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
	if _, err := resolveMedia(ctx, input); err != nil {
		return nil, err
	}
	return &ViewImageOutput{Success: true, Message: "已取到图片内容"}, nil
}

// viewImageRun 读取媒体并作为图片返回给模型
func viewImageRun(ctx context.Context, argument *schema.ToolArgument) (*schema.ToolResult, error) {
	input := &ViewImageInput{}
	if argument != nil && strings.TrimSpace(argument.Text) != "" {
		if err := sonic.UnmarshalString(argument.Text, input); err != nil {
			return nil, fmt.Errorf("参数解析失败: %w", err)
		}
	}
	item, err := resolveMedia(ctx, input)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(item.Path)
	if err != nil {
		return nil, fmt.Errorf("读取图片失败: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	mime := item.Mime
	if mime == "" {
		mime = "image/jpeg"
	}
	parts := []schema.ToolOutputPart{
		{Type: schema.ToolPartTypeText, Text: fmt.Sprintf("图片 %s，%d 字节", mime, len(data))},
		{Type: schema.ToolPartTypeImage, Image: &schema.ToolOutputImage{MessagePartCommon: schema.MessagePartCommon{
			Base64Data: &encoded,
			MIMEType:   mime,
		}}},
	}
	return &schema.ToolResult{Parts: parts}, nil
}

// resolveMedia 从会话里按哈希取媒体
func resolveMedia(ctx context.Context, input *ViewImageInput) (media.Ref, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.Session == nil {
		return media.Ref{}, NewTerminalToolError(fmt.Errorf("媒体读取能力未初始化"))
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
	Hash        string `json:"hash" jsonschema:"description=表情的短哈希标识，取自消息里的方括号标记"`
	Description string `json:"description" jsonschema:"description=表情含义的一句话备注"`
}

// SaveStickerOutput 收藏表情包的结果
type SaveStickerOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// NewSaveStickerTool 返回收藏表情包的工具，只接受消息里的表情
func NewSaveStickerTool() (tool.InvokableTool, error) {
	return utils.InferTool("saveSticker", "把一条表情收藏进表情包库，只支持消息里标注为表情的条目，普通图片不能收藏。", saveStickerFunc)
}

// saveStickerFunc 收藏表情包
func saveStickerFunc(ctx context.Context, input *SaveStickerInput) (*SaveStickerOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.Session == nil {
		return nil, NewTerminalToolError(fmt.Errorf("表情包收藏能力未初始化"))
	}
	if input == nil || strings.TrimSpace(input.Hash) == "" {
		return nil, fmt.Errorf("请提供表情哈希标识")
	}
	item, ok := tc.Session.ResolveMediaByHash(input.Hash)
	if !ok {
		return nil, fmt.Errorf("没有找到标识为 %s 的表情", input.Hash)
	}
	if !item.Sticker {
		return nil, fmt.Errorf("只能收藏表情，标识 %s 是普通图片", input.Hash)
	}
	if tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("表情包收藏能力未初始化"))
	}
	cfg := config.Get()
	stored, err := storeStickerFile(item, cfg.Sticker.StoragePath)
	if err != nil {
		return nil, err
	}
	sticker := &memory.Sticker{FileName: stored, FileHash: item.Hash, Description: strings.TrimSpace(input.Description)}
	isDuplicate, err := tc.MemoryMgr.SaveSticker(sticker)
	if err != nil {
		return nil, err
	}
	if isDuplicate {
		return &SaveStickerOutput{Success: true, Message: "表情已收藏过，备注未变"}, nil
	}
	return &SaveStickerOutput{Success: true, Message: "已收藏表情"}, nil
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
