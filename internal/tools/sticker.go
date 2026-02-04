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
	Keyword string `json:"keyword" jsonschema:"description=按描述关键词搜索，如：猫、开心、无语等"`
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
		output := &SearchStickersOutput{Success: false, Message: "搜索失败: " + err.Error()}
		LogToolCall("searchStickers", input, output, err)
		return output, nil
	}

	if len(stickers) == 0 {
		output := &SearchStickersOutput{Success: true, Message: "没有找到相关表情包"}
		LogToolCall("searchStickers", input, output, nil)
		return output, nil
	}

	results := make([]StickerSummary, 0, len(stickers))
	for _, s := range stickers {
		results = append(results, StickerSummary{
			ID:          s.ID,
			Description: s.Description,
			UseCount:    s.UseCount,
		})
	}

	output := &SearchStickersOutput{Success: true, Stickers: results}
	LogToolCall("searchStickers", input, output, nil)
	return output, nil
}

func NewSearchStickersTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"searchStickers",
		"搜索已保存的表情包。可以通过关键词搜索，如情绪（开心、无语）、内容（猫、狗）等。",
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
	if tc.Bot == nil {
		return &SendStickerOutput{Success: false, Message: "Bot 未连接"}, nil
	}
	if input.StickerID == 0 {
		return &SendStickerOutput{Success: false, Message: "表情包 ID 不能为空"}, nil
	}

	// 获取表情包信息
	sticker, err := tc.MemoryMgr.GetStickerByID(input.StickerID)
	if err != nil {
		output := &SendStickerOutput{Success: false, Message: "表情包不存在"}
		LogToolCall("sendSticker", input, output, err)
		return output, nil
	}

	// 构建文件路径
	cfg := config.Get()
	storagePath := cfg.Sticker.StoragePath
	if storagePath == "" {
		storagePath = "data/stickers"
	}
	filePath, err := filepath.Abs(filepath.Join(storagePath, sticker.FileName))
	if err != nil {
		output := &SendStickerOutput{Success: false, Message: "获取文件路径失败"}
		LogToolCall("sendSticker", input, output, err)
		return output, nil
	}

	// 检查文件是否存在
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		output := &SendStickerOutput{Success: false, Message: "表情包文件不存在"}
		LogToolCall("sendSticker", input, output, err)
		return output, nil
	}

	// 发送图片（作为表情包）
	msgID, err := tc.Bot.SendImageMessage(tc.GroupID, filePath, true)
	if err != nil {
		output := &SendStickerOutput{Success: false, Message: "发送失败: " + err.Error()}
		LogToolCall("sendSticker", input, output, err)
		return output, nil
	}

	// 更新使用记录
	_ = tc.MemoryMgr.UpdateStickerUsage(input.StickerID)

	output := &SendStickerOutput{
		Success:   true,
		Message:   "表情包已发送",
		MessageID: msgID,
	}
	LogToolCall("sendSticker", input, output, nil)
	return output, nil
}

func NewSendStickerTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"sendSticker",
		"发送一个已保存的表情包。先用searchStickers搜索找到合适的表情包，再用这个工具发送。",
		sendStickerFunc,
	)
}
