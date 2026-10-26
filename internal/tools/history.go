package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// SearchHistoryInput 检索历史原文的参数
type SearchHistoryInput struct {
	Query    string `json:"query,omitempty" jsonschema:"description=要搜索的关键词；留空表示按时间倒序取最近原文"`
	PersonID int64  `json:"person_id,omitempty" jsonschema:"description=只看某个人的消息"`
	From     string `json:"from,omitempty" jsonschema:"description=起始时间，格式 2006-01-02 或 2006-01-02 15:04"`
	To       string `json:"to,omitempty" jsonschema:"description=结束时间，格式 2006-01-02 或 2006-01-02 15:04"`
	AfterID  uint   `json:"after_id,omitempty" jsonschema:"description=继续翻页时填上页的 next_id"`
	Limit    int    `json:"limit,omitempty" jsonschema:"description=返回条数，默认8，最多20"`
}

// SearchHistoryOutput 检索历史原文的结果
type SearchHistoryOutput struct {
	Success  bool                `json:"success"`
	HasMore  bool                `json:"has_more"`
	NextID   uint                `json:"next_id,omitempty"`
	Messages []HistoryMessageHit `json:"messages"`
}

// HistoryMessageHit 一条历史原文
type HistoryMessageHit struct {
	MessageID int64  `json:"message_id"`
	Kind      string `json:"conversation_kind"`
	TargetID  int64  `json:"target_id"`
	SenderID  int64  `json:"sender_id"`
	Nickname  string `json:"nickname"`
	Time      string `json:"time"`
	Content   string `json:"content"`
}

// NewSearchHistoryTool 返回检索历史原文与话题摘要的工具
func NewSearchHistoryTool() (tool.InvokableTool, error) {
	return utils.InferTool("searchHistory", "检索历史聊天原文，默认跨群聊与私聊全库搜索。涉及之前聊过的事、对方的说法、约定或时间线时调用；关键词留空表示取最近原文。", searchHistoryFunc)
}

// searchHistoryFunc 检索历史原文
func searchHistoryFunc(ctx context.Context, input *SearchHistoryInput) (*SearchHistoryOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("历史检索能力未初始化"))
	}
	if input == nil {
		input = &SearchHistoryInput{}
	}
	from, err := parseHistoryTime(input.From)
	if err != nil {
		return nil, err
	}
	to, err := parseHistoryTime(input.To)
	if err != nil {
		return nil, err
	}
	page, err := tc.MemoryMgr.SearchConversationHistory(ctx, memory.HistorySearchQuery{
		UserID:      input.PersonID,
		Text:        input.Query,
		From:        from,
		To:          to,
		AfterID:     input.AfterID,
		Limit:       input.Limit,
		NewestFirst: true,
	})
	if err != nil {
		return nil, err
	}
	hits := make([]HistoryMessageHit, 0, len(page.Messages))
	for _, item := range page.Messages {
		hits = append(hits, HistoryMessageHit{
			MessageID: item.OneBotMessageID,
			Kind:      item.ConversationKind,
			TargetID:  item.TargetID,
			SenderID:  item.UserID,
			Nickname:  item.Nickname,
			Time:      item.MessageTime.Format("2006-01-02 15:04"),
			Content:   strings.TrimSpace(item.TextContent),
		})
	}
	return &SearchHistoryOutput{Success: true, HasMore: page.HasMore, NextID: page.NextID, Messages: hits}, nil
}

// parseHistoryTime 解析时间参数
func parseHistoryTime(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return &parsed, nil
		}
	}
	return nil, fmt.Errorf("时间格式无法解析：%s", value)
}

// UpdateMemoryInput 更新知识的参数
type UpdateMemoryInput struct {
	ItemID        uint   `json:"item_id" jsonschema:"description=要更新的知识编号"`
	Content       string `json:"content,omitempty" jsonschema:"description=新的正文，写成完整自然语言命题"`
	Label         string `json:"label,omitempty" jsonschema:"description=新的标题"`
	Kind          string `json:"kind,omitempty" jsonschema:"description=新的类型：fact/preference/constraint/goal/term/expression/alias"`
	SubjectUserID *int64 `json:"subject_user_id,omitempty" jsonschema:"description=新的主体 QQ 号；-1 表示机器人自身，0 表示会话"`
	UpdatedAt     string `json:"updated_at,omitempty" jsonschema:"description=读取时看到的更新时间，用于并发冲突检测"`
}

// UpdateMemoryOutput 更新知识的结果
type UpdateMemoryOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// NewUpdateMemoryTool 返回更新已存知识的工具
func NewUpdateMemoryTool() (tool.InvokableTool, error) {
	return utils.InferTool("updateMemory", "按编号更正或补充一条已存知识。正文改成完整自然语言命题；自身相关的内容用第一人称写。传 updated_at 可检测他人同时修改。", updateMemoryFunc)
}

// updateMemoryFunc 更新知识条目
func updateMemoryFunc(ctx context.Context, input *UpdateMemoryInput) (*UpdateMemoryOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("知识维护能力未初始化"))
	}
	if input == nil || input.ItemID == 0 {
		return nil, fmt.Errorf("请提供 item_id")
	}
	var expected time.Time
	if value := strings.TrimSpace(input.UpdatedAt); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return nil, fmt.Errorf("updated_at 格式无法解析：%s", value)
		}
		expected = parsed
	}
	update := memory.KnowledgeUpdate{ExpectedUpdate: expected}
	if value := strings.TrimSpace(input.Content); value != "" {
		update.Content = &value
	}
	if value := strings.TrimSpace(input.Label); value != "" {
		update.Label = &value
	}
	if value := strings.TrimSpace(input.Kind); value != "" {
		update.Kind = &value
	}
	if input.SubjectUserID != nil {
		update.SubjectUserID = input.SubjectUserID
	}
	if err := tc.MemoryMgr.UpdateKnowledgeItem(ctx, input.ItemID, update); err != nil {
		return nil, err
	}
	return &UpdateMemoryOutput{Success: true, Message: "已更新知识"}, nil
}

// DeleteMemoryInput 删除知识的参数
type DeleteMemoryInput struct {
	ItemID uint `json:"item_id" jsonschema:"description=要删除的知识编号"`
}

// DeleteMemoryOutput 删除知识的结果
type DeleteMemoryOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// NewDeleteMemoryTool 返回删除已存知识的工具
func NewDeleteMemoryTool() (tool.InvokableTool, error) {
	return utils.InferTool("deleteMemory", "按编号删除一条已存知识。确认内容已经过时、重复或不该保留时才调用。", deleteMemoryFunc)
}

// deleteMemoryFunc 删除知识条目
func deleteMemoryFunc(ctx context.Context, input *DeleteMemoryInput) (*DeleteMemoryOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("知识维护能力未初始化"))
	}
	if input == nil || input.ItemID == 0 {
		return nil, fmt.Errorf("请提供 item_id")
	}
	if err := tc.MemoryMgr.DeleteKnowledgeItem(ctx, input.ItemID); err != nil {
		return nil, err
	}
	return &DeleteMemoryOutput{Success: true, Message: "已删除知识"}, nil
}
