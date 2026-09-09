package learning

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// 模型与工具循环由框架驱动；每轮调查的模型包装仅负责请求策略
type memoryChatModel struct {
	model.ToolCallingChatModel
}

func (m *memoryChatModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := m.ToolCallingChatModel.WithTools(infos)
	if err != nil {
		return nil, err
	}
	return &memoryChatModel{ToolCallingChatModel: bound}, nil
}

func (m *memoryChatModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	response, err := m.ToolCallingChatModel.Generate(ctx, messages, append(opts, model.WithMaxTokens(8192))...)
	if err != nil {
		return nil, err
	}
	if response == nil || len(response.ToolCalls) == 0 {
		return nil, noFinishError()
	}
	for _, call := range response.ToolCalls {
		if call.Function.Name == "finishMemoryBatch" && len(response.ToolCalls) != 1 {
			return nil, fmt.Errorf("终结提交必须单独调用")
		}
	}
	return response, nil
}
