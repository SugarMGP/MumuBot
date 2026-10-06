package tools

import (
	"context"
	"fmt"
	"strings"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// SearchPrivateMessagesInput 私聊历史检索参数
type SearchPrivateMessagesInput struct {
	Query    string `json:"query,omitempty" jsonschema:"description=要搜索的关键词，留空表示按时间倒序取最近的私聊原文"`
	Limit    int    `json:"limit,omitempty" jsonschema:"description=返回条数，默认8，最多20"`
	BeforeID uint   `json:"before_id,omitempty" jsonschema:"description=继续查询更早消息时填上页的 next_before_id"`
}

// PrivateMessageHit 一条私聊原文
type PrivateMessageHit struct {
	MessageRef string `json:"message_ref"`
	SenderID   int64  `json:"sender_id"`
	Nickname   string `json:"nickname"`
	Time       string `json:"time"`
	Content    string `json:"content"`
}

// SearchPrivateMessagesOutput 私聊历史检索结果
type SearchPrivateMessagesOutput struct {
	Success      bool                `json:"success"`
	HasMore      bool                `json:"has_more"`
	NextBeforeID uint                `json:"next_before_id,omitempty"`
	Messages     []PrivateMessageHit `json:"messages"`
}

func searchPrivateMessagesFunc(ctx context.Context, input *SearchPrivateMessagesInput) (*SearchPrivateMessagesOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("工具上下文未初始化"))
	}
	if tc.ConversationKind != memory.ConversationKindPrivate {
		return nil, fmt.Errorf("只能在私聊中检索当前好友的历史消息")
	}
	if tc.SnapshotMessageID == 0 {
		return nil, NewTerminalToolError(fmt.Errorf("当前私聊快照未就绪"))
	}
	upper, err := tc.MemoryMgr.GetMessageLogByScope(tc.ConversationKind, tc.TargetID, tc.SnapshotMessageID)
	if err != nil {
		return nil, NewTerminalToolError(fmt.Errorf("读取私聊历史范围失败: %w", err))
	}

	limit := 8
	if input != nil && input.Limit > 0 {
		limit = min(input.Limit, 20)
	}
	keyword := ""
	beforeID := uint(0)
	if input != nil {
		keyword = strings.TrimSpace(input.Query)
		beforeID = input.BeforeID
	}

	page, err := tc.MemoryMgr.SearchKnowledgeMessages(ctx, memory.KnowledgeMessageQuery{
		ConversationKind: memory.ConversationKindPrivate,
		TargetID:         tc.TargetID,
		ThroughID:        upper.ID,
		BeforeID:         beforeID,
		NewestFirst:      true,
		Text:             keyword,
		Limit:            limit,
	})
	if err != nil {
		return nil, NewTerminalToolError(err)
	}

	out := &SearchPrivateMessagesOutput{Success: true, HasMore: page.HasMore, NextBeforeID: page.NextID, Messages: make([]PrivateMessageHit, 0, len(page.Messages))}
	for _, msg := range page.Messages {
		out.Messages = append(out.Messages, PrivateMessageHit{
			MessageRef: tc.RegisterMessage(msg.OneBotMessageID),
			SenderID:   msg.UserID,
			Nickname:   msg.Nickname,
			Time:       msg.MessageTime.Format("2006-01-02 15:04"),
			Content:    msg.TextContent,
		})
	}
	return out, nil
}

// NewSearchPrivateMessagesTool 创建私聊历史检索工具
func NewSearchPrivateMessagesTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"searchPrivateMessages",
		"按关键词检索当前好友的私聊历史原文；只能看到已经落库的当前私聊消息，不跨会话。",
		searchPrivateMessagesFunc,
	)
}
