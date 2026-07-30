package tools

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type SearchJargonInput struct {
	Keyword string `json:"keyword" jsonschema:"description=搜索关键词"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=返回数量，默认10"`
}

type SearchJargonOutput struct {
	Success bool             `json:"success"`
	Count   int              `json:"count"`
	Jargons []map[string]any `json:"jargons,omitempty"`
	Message string           `json:"message,omitempty"`
}

func searchJargonFunc(ctx context.Context, input *SearchJargonInput) (*SearchJargonOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &SearchJargonOutput{Message: "工具上下文未初始化"}, nil
	}
	keyword := strings.TrimSpace(input.Keyword)
	if keyword == "" {
		return &SearchJargonOutput{Message: "搜索关键词不能为空"}, nil
	}
	limit := input.Limit
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	rows, err := tc.MemoryMgr.SearchJargons(tc.GroupID, keyword, limit)
	if err != nil {
		return &SearchJargonOutput{Message: err.Error()}, nil
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.ID, "term": row.Term, "meaning": row.Meaning})
	}
	return &SearchJargonOutput{Success: true, Count: len(items), Jargons: items}, nil
}

func NewSearchJargonTool() (tool.InvokableTool, error) {
	return utils.InferTool("searchJargon", "搜索当前群已经审核通过的黑话、术语或梗。", searchJargonFunc)
}
