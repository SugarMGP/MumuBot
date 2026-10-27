package tools

import (
	"context"
	"fmt"

	"mumu-bot/internal/onebot"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ==================== 查询 QQ 表情工具 ====================

// SearchFacesInput 查询 QQ 表情的参数
type SearchFacesInput struct {
	Keyword string `json:"keyword,omitempty" jsonschema:"description=表情名称关键词，留空返回常见表情"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=返回数量，默认20，最大50"`
}

// FaceSummary 一条 QQ 自带表情
type FaceSummary struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// SearchFacesOutput 查询结果
type SearchFacesOutput struct {
	Success bool          `json:"success"`
	Faces   []FaceSummary `json:"faces,omitempty"`
	Message string        `json:"message,omitempty"`
}

func searchFacesFunc(ctx context.Context, input *SearchFacesInput) (*SearchFacesOutput, error) {
	if input == nil {
		return nil, fmt.Errorf("查询参数不能为空")
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	entries := onebot.SearchQQFaces(input.Keyword, limit)
	if len(entries) == 0 {
		return &SearchFacesOutput{Success: true, Message: "没有找到相关表情"}, nil
	}
	results := make([]FaceSummary, 0, len(entries))
	for _, entry := range entries {
		results = append(results, FaceSummary{ID: entry.ID, Name: entry.Name})
	}
	return &SearchFacesOutput{Success: true, Faces: results}, nil
}

// NewSearchFacesTool 返回查询 QQ 自带表情的工具
func NewSearchFacesTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"searchFaces",
		"查询 QQ 自带表情的编号和名称。想发表情时先用它找到编号，再在发言内容里写 %qface:名称% 或 %qface:编号%。",
		searchFacesFunc,
	)
}
