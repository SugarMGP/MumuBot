package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"mumu-bot/internal/agentic"
	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/media"
	"mumu-bot/internal/modelprofiles"
	"mumu-bot/internal/tools"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var ErrNoModel = fmt.Errorf("尚未配置模型")

type roleModel struct {
	provider       string
	model          model.Model
	modelName      string
	contextWindow  int
	profileID      uint
	profileVersion int64
	config         model.GenerationConfig
}

// Generation 是一次可替换的场合模型、工具和 MCP 快照
type Generation struct {
	roleModel
	lifeModel    roleModel
	workerModel  roleModel
	groupTools   []tool.Tool
	privateTools []tool.Tool
	workerTools  []tool.Tool
	lifeTools    []tool.Tool
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
	return buildRoleModel(profile, apiKey)
}

func buildRoleModel(profile modelprofiles.Profile, apiKey string) (roleModel, error) {
	client, err := llm.NewClient(llm.ModelConfig{Provider: profile.Provider, APIKey: apiKey, BaseURL: profile.BaseURL, Model: profile.ModelName, ReasoningEffort: profile.ReasoningEffort, ThinkingMode: profile.ThinkingMode, BodyOverride: profile.BodyOverride})
	if err != nil {
		return roleModel{}, err
	}
	return roleModel{
		provider: profile.Provider, model: client, modelName: profile.ModelName, contextWindow: profile.ContextWindow,
		profileID: profile.ID, profileVersion: profile.Version, config: generationConfigFor(profile),
	}, nil
}

// generationConfigFor 把档案里的推理强度映射到各协议的生成参数
func generationConfigFor(profile modelprofiles.Profile) model.GenerationConfig {
	config := model.GenerationConfig{}
	effort := strings.TrimSpace(profile.ReasoningEffort)
	if profile.Provider == agentic.ProviderGemini {
		// Gemini 的预算与等级独立配置，两者都填时原样发送
		config.ThinkingTokens = profile.ThinkingBudget
		if level := strings.TrimSpace(profile.ThinkingLevel); level != "" {
			upper := strings.ToUpper(level)
			config.ThinkingLevel = &upper
		}
		return config
	}
	// Chat 的 reasoning_effort 由客户端 extra 保持原出站形态；Responses 走原生参数
	if effort != "" && profile.Provider == agentic.ProviderResponses {
		config.ReasoningEffort = &effort
	}
	return config
}

// newChatAgent 两种会话共用步数、错误处理与执行约束，工具清单各自独立
func (a *Agent) newChatAgent(generation *Generation, chatTools []tool.Tool) (agent.Agent, error) {
	return a.newTypedChatAgent("chat", generation.roleModel, chatTools), nil
}

// newWorkerAgent 复用共同人格与规则，只挂执行工具和工作摘要
func (a *Agent) newWorkerAgent(generation *Generation, chatTools []tool.Tool) (agent.Agent, error) {
	return a.newTypedChatAgent("worker", generation.workerModel, chatTools), nil
}

// newLifeAgent 使用生活档案与摘要检查点
func (a *Agent) newLifeAgent(generation *Generation, chatTools []tool.Tool) (agent.Agent, error) {
	return a.newTypedChatAgent("life", generation.lifeModel, chatTools), nil
}

func (a *Agent) newTypedChatAgent(name string, selected roleModel, chatTools []tool.Tool) agent.Agent {
	callbacks := a.callbacks()
	return llmagent.New(name,
		llmagent.WithModel(selected.model),
		llmagent.WithTools(chatTools),
		llmagent.WithGenerationConfig(selected.config),
		llmagent.WithEnableParallelTools(false),
		llmagent.WithModelCallbacks(callbacks.modelCallbacks()),
		llmagent.WithToolCallbacks(callbacks.toolCallbacks()),
	)
}

