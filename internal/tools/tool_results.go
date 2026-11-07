package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type ReadToolResultInput struct {
	ResultRef string `json:"result_ref"`
	Query     string `json:"query,omitempty" jsonschema:"description=可选关键词，区分大小写的字面匹配；返回从 offset 起的首个命中及附近正文"`
	Offset    int    `json:"offset,omitempty" jsonschema:"description=从0开始的字符位置；搜索时表示从此处查找"`
	Length    int    `json:"length,omitempty" jsonschema:"description=正文窗口长度，默认4000字符，最大8000"`
}

func readToolResult(ctx context.Context, input *ReadToolResultInput) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具结果读取能力未初始化"))
	}
	if input == nil || input.ResultRef == "" {
		return nil, fmt.Errorf("result_ref 不能为空")
	}
	window, err := tc.MemoryMgr.ReadToolResultText(ctx, input.ResultRef, input.Offset, input.Length, input.Query)
	if err != nil {
		return nil, err
	}
	output := map[string]any{
		"success": true, "result_ref": input.ResultRef, "content": window.Content,
		"offset": window.Offset, "total": window.Total, "complete": window.Complete, "next_offset": window.NextOffset,
	}
	if input.Query != "" {
		output["query"] = input.Query
		output["found"] = window.MatchOffset != nil
		if window.MatchOffset != nil {
			output["match_offset"] = *window.MatchOffset
		}
	}
	return output, nil
}

func NewReadToolResultTool() (tool.InvokableTool, error) {
	return utils.InferTool("readToolResult", "按字符窗口读取已保存的工具结果，或在指定结果内按 query 搜索；offset 是返回窗口起点，match_offset 是命中位置，next_offset 用于以相同 query 续查，complete 表示已无后续正文或匹配。", readToolResult)
}
