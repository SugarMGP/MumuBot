package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type ReadToolResultInput struct {
	ResultRef string `json:"result_ref"`
	Offset    int    `json:"offset,omitempty"`
	Length    int    `json:"length,omitempty" jsonschema:"description=默认4000字符，最大8000"`
}

func readToolResult(ctx context.Context, input *ReadToolResultInput) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具结果读取能力未初始化"))
	}
	if input == nil || input.ResultRef == "" {
		return nil, fmt.Errorf("result_ref 不能为空")
	}
	content, total, complete, err := tc.MemoryMgr.ReadToolResultText(ctx, input.ResultRef, input.Offset, input.Length)
	if err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "result_ref": input.ResultRef, "content": content, "complete": complete, "total": total, "next_offset": input.Offset + len([]rune(content))}, nil
}

func NewReadToolResultTool() (tool.InvokableTool, error) {
	return utils.InferTool("readToolResult", "读取已经保存的大型工具结果，按字符窗口分页；不要用它重新执行原工具。", readToolResult)
}
