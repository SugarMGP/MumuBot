package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type CancelWorkerInput struct {
	WorkerID uint   `json:"worker_id" jsonschema:"description=worker 会话编号"`
	Reason   string `json:"reason,omitempty" jsonschema:"description=取消的简短原因，便于后续交接"`
}

type CancelWorkerOutput struct {
	Success  bool `json:"success"`
	Canceled bool `json:"canceled"`
}

func cancelWorker(ctx context.Context, input *CancelWorkerInput) (*CancelWorkerOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.CancelWorkerCallback == nil {
		return nil, NewTerminalToolError(fmt.Errorf("worker 取消能力未初始化"))
	}
	if input == nil || input.WorkerID == 0 {
		return nil, fmt.Errorf("worker_id 不能为空")
	}
	canceled, err := tc.CancelWorkerCallback(ctx, input.WorkerID, strings.TrimSpace(input.Reason))
	if err != nil {
		return nil, err
	}
	return &CancelWorkerOutput{Success: true, Canceled: canceled}, nil
}

// NewCancelWorkerTool 只取消 worker 当前回合，不删除槽位、历史或文件
func NewCancelWorkerTool() (tool.InvokableTool, error) {
	return utils.InferTool("cancelWorker", "取消指定 worker 的当前回合；保留会话、历史和文件，不自动重跑。worker_id 来自 listSessions，reason 写简短原因。", cancelWorker)
}
