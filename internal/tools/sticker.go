package tools

import (
	"context"
	"mumu-bot/internal/config"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ==================== 搜索表情包工具 ====================

type SearchStickersInput struct {
	Keyword string `json:"keyword" jsonschema:"description=按关键词搜索，如：猫、开心、无语等，关键词之间用空格隔开"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=返回数量，默认10"`
}

type StickerSummary struct {
	ID          uint   `json:"id"`
	Description string `json:"description"`
	UseCount    int    `json:"use_count"`
}

type SearchStickersOutput struct {
	Success  bool             `json:"success"`
	Stickers []StickerSummary `json:"stickers,omitempty"`
	Message  string           `json:"message,omitempty"`
}

func searchStickersFunc(ctx context.Context, input *SearchStickersInput) (*SearchStickersOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &SearchStickersOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}

	stickers, err := tc.MemoryMgr.SearchStickers(input.Keyword, limit)
	if err != nil {
		return &SearchStickersOutput{Success: false, Message: "搜索失败: " + err.Error()}, nil
	}

	if len(stickers) == 0 {
		return &SearchStickersOutput{Success: true, Message: "没有找到相关表情包"}, nil
	}

	results := make([]StickerSummary, 0, len(stickers))
	for _, s := range stickers {
		results = append(results, StickerSummary{
			ID:          s.ID,
			Description: s.Description,
			UseCount:    s.UseCount,
		})
	}

	return &SearchStickersOutput{Success: true, Stickers: results}, nil
}

func NewSearchStickersTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"searchStickers",
		"搜索已保存的表情包。",
		searchStickersFunc,
	)
}

// ==================== 发送表情包工具 ====================

type SendStickerInput struct {
	StickerID uint `json:"sticker_id" jsonschema:"description=表情包ID（从searchStickers获取）"`
}

type SendStickerOutput struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	MessageID int64  `json:"message_id,omitempty"`
}

func sendStickerFunc(ctx context.Context, input *SendStickerInput) (*SendStickerOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &SendStickerOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	if tc.SendStickerCallback == nil {
		return &SendStickerOutput{Success: false, Message: "发送表情包回调未初始化"}, nil
	}
	if input.StickerID == 0 {
		return &SendStickerOutput{Success: false, Message: "表情包 ID 不能为空"}, nil
	}

	// 获取表情包信息
	sticker, err := tc.MemoryMgr.GetStickerByID(input.StickerID)
	if err != nil {
		return &SendStickerOutput{Success: false, Message: "表情包不存在"}, nil
	}

	// 构建文件路径
	cfg := config.Get()
	storagePath := cfg.Sticker.StoragePath
	filePath, err := filepath.Abs(filepath.Join(storagePath, sticker.FileName))
	if err != nil {
		return &SendStickerOutput{Success: false, Message: "获取文件路径失败"}, nil
	}

	// 检查文件是否存在
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return &SendStickerOutput{Success: false, Message: "表情包文件不存在"}, nil
	}

	// 发送表情包（使用回调以记录消息）
	msgID, err := tc.SendStickerCallback(ctx, tc.GroupID, filePath, sticker.Description)
	if err != nil {
		return &SendStickerOutput{Success: false, Message: err.Error()}, nil
	}
	tc.MarkActed()

	// 更新使用记录
	_ = tc.MemoryMgr.UpdateStickerUsage(input.StickerID)

	return &SendStickerOutput{
		Success:   true,
		Message:   "表情包已发送",
		MessageID: msgID,
	}, nil
}

func NewSendStickerTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"sendSticker",
		"发送一个已保存的表情包。先用searchStickers搜索表情包，再用该工具发送。",
		sendStickerFunc,
	)
}
