package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/media"
	"mumu-bot/internal/modelprofiles"
	"mumu-bot/internal/tools"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

var ErrNoModel = fmt.Errorf("尚未配置模型")

// Generation 是一次可替换的模型、工具、MCP 和 ADK Agent 快照
type Generation struct {
	model         model.AgenticModel
	modelName     string
	contextWindow int
	groupAgent    *adk.TypedChatModelAgent[*schema.AgenticMessage]
	privateAgent  *adk.TypedChatModelAgent[*schema.AgenticMessage]
	groupTools    []tool.BaseTool
	privateTools  []tool.BaseTool
	mcp           *mcp.Manager
	media         *media.Manager
	toolNames     []string

	wg        sync.WaitGroup
	closeOnce sync.Once
}

func (g *Generation) close() {
	if g == nil {
		return
	}
	g.closeOnce.Do(func() {
		if g.mcp != nil {
			g.mcp.Close()
		}
	})
}

// buildGeneration 使用当前数据库模型和 MCP 配置构造完整运行态
func (a *Agent) buildGeneration() (*Generation, error) {
	cfg := config.Snapshot()
	profile, err := a.modelStore.Active(a.ctx)
	if err != nil {
		if err == modelprofiles.ErrNotFound {
			return nil, ErrNoModel
		}
		return nil, err
	}
	apiKey, err := a.modelStore.DecryptKey(profile)
	if err != nil {
		return nil, fmt.Errorf("解密模型密钥失败: %w", err)
	}
	chatModel, err := llm.NewClient(llm.ModelConfig{
		APIKey:          apiKey,
		BaseURL:         profile.BaseURL,
		Model:           profile.ModelName,
		ThinkingType:    profile.ThinkingType,
		ReasoningEffort: profile.ReasoningEffort,
	})
	if err != nil {
		return nil, err
	}

	servers, err := a.mcpStore.EnabledConfigs(a.ctx)
	if err != nil {
		return nil, err
	}
	mcpManager := mcp.NewMCPManager()
	if err := mcpManager.Load(a.ctx, servers); err != nil {
		mcpManager.Close()
		return nil, err
	}

	boot := config.Boot()
	mediaManager := media.New(media.Options{
		Dir:        boot.MediaDir,
		MaxBytesMB: cfg.Media.MaxBytesMB,
		Quality:    float32(cfg.Media.ImageQuality),
		NarrowEdge: cfg.Media.NarrowEdge,
	})
	groupTools, privateTools, toolNames, err := a.buildToolLists(mcpManager)
	if err != nil {
		mcpManager.Close()
		return nil, err
	}
	generation := &Generation{
		model:         chatModel,
		modelName:     profile.ModelName,
		contextWindow: profile.ContextWindow,
		groupTools:    groupTools,
		privateTools:  privateTools,
		mcp:           mcpManager,
		media:         mediaManager,
		toolNames:     toolNames,
	}
	generation.groupAgent, err = a.newChatAgent(generation, groupTools, a.groupGenModelInput)
	if err != nil {
		mcpManager.Close()
		return nil, err
	}
	generation.privateAgent, err = a.newChatAgent(generation, privateTools, a.privateGenModelInput)
	if err != nil {
		mcpManager.Close()
		return nil, err
	}
	return generation, nil
}

// newChatAgent 两种会话共用步数、错误处理与执行约束，工具清单各自独立
func (a *Agent) newChatAgent(generation *Generation, chatTools []tool.BaseTool, genModelInput adk.TypedGenModelInput[*schema.AgenticMessage]) (*adk.TypedChatModelAgent[*schema.AgenticMessage], error) {
	maxStep := config.Snapshot().Agent.MaxStep
	if maxStep <= 0 {
		maxStep = 50
	}
	argumentsHandler, err := tools.NewToolArgumentsHandler(a.ctx, chatTools)
	if err != nil {
		return nil, err
	}
	return adk.NewTypedChatModelAgent[*schema.AgenticMessage](a.ctx, &adk.TypedChatModelAgentConfig[*schema.AgenticMessage]{
		Model:         generation.model,
		GenModelInput: genModelInput,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:                chatTools,
				ExecuteSequentially:  true,
				UnknownToolsHandler:  tools.UnknownToolHandler,
				ToolArgumentsHandler: argumentsHandler,
				ToolCallMiddlewares: []compose.ToolMiddleware{
					{Invokable: tools.ToolErrorMiddleware()},
					tools.ToolTranscriptMiddleware(),
				},
			},
			ReturnDirectly: map[string]bool{"stayQuiet": true},
		},
		MaxIterations: maxStep,
		Handlers: []adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage]{
			&sessionInjector{TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{}, agent: a},
			&sessionPersister{TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{}},
		},
	})
}

