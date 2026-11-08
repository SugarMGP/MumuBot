package tools

import (
	"context"
	"fmt"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type UpdateTopicToolInput struct {
	TopicID  uint                     `json:"topic_id,omitempty" jsonschema:"description=现有话题 ID，省略则新建且必须提供 summary"`
	Summary  *memory.TopicSummary     `json:"summary,omitempty" jsonschema:"description=完整摘要；新建或提供 messages 时必填，title/gist 不可空，主体和来源按事实填写"`
	Messages []memory.TopicMessageRef `json:"messages,omitempty" jsonschema:"description=本会话真实 OneBot 消息 ID 与 is_anchor；提供时须同时给完整 summary"`
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
	return utils.InferTool("updateTopic", "维护当前 QQ 会话的话题：新建、完整更新摘要与消息归属，或单独调整 active/dormant。新建省略 topic_id；messages 与 summary 一起提供。只改变索引，不立即改当前摘要，不为清空提醒硬造话题", updateTopicFunc)
}
