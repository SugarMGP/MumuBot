package learning

import (
	"context"

	agenttools "mumu-bot/internal/tools"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// memoryChatModel 包装 agentic 模型，记录结束工具是否被单独调用
type memoryChatModel struct {
	model.AgenticModel
	finishName  string
	finishAlone *bool
}

// Generate 检查本轮是否调用了工具，没调用时视为未完成
func (m *memoryChatModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	*m.finishAlone = false
	response, err := m.AgenticModel.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	calls := functionToolCallNames(response)
	if len(calls) == 0 {
		return nil, noFinishError()
	}
	*m.finishAlone = len(calls) == 1 && calls[0] == m.finishName
	return response, nil
}

// functionToolCallNames 取出模型输出的工具调用名
func functionToolCallNames(message *schema.AgenticMessage) []string {
	if message == nil {
		return nil
	}
	names := make([]string, 0, 2)
	for _, block := range message.ContentBlocks {
		if block != nil && block.FunctionToolCall != nil {
			names = append(names, block.FunctionToolCall.Name)
		}
	}
	return names
}

// webTools 提供外部资料查询工具，仅辅助理解语义，不作为证据
func webTools() ([]tool.BaseTool, error) {
	searchWeb, err := agenttools.NewSearchWebTool()
	if err != nil {
		return nil, err
	}
	searchMeme, err := agenttools.NewSearchMemeTool()
	if err != nil {
		return nil, err
	}
	fetchWeb, err := agenttools.NewFetchWebTool()
	if err != nil {
		return nil, err
	}
	return []tool.BaseTool{searchWeb, searchMeme, fetchWeb}, nil
}

// runUntilFinish 驱动调查直到合法提交；运行报错但已提交时仍视为完成
func runUntilFinish(ctx context.Context, agent *adk.TypedChatModelAgent[*schema.AgenticMessage], messages []*schema.AgenticMessage, finished *bool) error {
	runner := adk.NewTypedRunner[*schema.AgenticMessage](adk.TypedRunnerConfig[*schema.AgenticMessage]{Agent: agent})
	iter := runner.Run(ctx, messages)
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			if *finished {
				return nil
			}
			return event.Err
		}
	}
	if !*finished {
		return noFinishError()
	}
	return nil
}

func (r *groupInvestigation) newAgent(ctx context.Context, base model.AgenticModel, maxStep int) (*adk.TypedChatModelAgent[*schema.AgenticMessage], error) {
	available, err := r.tools()
	if err != nil {
		return nil, err
	}
	return newMemoryAgent(ctx, base, available, maxStep, "finishMemoryBatch", &r.finishAlone)
}

func newMemoryAgent(ctx context.Context, base model.AgenticModel, available []tool.BaseTool, maxStep int, finishName string, finishAlone *bool) (*adk.TypedChatModelAgent[*schema.AgenticMessage], error) {
	return adk.NewTypedChatModelAgent[*schema.AgenticMessage](ctx, &adk.TypedChatModelAgentConfig[*schema.AgenticMessage]{
		Model: &memoryChatModel{AgenticModel: base, finishName: finishName, finishAlone: finishAlone},
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               available,
				ExecuteSequentially: true,
				UnknownToolsHandler: agenttools.UnknownToolHandler,
				ToolCallMiddlewares: []compose.ToolMiddleware{{Invokable: agenttools.ToolErrorMiddleware()}},
			},
			ReturnDirectly: map[string]bool{finishName: true},
		},
		MaxIterations: maxStep,
	})
}
