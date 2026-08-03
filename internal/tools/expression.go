package tools

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type SearchExpressionsInput struct {
	Query string `json:"query" jsonschema:"description=当前想贴合的聊天场景、语气或说法"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=返回数量，默认3，最大5"`
}

type ExpressionReference struct {
	Situation  string   `json:"situation"`
	Expression string   `json:"expression"`
	Examples   []string `json:"examples"`
}

type SearchExpressionsOutput struct {
	Success     bool                  `json:"success"`
	Count       int                   `json:"count"`
	Expressions []ExpressionReference `json:"expressions,omitempty"`
	Message     string                `json:"message,omitempty"`
}

func searchExpressionsFunc(ctx context.Context, input *SearchExpressionsInput) (*SearchExpressionsOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return &SearchExpressionsOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return &SearchExpressionsOutput{Success: false, Message: "查询内容不能为空"}, nil
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 3
	} else if limit > 5 {
		limit = 5
	}

	matches, err := tc.MemoryMgr.SearchExpressions(ctx, tc.GroupID, query, tc.SnapshotMessageID, limit)
	if err != nil {
		return &SearchExpressionsOutput{Success: false, Message: err.Error()}, nil
	}
	result := make([]ExpressionReference, 0, len(matches))
	for _, match := range matches {
		result = append(result, ExpressionReference{
			Situation: match.Situation, Expression: match.Expression, Examples: match.Examples,
		})
	}
	return &SearchExpressionsOutput{Success: true, Count: len(result), Expressions: result}, nil
}

func NewSearchExpressionsTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"searchExpressions",
		"查询当前群已经确认的表达方式和少量原始示例。需要接梗、吐槽、起哄或贴合本群说法时使用；普通事实回答不需要查询。结果只用于理解语气和用法，不要逐字照抄示例。",
		searchExpressionsFunc,
	)
}
