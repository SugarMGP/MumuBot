package learning

import (
	"context"

	agenttools "mumu-bot/internal/tools"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// 模型与工具循环由框架驱动；每轮调查的模型包装仅负责请求策略
type memoryChatModel struct {
	model.ToolCallingChatModel
	run *investigation
}

func (m *memoryChatModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := m.ToolCallingChatModel.WithTools(infos)
	if err != nil {
		return nil, err
	}
	return &memoryChatModel{ToolCallingChatModel: bound, run: m.run}, nil
}

func (m *memoryChatModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	response, err := m.ToolCallingChatModel.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	if response == nil || len(response.ToolCalls) == 0 {
		return nil, noFinishError()
	}
	m.run.finishAlone = len(response.ToolCalls) == 1 && response.ToolCalls[0].Function.Name == "finishMemoryBatch"
	return response, nil
}

func (r *investigation) newAgent(ctx context.Context, base model.ToolCallingChatModel, maxStep int) (*react.Agent, error) {
	available, err := r.tools()
	if err != nil {
		return nil, err
	}
	return react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: &memoryChatModel{ToolCallingChatModel: base, run: r},
		ToolsConfig:      compose.ToolsNodeConfig{Tools: available, ExecuteSequentially: true, UnknownToolsHandler: agenttools.UnknownToolHandler, ToolCallMiddlewares: []compose.ToolMiddleware{{Invokable: agenttools.ToolErrorMiddleware()}}},
		MaxStep:          maxStep,
	})
}
