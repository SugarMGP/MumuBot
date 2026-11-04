package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type BashInput struct {
	Command        string `json:"command" jsonschema:"description=要在 worker 工作区执行的 bash 命令"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"description=超时秒数，默认300，最大1800"`
}

func bashTool(ctx context.Context, input *BashInput) (*BashResult, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.BashCallback == nil {
		return nil, NewTerminalToolError(fmt.Errorf("worker 执行能力未初始化"))
	}
	if input == nil || input.Command == "" {
		return nil, fmt.Errorf("command 不能为空")
	}
	result, err := tc.BashCallback(ctx, input.Command, input.TimeoutSeconds)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, NewTerminalToolError(fmt.Errorf("worker executor 返回空结果"))
	}
	result.Success = result.ExitCode == 0 && !result.TimedOut
	return result, nil
}

func NewBashTool() (tool.InvokableTool, error) {
	return utils.InferTool("bash", "在 worker 的 home 工作区执行一条 bash 命令；每次都是新 shell。", bashTool)
}