func workerToolBuilders() []func() (tool.Tool, error) {
	return []func() (tool.Tool, error){
		func() (tool.Tool, error) { return tools.NewBashTool() },
		func() (tool.Tool, error) { return tools.NewSearchWebTool() },
		func() (tool.Tool, error) { return tools.NewFetchWebTool() },
		func() (tool.Tool, error) { return tools.NewListSessionsTool() },
		func() (tool.Tool, error) { return tools.NewReadSessionHistoryTool() },
		func() (tool.Tool, error) { return tools.NewReadSessionEntryTool() },
		func() (tool.Tool, error) { return tools.NewReadToolResultTool() },
		func() (tool.Tool, error) { return tools.NewUpdateWorkerTitleTool() },
		func() (tool.Tool, error) { return tools.NewSendSessionMessageTool() },
		func() (tool.Tool, error) { return tools.NewListFilesTool() },
		func() (tool.Tool, error) { return tools.NewReadFileTool() },
		func() (tool.Tool, error) { return tools.NewWriteFileTool() },
		func() (tool.Tool, error) { return tools.NewCreateFileRefTool() },
		func() (tool.Tool, error) { return tools.NewWorkerSaveFileTool() },
		func() (tool.Tool, error) { return tools.NewLifeViewImageTool() },
	}
}

// lifeToolBuilders 是生活主线自己的工具表：没有 QQ 场合的表达工具，扩展 MCP 只挂在这里
func lifeToolBuilders() []func() (tool.Tool, error) {
	return []func() (tool.Tool, error){
		func() (tool.Tool, error) { return tools.NewManageMemoryToolV2() },
		func() (tool.Tool, error) { return tools.NewSearchMemoryToolV2() },
		func() (tool.Tool, error) { return tools.NewSearchMembersTool() },
		func() (tool.Tool, error) { return tools.NewReadMemoryToolV2() },
		func() (tool.Tool, error) { return tools.NewSearchTopicsTool() },
		func() (tool.Tool, error) { return tools.NewReadTopicTool() },
		func() (tool.Tool, error) { return tools.NewSearchHistoryToolV2() },
		func() (tool.Tool, error) { return tools.NewReadHistoryToolV2() },
		func() (tool.Tool, error) { return tools.NewReadToolResultTool() },
		func() (tool.Tool, error) { return tools.NewListSessionsTool() },
		func() (tool.Tool, error) { return tools.NewReadSessionHistoryTool() },
		func() (tool.Tool, error) { return tools.NewReadSessionEntryTool() },
		func() (tool.Tool, error) { return tools.NewSendSessionMessageTool() },
		func() (tool.Tool, error) { return tools.NewGetCurrentStatusTool() },
		func() (tool.Tool, error) { return tools.NewLifeUpdateMoodTool() },
		func() (tool.Tool, error) { return tools.NewSearchWebTool() },
		func() (tool.Tool, error) { return tools.NewFetchWebTool() },
		func() (tool.Tool, error) { return tools.NewLifeViewImageTool() },
		func() (tool.Tool, error) { return tools.NewListFilesTool() },
		func() (tool.Tool, error) { return tools.NewReadFileTool() },
		func() (tool.Tool, error) { return tools.NewWriteFileTool() },
		func() (tool.Tool, error) { return tools.NewSaveFileTool() },
		func() (tool.Tool, error) { return tools.NewCreateFileRefTool() },
		func() (tool.Tool, error) { return tools.NewCancelWorkerTool() },
		func() (tool.Tool, error) { return tools.NewGetUserProfileTool() },
		func() (tool.Tool, error) { return tools.NewSetSelfProfileTool() },
		func() (tool.Tool, error) { return tools.NewListQzonePostsTool() },
		func() (tool.Tool, error) { return tools.NewReadQzonePostTool() },
		func() (tool.Tool, error) { return tools.NewReadQzoneCommentsTool() },
		func() (tool.Tool, error) { return tools.NewPublishQzoneTool() },
		func() (tool.Tool, error) { return tools.NewLikeQzoneTool() },
		func() (tool.Tool, error) { return tools.NewCommentQzoneTool() },
		func() (tool.Tool, error) { return tools.NewReadForwardTool() },
		func() (tool.Tool, error) { return tools.NewPlanNextTool() },
		func() (tool.Tool, error) { return tools.NewManageScheduleTool() },
		func() (tool.Tool, error) { return tools.NewSleepUntilTool() },
		func() (tool.Tool, error) { return tools.NewWakeUpTool() },
		func() (tool.Tool, error) { return tools.NewWriteDiaryTool() },
		func() (tool.Tool, error) { return tools.NewReadDiaryTool() },
		func() (tool.Tool, error) { return tools.NewSearchDiaryTool() },
	}
}

