package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ==================== 读取合并转发工具 ====================

// ReadForwardInput 读取合并转发内容的参数
type ReadForwardInput struct {
	ID    string `json:"id" jsonschema:"description=合并转发句柄，来自消息里的合并转发占位"`
	Index int    `json:"index,omitempty" jsonschema:"description=要看的第几条消息，从 0 开始；留空先看这份转发里有哪些消息"`
}

// ForwardMessageSummary 转发里一条消息的概览
type ForwardMessageSummary struct {
	Index   int    `json:"index"`
	Sender  string `json:"sender,omitempty"`
	Preview string `json:"preview"`
	Nested  string `json:"nested_forward,omitempty"`
}

// ReadForwardOutput 读取结果
type ReadForwardOutput struct {
	Success  bool                    `json:"success"`
	ID       string                  `json:"id,omitempty"`
	Count    int                     `json:"count,omitempty"`
	Messages []ForwardMessageSummary `json:"messages,omitempty"`
	Index    int                     `json:"index,omitempty"`
	Sender   string                  `json:"sender,omitempty"`
	Content  string                  `json:"content,omitempty"`
	Nested   string                  `json:"nested_forward,omitempty"`
	Message  string                  `json:"message,omitempty"`
}

func readForwardFunc(ctx context.Context, input *ReadForwardInput) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.ReadForwardCallback == nil {
		return nil, NewTerminalToolError(fmt.Errorf("合并转发读取能力未初始化"))
	}
	if input == nil || strings.TrimSpace(input.ID) == "" {
		return nil, fmt.Errorf("请提供合并转发句柄")
	}
	return tc.ReadForwardCallback(ctx, strings.TrimSpace(input.ID), input.Index)
}

// NewReadForwardTool 返回逐层翻阅合并转发内容的工具
func NewReadForwardTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"readForward",
		"查看合并转发里的内容。消息里的合并转发占位带着句柄，先用 readForward 列出这份转发里有哪些消息，再带上 index 看某一条的具体内容；转发里如果还有嵌套转发，会给出新的句柄，可以继续往下翻。",
		readForwardFunc,
	)
}
