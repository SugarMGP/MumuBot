package agent

import (
	"context"
	"fmt"
	"sync"

	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/media"
	"mumu-bot/internal/tools"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

var ErrNoModel = fmt.Errorf("尚未配置模型")

type roleModel struct {
	model          model.AgenticModel
	modelName      string
	contextWindow  int
	profileID      uint
	profileVersion int64
}

// Generation 是一次可替换的场合模型、工具和 MCP 快照
type Generation struct {
	roleModel
	lifeModel    roleModel
	workerModel  roleModel
	groupTools   []tool.BaseTool
	privateTools []tool.BaseTool
	workerTools  []tool.BaseTool
	lifeTools    []tool.BaseTool
	mcp          *mcp.Manager
	media        *media.Manager

	wg        sync.WaitGroup
	closeOnce sync.Once
}

func (g *Generation) forKind(kind string) roleModel {
	switch kind {
	case "life":
		return g.lifeModel
	case "worker":
		return g.workerModel
	default:
		return g.roleModel
	}
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
	selection, err := a.modelStore.Selection(a.ctx)
	if err != nil {
		return nil, err
	}
	loaded := map[uint]roleModel{}
	load := func(id uint) (roleModel, error) {
		if existing, ok := loaded[id]; ok {
			return existing, nil
		}
		item, err := a.loadModel(a.ctx, id)
		if err != nil {
			return roleModel{}, err
		}
		loaded[id] = item
		return item, nil
	}
	qqModel, err := load(selection.QQ)
	if err != nil {
		return nil, err
	}
	lifeModel, err := load(selection.Life)
	if err != nil {
		return nil, err
	}
	workerModel, err := load(selection.Worker)
	if err != nil {
		return nil, err
	}

	servers, err := a.mcpStore.EnabledConfigs(a.ctx)
	if err != nil {
		return nil, err
	}
	mcpManager := mcp.NewMCPManager(a.memory.RefStore())
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
	groupTools, privateTools, lifeTools, err := buildToolLists(mcpManager)
	if err != nil {
		mcpManager.Close()
		return nil, err
	}
	workerTools, err := buildTools(workerToolBuilders())
	if err != nil {
		mcpManager.Close()
		return nil, err
	}
	generation := &Generation{
		roleModel: qqModel, lifeModel: lifeModel, workerModel: workerModel,
		groupTools:   groupTools,
		privateTools: privateTools,
		workerTools:  workerTools,
		lifeTools:    lifeTools,
		mcp:          mcpManager,
		media:        mediaManager,
	}
	return generation, nil
}

func (a *Agent) loadModel(ctx context.Context, id uint) (roleModel, error) {
	if id == 0 {
		return roleModel{}, ErrNoModel
	}
	profile, err := a.modelStore.Get(ctx, id)
	if err != nil {
		return roleModel{}, err
	}
	apiKey, err := a.modelStore.DecryptKey(profile)
	if err != nil {
		return roleModel{}, err
	}
	client, err := llm.NewClient(llm.ModelConfig{APIKey: apiKey, BaseURL: profile.BaseURL, Model: profile.ModelName, ReasoningEffort: profile.ReasoningEffort})
	if err != nil {
		return roleModel{}, err
	}
	return roleModel{model: client, modelName: profile.ModelName, contextWindow: profile.ContextWindow, profileID: id, profileVersion: profile.Version}, nil
}

// newChatAgent 两种会话共用步数、错误处理与执行约束，工具清单各自独立
func (a *Agent) newChatAgent(generation *Generation, chatTools []tool.BaseTool, maxStep int) (*adk.TypedChatModelAgent[*schema.AgenticMessage], error) {
	if maxStep <= 0 {
		return nil, fmt.Errorf("聊天单轮步数无效")
	}
	handlers := []adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage]{
		&sessionInjector{TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{}, agent: a},
		&sessionPersister{TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{}},
	}
	return a.newTypedChatAgent(generation.roleModel, chatTools, maxStep, handlers, map[string]bool{"stayQuiet": true})
}

