package tools

import (
	"context"
	"fmt"
	"mumu-bot/internal/memory"

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
	Approve bool `json:"approve" jsonschema:"description=是否通过审核，true=通过，false=拒绝"`
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

	if err := tc.MemoryMgr.SaveExpression(exp); err != nil {
		output := &SaveExpressionOutput{Success: false, Message: err.Error()}
		LogToolCall("saveExpression", input, output, err)
		return output, nil
	}

	output := &SaveExpressionOutput{Success: true, Message: "已记住这种表达方式"}
	LogToolCall("saveExpression", input, output, nil)
	return output, nil
}

func NewSaveExpressionTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"saveExpression",
		"保存你学到的群友表达方式或口头禅。当你发现群友在特定场景下有独特的说话习惯时，可以记录下来以便模仿。",
		saveExpressionFunc,
	)
}

// ==================== 获取表达方式工具 ====================

type GetExpressionsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=返回数量，默认5"`
}

type GetExpressionsOutput struct {
	Success     bool     `json:"success"`
	Expressions []string `json:"expressions,omitempty"`
	Message     string   `json:"message,omitempty"`
}

func getExpressionsFunc(ctx context.Context, input *GetExpressionsInput) (*GetExpressionsOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &GetExpressionsOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 5
	}

	exps, err := tc.MemoryMgr.GetExpressions(tc.GroupID, limit)
	if err != nil {
		output := &GetExpressionsOutput{Success: false, Message: err.Error()}
		LogToolCall("getExpressions", input, output, err)
		return output, nil
	}

	results := make([]string, 0, len(exps))
	for _, e := range exps {
		results = append(results, fmt.Sprintf("[%s]: %s (示例: %s)", e.Situation, e.Style, e.Examples))
	}

	output := &GetExpressionsOutput{
		Success:     true,
		Expressions: results,
	}
	LogToolCall("getExpressions", input, output, nil)
	return output, nil
}

func NewGetExpressionsTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getExpressions",
		"查看你学到的群友表达方式和口头禅。在你想模仿群友说话或者不知道该怎么表达时使用。",
		getExpressionsFunc,
	)
}
