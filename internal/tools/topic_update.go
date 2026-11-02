package tools

import (
	"context"
	"fmt"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type UpdateTopicToolInput struct {
	TopicID  uint                     `json:"topic_id,omitempty"`
	Summary  *memory.TopicSummary     `json:"summary,omitempty"`
	Messages []memory.TopicMessageRef `json:"messages,omitempty"`
	Status   string                   `json:"status,omitempty" jsonschema:"description=active 或 dormant"`
}

func updateTopicFunc(ctx context.Context, input *UpdateTopicToolInput) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("话题维护能力未初始化"))
	}
	if input == nil {
		return nil, fmt.Errorf("缺少话题维护参数")
	}
	result, err := tc.MemoryMgr.UpdateTopic(ctx, tc.ConversationKind, tc.TargetID, memory.UpdateTopicInput{
		TopicID: input.TopicID, Summary: input.Summary, Messages: input.Messages, Status: input.Status,
	})
	if err != nil {
		return nil, err
	}
	tc.TopicMaintained = true
	return map[string]any{"success": true, "topic_id": result.TopicID, "summary_version": result.SummaryVersion, "status": result.Status, "assigned_count": result.AssignedCount}, nil
}

func NewUpdateTopicTool() (tool.InvokableTool, error) {
	return utils.InferTool("updateTopic", "更新当前会话的话题摘要、消息归属或 active/dormant 状态", updateTopicFunc)
}