// newWorkerAgent 复用共同人格与规则，只挂执行工具和工作摘要，不接 QQ 聊天职责/压缩器
func (a *Agent) newWorkerAgent(ctx context.Context, generation *Generation, chatTools []tool.BaseTool, maxStep int) (*adk.TypedChatModelAgent[*schema.AgenticMessage], error) {
	if maxStep <= 0 {
		return nil, fmt.Errorf("工作单轮步数无效")
	}
	summarizer, err := a.newWorkerSummarizer(ctx, generation)
	if err != nil {
		return nil, err
	}
	handlers := make([]adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage], 0, 3)
	handlers = append(handlers,
		&sessionInjector{TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{}, agent: a},
	)
	if summarizer != nil {
		handlers = append(handlers, summarizer)
	}
	handlers = append(handlers,
		&sessionPersister{TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{}},
	)
	return a.newTypedChatAgent(generation.workerModel, chatTools, maxStep, handlers, nil)
}

// newLifeAgent 使用生活档案和摘要中间件；睡眠成功后直接收尾
func (a *Agent) newLifeAgent(ctx context.Context, generation *Generation, chatTools []tool.BaseTool, maxStep int) (*adk.TypedChatModelAgent[*schema.AgenticMessage], error) {
	if maxStep <= 0 {
		return nil, fmt.Errorf("生活单轮步数无效")
	}
	summarizer, err := a.newLifeSummarizer(ctx, generation)
	if err != nil {
		return nil, err
	}
	handlers := make([]adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage], 0, 3)
	handlers = append(handlers,
		&sessionInjector{TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{}, agent: a},
	)
	if summarizer != nil {
		handlers = append(handlers, summarizer)
	}
	handlers = append(handlers,
		&sessionPersister{TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{}},
	)
	return a.newTypedChatAgent(generation.lifeModel, chatTools, maxStep, handlers, nil)
}

func (a *Agent) newTypedChatAgent(selected roleModel, chatTools []tool.BaseTool, maxStep int, handlers []adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage], returnDirectly map[string]bool) (*adk.TypedChatModelAgent[*schema.AgenticMessage], error) {
	argumentsHandler, err := tools.NewToolArgumentsHandler(a.ctx, chatTools)
	if err != nil {
		return nil, err
	}
	return adk.NewTypedChatModelAgent[*schema.AgenticMessage](a.ctx, &adk.TypedChatModelAgentConfig[*schema.AgenticMessage]{
		Model:         selected.model,
		GenModelInput: a.passthroughModelInput,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:                chatTools,
				ExecuteSequentially:  true,
				UnknownToolsHandler:  tools.UnknownToolHandler,
				ToolArgumentsHandler: argumentsHandler,
				ToolCallMiddlewares: []compose.ToolMiddleware{
					tools.ToolTranscriptMiddleware(),
					tools.ToolLogMiddleware(),
					tools.ToolErrorMiddleware(),
				},
			},
			ReturnDirectly: returnDirectly,
		},
		MaxIterations: maxStep,
		Handlers:      handlers,
	})
}

func workerToolBuilders() []func() (tool.BaseTool, error) {
	return []func() (tool.BaseTool, error){
		func() (tool.BaseTool, error) { return tools.NewManageMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewSearchMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewReadMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewBashTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchWebTool() },
		func() (tool.BaseTool, error) { return tools.NewFetchWebTool() },
		func() (tool.BaseTool, error) { return tools.NewListSessionsTool() },
		func() (tool.BaseTool, error) { return tools.NewReadSessionHistoryTool() },
		func() (tool.BaseTool, error) { return tools.NewReadSessionEntryTool() },
		func() (tool.BaseTool, error) { return tools.NewReadToolResultTool() },
		func() (tool.BaseTool, error) { return tools.NewUpdateWorkerTitleTool() },
		func() (tool.BaseTool, error) { return tools.NewSendSessionMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewListFilesTool() },
		func() (tool.BaseTool, error) { return tools.NewReadFileTool() },
		func() (tool.BaseTool, error) { return tools.NewWriteFileTool() },
		func() (tool.BaseTool, error) { return tools.NewCreateFileRefTool() },
		func() (tool.BaseTool, error) { return tools.NewWorkerSaveFileTool() },
		func() (tool.BaseTool, error) { return tools.NewLifeViewImageTool() },
	}
}

