package tools

import (
	"context"
	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ==================== 保存黑话工具 ====================

// SaveJargonInput 保存黑话的输入参数
type SaveJargonInput struct {
	// Content 黑话/术语/梗的内容
	Content string `json:"content" jsonschema:"description=黑话、术语或梗的原文"`
	// Meaning 含义解释
	Meaning string `json:"meaning" jsonschema:"description=这个黑话/术语的含义或解释"`
	// Context 使用场景或上下文
	Context string `json:"context,omitempty" jsonschema:"description=在什么情况下使用，或者来源背景"`
}

// SaveJargonOutput 保存黑话的输出
type SaveJargonOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// saveJargonFunc 保存黑话的实际实现
func saveJargonFunc(ctx context.Context, input *SaveJargonInput) (*SaveJargonOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &SaveJargonOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.Content == "" {
		return &SaveJargonOutput{Success: false, Message: "黑话内容不能为空"}, nil
	}
	if input.Meaning == "" {
		return &SaveJargonOutput{Success: false, Message: "黑话含义不能为空"}, nil
	}

	jargon := &memory.Jargon{
		GroupID: tc.GroupID,
		Content: input.Content,
		Meaning: input.Meaning,
		Context: input.Context,
	}

	if err := tc.MemoryMgr.SaveJargon(jargon); err != nil {
		output := &SaveJargonOutput{Success: false, Message: err.Error()}
		LogToolCall("saveJargon", input, output, err)
		return output, nil
	}

	output := &SaveJargonOutput{Success: true, Message: "已记住这个黑话"}
	LogToolCall("saveJargon", input, output, nil)
	return output, nil
}

// NewSaveJargonTool 创建保存黑话工具
func NewSaveJargonTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"saveJargon",
		`保存群里的黑话、术语或梗。当你发现群友使用了你不懂的词汇，并且从上下文理解了它的含义时，可以保存下来。`,
		saveJargonFunc,
	)
}

// ==================== 搜索黑话工具 ====================

// SearchJargonInput 搜索黑话的输入参数
type SearchJargonInput struct {
	// Keyword 搜索关键词
	Keyword string `json:"keyword" jsonschema:"description=搜索关键词，可以是多个词用空格分隔"`
	// Limit 返回结果数量限制，默认10
	Limit int `json:"limit,omitempty" jsonschema:"description=返回结果数量限制，默认10"`
}

// SearchJargonOutput 搜索黑话的输出
type SearchJargonOutput struct {
	Success bool                     `json:"success"`
	Count   int                      `json:"count"`
	Jargons []map[string]interface{} `json:"jargons,omitempty"`
	Message string                   `json:"message,omitempty"`
}

// searchJargonFunc 搜索黑话的实际实现
func searchJargonFunc(ctx context.Context, input *SearchJargonInput) (*SearchJargonOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &SearchJargonOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.Keyword == "" {
		return &SearchJargonOutput{Success: false, Message: "搜索关键词不能为空"}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}

	jargons, err := tc.MemoryMgr.SearchJargons(tc.GroupID, input.Keyword, limit)
	if err != nil {
		output := &SearchJargonOutput{Success: false, Message: err.Error()}
		LogToolCall("searchJargon", input, output, err)
		return output, nil
	}

	results := make([]map[string]interface{}, 0, len(jargons))
	for _, j := range jargons {
		results = append(results, map[string]interface{}{
			"id":       j.ID,
			"content":  j.Content,
			"meaning":  j.Meaning,
			"context":  j.Context,
			"verified": j.Verified,
		})
	}

	output := &SearchJargonOutput{
		Success: true,
		Count:   len(results),
		Jargons: results,
	}
	LogToolCall("searchJargon", input, output, nil)
	return output, nil
}

// NewSearchJargonTool 创建搜索黑话工具
func NewSearchJargonTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"searchJargon",
		`搜索已保存的黑话、术语或梗（优先搜索来源于本群的）。`,
		searchJargonFunc,
	)
}

// ==================== 获取待审核黑话工具 ====================

type GetUnverifiedJargonsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=返回数量，默认5"`
}

type UnverifiedJargonItem struct {
	ID      uint   `json:"id"`
	Content string `json:"content"`
	Meaning string `json:"meaning"`
	Context string `json:"context"`
}

type GetUnverifiedJargonsOutput struct {
	Success bool                   `json:"success"`
	Jargons []UnverifiedJargonItem `json:"jargons,omitempty"`
	Message string                 `json:"message,omitempty"`
}

func getUnverifiedJargonsFunc(ctx context.Context, input *GetUnverifiedJargonsInput) (*GetUnverifiedJargonsOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &GetUnverifiedJargonsOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 5
	}

	jargons, err := tc.MemoryMgr.GetUnverifiedJargons(tc.GroupID, limit)
	if err != nil {
		output := &GetUnverifiedJargonsOutput{Success: false, Message: err.Error()}
		LogToolCall("getUnverifiedJargons", input, output, err)
		return output, nil
	}

	results := make([]UnverifiedJargonItem, 0, len(jargons))
	for _, j := range jargons {
		results = append(results, UnverifiedJargonItem{
			ID:      j.ID,
			Content: j.Content,
			Meaning: j.Meaning,
			Context: j.Context,
		})
	}

	output := &GetUnverifiedJargonsOutput{Success: true, Jargons: results}
	LogToolCall("getUnverifiedJargons", input, output, nil)
	return output, nil
}

func NewGetUnverifiedJargonsTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getUnverifiedJargons",
		"查看待审核的黑话/术语。你可以检查这些黑话的含义是否准确。",
		getUnverifiedJargonsFunc,
	)
}

// ==================== 审核黑话工具 ====================

type ReviewJargonInput struct {
	ID      uint `json:"id" jsonschema:"description=黑话ID"`
	Approve bool `json:"approve" jsonschema:"description=是否通过审核，true=通过，false=拒绝"`
}

type ReviewJargonOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func reviewJargonFunc(ctx context.Context, input *ReviewJargonInput) (*ReviewJargonOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &ReviewJargonOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.ID == 0 {
		return &ReviewJargonOutput{Success: false, Message: "黑话 ID 不能为空"}, nil
	}

	err := tc.MemoryMgr.ReviewJargon(input.ID, input.Approve)
	if err != nil {
		output := &ReviewJargonOutput{Success: false, Message: err.Error()}
		LogToolCall("reviewJargon", input, output, err)
		return output, nil
	}

	msg := "已拒绝该黑话"
	if input.Approve {
		msg = "已验证该黑话"
	}
	output := &ReviewJargonOutput{Success: true, Message: msg}
	LogToolCall("reviewJargon", input, output, nil)
	return output, nil
}

func NewReviewJargonTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"reviewJargon",
		"审核一条黑话/术语。如果含义正确，可以通过验证；如果有误，可以拒绝。",
		reviewJargonFunc,
	)
}