func (a *Agent) buildToolLists(mcpManager *mcp.Manager) ([]tool.BaseTool, []tool.BaseTool, []string, error) {
	groupTools, err := buildTools(mcpManager, groupToolBuilders())
	if err != nil {
		return nil, nil, nil, err
	}
	privateTools, err := buildTools(mcpManager, privateToolBuilders())
	if err != nil {
		return nil, nil, nil, err
	}
	seen := map[string]bool{}
	var names []string
	for _, list := range [][]tool.BaseTool{groupTools, privateTools} {
		for _, item := range list {
			info, err := item.Info(a.ctx)
			if err != nil || info == nil {
				continue
			}
			name := strings.TrimSpace(info.Name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	return groupTools, privateTools, names, nil
}

func buildTools(mcpManager *mcp.Manager, builders []func() (tool.BaseTool, error)) ([]tool.BaseTool, error) {
	list := make([]tool.BaseTool, 0, len(builders)+8)
	for _, build := range builders {
		item, err := build()
		if err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	if mcpManager != nil {
		list = append(list, mcpManager.GetTools()...)
	}
	return list, nil
}

func groupToolBuilders() []func() (tool.BaseTool, error) {
	return []func() (tool.BaseTool, error){
		func() (tool.BaseTool, error) { return tools.NewManageMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewSearchMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewReadMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewSearchTopicsTool() },
		func() (tool.BaseTool, error) { return tools.NewReadTopicTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchHistoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewReadHistoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewReadToolResultTool() },
		func() (tool.BaseTool, error) { return tools.NewGetNewMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewListSessionsTool() },
		func() (tool.BaseTool, error) { return tools.NewSendSessionMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewSpeakTool() },
		func() (tool.BaseTool, error) { return tools.NewStayQuietTool() },
		func() (tool.BaseTool, error) { return tools.NewGetGroupMemberDetailTool() },
		func() (tool.BaseTool, error) { return tools.NewPokeTool() },
		func() (tool.BaseTool, error) { return tools.NewReactToMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewRecallMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchStickersTool() },
		func() (tool.BaseTool, error) { return tools.NewSendStickerTool() },
		func() (tool.BaseTool, error) { return tools.NewSaveStickerTool() },
		func() (tool.BaseTool, error) { return tools.NewGetGroupNoticesTool() },
		func() (tool.BaseTool, error) { return tools.NewGetEssenceMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewGetMessageReactionsTool() },
		func() (tool.BaseTool, error) { return tools.NewUpdateMoodTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchWebTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchMemeTool() },
		func() (tool.BaseTool, error) { return tools.NewFetchWebTool() },
		func() (tool.BaseTool, error) { return tools.NewSendImageTool() },
		func() (tool.BaseTool, error) { return tools.NewViewImageTool() },
		func() (tool.BaseTool, error) { return tools.NewGetCurrentStatusTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchFacesTool() },
		func() (tool.BaseTool, error) { return tools.NewReadForwardTool() },
	}
}

func privateToolBuilders() []func() (tool.BaseTool, error) {
	return []func() (tool.BaseTool, error){
		func() (tool.BaseTool, error) { return tools.NewManageMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewSearchMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewReadMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewSearchTopicsTool() },
		func() (tool.BaseTool, error) { return tools.NewReadTopicTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchHistoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewReadHistoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewReadToolResultTool() },
		func() (tool.BaseTool, error) { return tools.NewGetNewMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewListSessionsTool() },
		func() (tool.BaseTool, error) { return tools.NewSendSessionMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewPrivateSpeakTool() },
		func() (tool.BaseTool, error) { return tools.NewStayQuietTool() },
		func() (tool.BaseTool, error) { return tools.NewPrivatePokeTool() },
		func() (tool.BaseTool, error) { return tools.NewRecallMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchStickersTool() },
		func() (tool.BaseTool, error) { return tools.NewSendStickerTool() },
		func() (tool.BaseTool, error) { return tools.NewSaveStickerTool() },
		func() (tool.BaseTool, error) { return tools.NewUpdateMoodTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchWebTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchMemeTool() },
		func() (tool.BaseTool, error) { return tools.NewFetchWebTool() },
		func() (tool.BaseTool, error) { return tools.NewSendImageTool() },
		func() (tool.BaseTool, error) { return tools.NewViewImageTool() },
		func() (tool.BaseTool, error) { return tools.NewGetCurrentStatusTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchFacesTool() },
		func() (tool.BaseTool, error) { return tools.NewReadForwardTool() },
	}
}

func generationFrom(ctx context.Context) *Generation {
	generation, _ := ctx.Value(generationKey{}).(*Generation)
	return generation
}

type generationKey struct{}

func withGeneration(ctx context.Context, generation *Generation) context.Context {
	return context.WithValue(ctx, generationKey{}, generation)
}

func (a *Agent) contextWindow() int {
	if generation := a.currentGeneration(); generation != nil {
		return generation.contextWindow
	}
	return 0
}