// lifeToolBuilders 是生活主线自己的工具表：没有 QQ 场合的表达工具，扩展 MCP 只挂在这里
func lifeToolBuilders() []func() (tool.BaseTool, error) {
	return []func() (tool.BaseTool, error){
		func() (tool.BaseTool, error) { return tools.NewManageMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewSearchMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewSearchMembersTool() },
		func() (tool.BaseTool, error) { return tools.NewReadMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewSearchTopicsTool() },
		func() (tool.BaseTool, error) { return tools.NewReadTopicTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchHistoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewReadHistoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewReadToolResultTool() },
		func() (tool.BaseTool, error) { return tools.NewListSessionsTool() },
		func() (tool.BaseTool, error) { return tools.NewReadSessionHistoryTool() },
		func() (tool.BaseTool, error) { return tools.NewReadSessionEntryTool() },
		func() (tool.BaseTool, error) { return tools.NewSendSessionMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewGetCurrentStatusTool() },
		func() (tool.BaseTool, error) { return tools.NewLifeUpdateMoodTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchWebTool() },
		func() (tool.BaseTool, error) { return tools.NewFetchWebTool() },
		func() (tool.BaseTool, error) { return tools.NewLifeViewImageTool() },
		func() (tool.BaseTool, error) { return tools.NewListFilesTool() },
		func() (tool.BaseTool, error) { return tools.NewReadFileTool() },
		func() (tool.BaseTool, error) { return tools.NewWriteFileTool() },
		func() (tool.BaseTool, error) { return tools.NewSaveFileTool() },
		func() (tool.BaseTool, error) { return tools.NewCreateFileRefTool() },
		func() (tool.BaseTool, error) { return tools.NewCancelWorkerTool() },
		func() (tool.BaseTool, error) { return tools.NewGetUserProfileTool() },
		func() (tool.BaseTool, error) { return tools.NewSetSelfProfileTool() },
		func() (tool.BaseTool, error) { return tools.NewListQzonePostsTool() },
		func() (tool.BaseTool, error) { return tools.NewReadQzonePostTool() },
		func() (tool.BaseTool, error) { return tools.NewPublishQzoneTool() },
		func() (tool.BaseTool, error) { return tools.NewLikeQzoneTool() },
		func() (tool.BaseTool, error) { return tools.NewCommentQzoneTool() },
		func() (tool.BaseTool, error) { return tools.NewReadForwardTool() },
		func() (tool.BaseTool, error) { return tools.NewPlanNextTool() },
		func() (tool.BaseTool, error) { return tools.NewManageScheduleTool() },
		func() (tool.BaseTool, error) { return tools.NewSleepUntilTool() },
		func() (tool.BaseTool, error) { return tools.NewWakeUpTool() },
		func() (tool.BaseTool, error) { return tools.NewWriteDiaryTool() },
		func() (tool.BaseTool, error) { return tools.NewReadDiaryTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchDiaryTool() },
	}
}

func buildToolLists(mcpManager *mcp.Manager) ([]tool.BaseTool, []tool.BaseTool, []tool.BaseTool, error) {
	groupTools, err := buildTools(groupToolBuilders())
	if err != nil {
		return nil, nil, nil, err
	}
	privateTools, err := buildTools(privateToolBuilders())
	if err != nil {
		return nil, nil, nil, err
	}
	lifeTools, err := buildToolsWithMCP(mcpManager, lifeToolBuilders())
	if err != nil {
		return nil, nil, nil, err
	}
	return groupTools, privateTools, lifeTools, nil
}

