package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ==================== 搜索表情包工具 ====================

type SearchStickersInput struct {
	Keyword string `json:"keyword" jsonschema:"description=按关键词搜索，如：猫、开心、无语等，关键词之间用空格隔开"`
	Cursor  string `json:"cursor,omitempty" jsonschema:"description=上一页返回的 cursor"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=默认12，最大99"`
}

type StickerSummary struct {
	ID          uint   `json:"id"`
	Description string `json:"description"`
	UseCount    int    `json:"use_count"`
}

type SearchStickersOutput struct {
	Success    bool             `json:"success"`
	Stickers   []StickerSummary `json:"stickers,omitempty"`
	Message    string           `json:"message,omitempty"`
	NextCursor string           `json:"next_cursor,omitempty"`
	HasMore    bool             `json:"has_more"`
}

func searchStickersFunc(ctx context.Context, input *SearchStickersInput) (*SearchStickersOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if input == nil {
		return nil, fmt.Errorf("搜索参数不能为空")
	}

	page, err := tc.MemoryMgr.SearchStickerPage(ctx, input.Keyword, input.Cursor, input.Limit)
	if err != nil {
		return nil, fmt.Errorf("搜索失败: %w", err)
	}

	if len(page.Items) == 0 {
		return &SearchStickersOutput{Success: true, Message: "没有找到相关表情包", HasMore: page.HasMore, NextCursor: page.NextCursor}, nil
	}

	results := make([]StickerSummary, 0, len(page.Items))
	for _, s := range page.Items {
		results = append(results, StickerSummary{
			ID:          s.ID,
			Description: s.Description,
			UseCount:    s.UseCount,
		})
	}

	return &SearchStickersOutput{Success: true, Stickers: results, NextCursor: page.NextCursor, HasMore: page.HasMore}, nil
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
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func sendStickerFunc(ctx context.Context, input *SendStickerInput) (*SendStickerOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if tc.SendStickerCallback == nil {
		return nil, NewTerminalToolError(fmt.Errorf("发送表情包回调未初始化"))
	}
	if input == nil || input.StickerID == 0 {
		return nil, fmt.Errorf("表情包 ID 不能为空")
	}

	// 获取表情包信息
	sticker, err := tc.MemoryMgr.GetStickerByID(input.StickerID)
	if err != nil {
		return nil, fmt.Errorf("获取表情包信息失败: %w", err)
	}

	// 文件由发送入口通过 os.Root 读取，工具层只传保存的相对文件名
	if err := tc.SendStickerCallback(ctx, tc.TargetID, sticker.FileName, sticker.Description); err != nil {
		return nil, NewTerminalToolError(err)
	}

	// 更新使用记录
	_ = tc.MemoryMgr.UpdateStickerUsage(input.StickerID)

	return &SendStickerOutput{
		Success: true,
		Message: "表情包已发送",
	}, nil
}

func NewSendStickerTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"sendSticker",
		"发送一个已保存的表情包。先用searchStickers搜索表情包，再用该工具发送。",
		sendStickerFunc,
	)
}
