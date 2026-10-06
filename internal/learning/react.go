package learning

import (
	"context"

	agenttools "mumu-bot/internal/tools"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// 模型与工具循环由框架驱动；每轮调查的模型包装仅负责请求策略
type memoryChatModel struct {
	model.ToolCallingChatModel
	finishName  string
	finishAlone *bool
}

func (m *memoryChatModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := m.ToolCallingChatModel.WithTools(infos)
	if err != nil {
		return nil, err
	}
	return &memoryChatModel{ToolCallingChatModel: bound, finishName: m.finishName, finishAlone: m.finishAlone}, nil
}

func (m *memoryChatModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	*m.finishAlone = false
	response, err := m.ToolCallingChatModel.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	if response == nil || len(response.ToolCalls) == 0 {
		return nil, noFinishError()
	}
	*m.finishAlone = len(response.ToolCalls) == 1 && response.ToolCalls[0].Function.Name == m.finishName
	return response, nil
}

func (r *groupInvestigation) newAgent(ctx context.Context, base model.ToolCallingChatModel, maxStep int) (*react.Agent, error) {
	available, err := r.tools()
	if err != nil {
		return nil, err
	}
	return newMemoryAgent(ctx, base, available, maxStep, "finishMemoryBatch", &r.finishAlone)
}

func newMemoryAgent(ctx context.Context, base model.ToolCallingChatModel, available []tool.BaseTool, maxStep int, finishName string, finishAlone *bool) (*react.Agent, error) {
	return react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: &memoryChatModel{ToolCallingChatModel: base, finishName: finishName, finishAlone: finishAlone},
		ToolsConfig:      compose.ToolsNodeConfig{Tools: available, ExecuteSequentially: true, UnknownToolsHandler: agenttools.UnknownToolHandler, ToolCallMiddlewares: []compose.ToolMiddleware{{Invokable: agenttools.ToolErrorMiddleware()}}},
		MaxStep:          maxStep,
	})
}