func buildToolLists(mcpManager *mcp.Manager) ([]tool.Tool, []tool.Tool, []tool.Tool, error) {
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

func buildTools(builders []func() (tool.Tool, error)) ([]tool.Tool, error) {
	list := make([]tool.Tool, 0, len(builders))
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
func buildToolsWithMCP(mcpManager *mcp.Manager, builders []func() (tool.Tool, error)) ([]tool.Tool, error) {
	list, err := buildTools(builders)
	if err != nil {
		return nil, err
	}
	if mcpManager != nil {
		list = append(list, mcpManager.GetTools()...)
	}
	return list, nil
}

func sharedToolBuilders(private bool) []func() (tool.Tool, error) {
	speak := func() (tool.Tool, error) { return tools.NewSpeakTool() }
	poke := func() (tool.Tool, error) { return tools.NewPokeTool() }
	if private {
		speak = func() (tool.Tool, error) { return tools.NewPrivateSpeakTool() }
		poke = func() (tool.Tool, error) { return tools.NewPrivatePokeTool() }
	}
	return []func() (tool.Tool, error){
		func() (tool.Tool, error) { return tools.NewManageMemoryToolV2() },
		func() (tool.Tool, error) { return tools.NewSearchMemoryToolV2() },
		func() (tool.Tool, error) { return tools.NewSearchMembersTool() },
		func() (tool.Tool, error) { return tools.NewReadMemoryToolV2() },
		func() (tool.Tool, error) { return tools.NewSearchTopicsTool() },
		func() (tool.Tool, error) { return tools.NewReadTopicTool() },
		func() (tool.Tool, error) { return tools.NewUpdateTopicTool() },
		func() (tool.Tool, error) { return tools.NewSearchHistoryToolV2() },
		func() (tool.Tool, error) { return tools.NewReadHistoryToolV2() },
		func() (tool.Tool, error) { return tools.NewReadToolResultTool() },
		func() (tool.Tool, error) { return tools.NewGetNewMessagesTool() },
		func() (tool.Tool, error) { return tools.NewListSessionsTool() },
		func() (tool.Tool, error) { return tools.NewReadSessionHistoryTool() },
		func() (tool.Tool, error) { return tools.NewReadSessionEntryTool() },
		func() (tool.Tool, error) { return tools.NewSendSessionMessageTool() },
		speak,
		func() (tool.Tool, error) { return tools.NewStayQuietTool() },
		poke,
		func() (tool.Tool, error) { return tools.NewRecallMessageTool() },
		func() (tool.Tool, error) { return tools.NewSearchStickersTool() },
		func() (tool.Tool, error) { return tools.NewSendStickerTool() },
		func() (tool.Tool, error) { return tools.NewSaveStickerTool() },
		func() (tool.Tool, error) { return tools.NewManageStickerTool() },
		func() (tool.Tool, error) { return tools.NewUpdateMoodTool() },
		func() (tool.Tool, error) { return tools.NewSearchMemeTool() },
		func() (tool.Tool, error) { return tools.NewReadMemeTool() },
		func() (tool.Tool, error) { return tools.NewSendImageTool() },
		func() (tool.Tool, error) { return tools.NewViewImageTool() },
		func() (tool.Tool, error) { return tools.NewChatReadFileTool() },
		func() (tool.Tool, error) { return tools.NewChatSendFileTool() },
		func() (tool.Tool, error) { return tools.NewReadDiaryTool() },
		func() (tool.Tool, error) { return tools.NewSearchDiaryTool() },
		func() (tool.Tool, error) { return tools.NewGetCurrentStatusTool() },
		func() (tool.Tool, error) { return tools.NewSearchFacesTool() },
		func() (tool.Tool, error) { return tools.NewReadForwardTool() },
	}
}

func groupToolBuilders() []func() (tool.Tool, error) {
	return append(sharedToolBuilders(false),
		func() (tool.Tool, error) { return tools.NewGetUserProfileTool() },
		func() (tool.Tool, error) { return tools.NewReactToMessageTool() },
		func() (tool.Tool, error) { return tools.NewGetGroupNoticesTool() },
		func() (tool.Tool, error) { return tools.NewGetEssenceMessagesTool() },
		func() (tool.Tool, error) { return tools.NewGetMessageReactionsTool() },
	)
}

func privateToolBuilders() []func() (tool.Tool, error) {
	return append(sharedToolBuilders(true),
		func() (tool.Tool, error) { return tools.NewGetUserProfileTool() },
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
