package tools

import (
	"context"
	"mumu-bot/internal/memory"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ==================== 审核表达方式工具 ====================

type GetUncheckedExpressionsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=返回数量，默认5"`
}

type UncheckedExpressionItem struct {
	ID        uint   `json:"id"`
	Situation string `json:"situation"`
	Style     string `json:"style"`
	Examples  string `json:"examples"`
}

type GetUncheckedExpressionsOutput struct {
	Success     bool                      `json:"success"`
	Expressions []UncheckedExpressionItem `json:"expressions,omitempty"`
	Message     string                    `json:"message,omitempty"`
}

func getUncheckedExpressionsFunc(ctx context.Context, input *GetUncheckedExpressionsInput) (*GetUncheckedExpressionsOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &GetUncheckedExpressionsOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 5
	}

	exps, err := tc.MemoryMgr.GetUncheckedExpressions(tc.GroupID, limit)
	if err != nil {
		output := &GetUncheckedExpressionsOutput{Success: false, Message: err.Error()}
		LogToolCall("getUncheckedExpressions", input, output, err)
		return output, nil
	}

	results := make([]UncheckedExpressionItem, 0, len(exps))
	for _, e := range exps {
		results = append(results, UncheckedExpressionItem{
			ID:        e.ID,
			Situation: e.Situation,
			Style:     e.Style,
			Examples:  e.Examples,
		})
	}

	output := &GetUncheckedExpressionsOutput{Success: true, Expressions: results}
	LogToolCall("getUncheckedExpressions", input, output, nil)
	return output, nil
}

func NewGetUncheckedExpressionsTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getUncheckedExpressions",
		"查看待审核的表达方式。你可以定期检查并审核这些学到的表达习惯是否准确。",
		getUncheckedExpressionsFunc,
	)
}

// ==================== 审核表达方式 ====================

type ReviewExpressionInput struct {
	ID      uint `json:"id" jsonschema:"description=表达方式ID"`
	Approve bool `json:"approve" jsonschema:"description=是否通过审核"`
}

type ReviewExpressionOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func reviewExpressionFunc(ctx context.Context, input *ReviewExpressionInput) (*ReviewExpressionOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &ReviewExpressionOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.ID == 0 {
		return &ReviewExpressionOutput{Success: false, Message: "表达方式 ID 不能为空"}, nil
	}

	err := tc.MemoryMgr.ReviewExpression(input.ID, input.Approve)
	if err != nil {
		output := &ReviewExpressionOutput{Success: false, Message: err.Error()}
		LogToolCall("reviewExpression", input, output, err)
		return output, nil
	}

	msg := "已拒绝该表达方式"
	if input.Approve {
		msg = "已通过该表达方式"
	}
	output := &ReviewExpressionOutput{Success: true, Message: msg}
	LogToolCall("reviewExpression", input, output, nil)
	return output, nil
}

func NewReviewExpressionTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"reviewExpression",
		"审核一条表达方式。如果你认为这个表达方式记录正确，可以通过；如果有误，可以拒绝。",
		reviewExpressionFunc,
	)
}

// ==================== 保存表达方式工具 ====================

type SaveExpressionInput struct {
	Situation string `json:"situation" jsonschema:"description=使用场景，例如：打招呼、吐槽、表达惊讶等"`
	Style     string `json:"style" jsonschema:"description=表达风格或具体的口头禅"`
	Example   string `json:"example,omitempty" jsonschema:"description=具体的例子"`
}

type SaveExpressionOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func saveExpressionFunc(ctx context.Context, input *SaveExpressionInput) (*SaveExpressionOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &SaveExpressionOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.Situation == "" {
		return &SaveExpressionOutput{Success: false, Message: "使用场景不能为空"}, nil
	}
	if input.Style == "" {
		return &SaveExpressionOutput{Success: false, Message: "表达风格不能为空"}, nil
	}

	exp := &memory.Expression{
		GroupID:   tc.GroupID,
		Situation: input.Situation,
		Style:     input.Style,
		Examples:  input.Example,
	}

	saved, err := tc.MemoryMgr.SaveExpression(exp)
	if err != nil {
		output := &SaveExpressionOutput{Success: false, Message: err.Error()}
		LogToolCall("saveExpression", input, output, err)
		return output, nil
	}

	msg := "已记住这种表达方式"
	if !saved {
		msg = "已存在该表达方式，无需重复保存"
	}
	output := &SaveExpressionOutput{Success: true, Message: msg}
	LogToolCall("saveExpression", input, output, nil)
	return output, nil
}

func NewSaveExpressionTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"saveExpression",
		"保存你学到的群友表达方式或口头禅。当你发现群友在特定场景下有独特的说话习惯时，可以记录下来以便模仿。不要记录自己的表达方式！",
		saveExpressionFunc,
	)
}

// ==================== 搜索表达方式工具 ====================

type SearchExpressionsInput struct {
	Keyword string `json:"keyword" jsonschema:"description=搜索关键词，可以是多个词用空格分隔"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=返回数量，默认10"`
}

type SearchExpressionsOutput struct {
	Success     bool             `json:"success"`
	Count       int              `json:"count"`
	Expressions []map[string]any `json:"expressions,omitempty"`
	Message     string           `json:"message,omitempty"`
}

func searchExpressionsFunc(ctx context.Context, input *SearchExpressionsInput) (*SearchExpressionsOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &SearchExpressionsOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if strings.TrimSpace(input.Keyword) == "" {
		return &SearchExpressionsOutput{Success: false, Message: "搜索关键词不能为空"}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}

	exps, err := tc.MemoryMgr.SearchExpressions(tc.GroupID, input.Keyword, limit)
	if err != nil {
		output := &SearchExpressionsOutput{Success: false, Message: err.Error()}
		LogToolCall("searchExpressions", input, output, err)
		return output, nil
	}

	results := make([]map[string]any, 0, len(exps))
	for _, j := range exps {
		results = append(results, map[string]any{
			"id":      j.ID,
			"content": j.Situation,
			"meaning": j.Style,
			"context": j.Examples,
			"checked": j.Checked,
		})
	}

	output := &SearchExpressionsOutput{
		Success:     true,
		Count:       len(exps),
		Expressions: results,
	}
	LogToolCall("searchExpressions", input, output, nil)
	return output, nil
}

func NewSearchExpressionsTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"searchExpressions",
		"搜索你从群友学到的表达方式和口头禅。",
		searchExpressionsFunc,
	)
}