func buildTools(builders []func() (tool.BaseTool, error)) ([]tool.BaseTool, error) {
	list := make([]tool.BaseTool, 0, len(builders))
	for _, build := range builders {
		item, err := build()
		if err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	return list, nil
}

// buildToolsWithMCP 只给生活主线装配扩展 MCP 工具
func buildToolsWithMCP(mcpManager *mcp.Manager, builders []func() (tool.BaseTool, error)) ([]tool.BaseTool, error) {
	list, err := buildTools(builders)
	if err != nil {
		return nil, err
	}
	if mcpManager != nil {
		list = append(list, mcpManager.GetTools()...)
	}
	return list, nil
}

func sharedToolBuilders(private bool) []func() (tool.BaseTool, error) {
	speak := func() (tool.BaseTool, error) { return tools.NewSpeakTool() }
	poke := func() (tool.BaseTool, error) { return tools.NewPokeTool() }
	if private {
		speak = func() (tool.BaseTool, error) { return tools.NewPrivateSpeakTool() }
		poke = func() (tool.BaseTool, error) { return tools.NewPrivatePokeTool() }
	}
	return []func() (tool.BaseTool, error){
		func() (tool.BaseTool, error) { return tools.NewManageMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewSearchMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewSearchMembersTool() },
		func() (tool.BaseTool, error) { return tools.NewReadMemoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewSearchTopicsTool() },
		func() (tool.BaseTool, error) { return tools.NewReadTopicTool() },
		func() (tool.BaseTool, error) { return tools.NewUpdateTopicTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchHistoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewReadHistoryToolV2() },
		func() (tool.BaseTool, error) { return tools.NewReadToolResultTool() },
		func() (tool.BaseTool, error) { return tools.NewGetNewMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewListSessionsTool() },
		func() (tool.BaseTool, error) { return tools.NewReadSessionHistoryTool() },
		func() (tool.BaseTool, error) { return tools.NewReadSessionEntryTool() },
		func() (tool.BaseTool, error) { return tools.NewSendSessionMessageTool() },
		speak,
		func() (tool.BaseTool, error) { return tools.NewStayQuietTool() },
		poke,
		func() (tool.BaseTool, error) { return tools.NewRecallMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchStickersTool() },
		func() (tool.BaseTool, error) { return tools.NewSendStickerTool() },
		func() (tool.BaseTool, error) { return tools.NewSaveStickerTool() },
		func() (tool.BaseTool, error) { return tools.NewManageStickerTool() },
		func() (tool.BaseTool, error) { return tools.NewUpdateMoodTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchMemeTool() },
		func() (tool.BaseTool, error) { return tools.NewReadMemeTool() },
		func() (tool.BaseTool, error) { return tools.NewSendImageTool() },
		func() (tool.BaseTool, error) { return tools.NewViewImageTool() },
		func() (tool.BaseTool, error) { return tools.NewChatReadFileTool() },
		func() (tool.BaseTool, error) { return tools.NewChatSendFileTool() },
		func() (tool.BaseTool, error) { return tools.NewReadDiaryTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchDiaryTool() },
		func() (tool.BaseTool, error) { return tools.NewGetCurrentStatusTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchFacesTool() },
		func() (tool.BaseTool, error) { return tools.NewReadForwardTool() },
	}
}

func groupToolBuilders() []func() (tool.BaseTool, error) {
	return append(sharedToolBuilders(false),
		func() (tool.BaseTool, error) { return tools.NewGetUserProfileTool() },
		func() (tool.BaseTool, error) { return tools.NewReactToMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewGetGroupNoticesTool() },
		func() (tool.BaseTool, error) { return tools.NewGetEssenceMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewGetMessageReactionsTool() },
	)
}

func privateToolBuilders() []func() (tool.BaseTool, error) {
	return append(sharedToolBuilders(true),
		func() (tool.BaseTool, error) { return tools.NewGetUserProfileTool() },
	)
}

func generationFrom(ctx context.Context) *Generation {
	generation, _ := ctx.Value(generationKey{}).(*Generation)
	return generation
}

type generationKey struct{}

func withGeneration(ctx context.Context, generation *Generation) context.Context {
	return context.WithValue(ctx, generationKey{}, generation)
}
