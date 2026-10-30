package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/persona"
	"mumu-bot/internal/tools"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
)

const (
	replyCacheTTL      = 30 * time.Minute
	replyCacheCapacity = 1024
	// restoreMessageLimit 重启后从消息库恢复的会话消息条数
	restoreMessageLimit = 50
)

type Agent struct {
	ctx              context.Context
	cancel           context.CancelFunc
	persona          *persona.Persona
	memory           *memory.Manager
	model            model.AgenticModel
	bot              *onebot.Client
	groupAgent       *adk.TypedChatModelAgent[*schema.AgenticMessage]
	privateAgent     *adk.TypedChatModelAgent[*schema.AgenticMessage]
	groupTools       []tool.BaseTool
	privateTools     []tool.BaseTool
	mcpMgr           *mcp.Manager
	groupConcurrency *GroupThinkConcurrency
	groupCommits     *commitQueueSet[commitItem]
	privateCommits   *commitQueueSet[commitItem]
	sessions         *sessionStore
	media            *media.Manager
	gates            *gateStore

	toolNames []string

	replyCache *ttlcache.Cache[string, onebot.ReplyInfo]

	recallMu       sync.Mutex
	pendingRecalls map[recallCommit]pendingRecall

	pendingGroupThinks map[int64]*pendingThink
	pendingGroupMu     sync.Mutex

	pendingPrivateThinks map[int64]*pendingThink
	privateMu            sync.Mutex
	privateRunning       map[int64]bool
	privateStopped       bool
	thinkWG              sync.WaitGroup

	stopping atomic.Bool
	wg       sync.WaitGroup
}

type pendingThink struct {
	timer      *time.Timer
	generation uint64
}

func New(mem *memory.Manager, botClient *onebot.Client) (*Agent, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, fmt.Errorf("配置未加载")
	}
	if botClient == nil || botClient.GetSelfID() <= 0 {
		return nil, fmt.Errorf("OneBot 尚未取得登录账号")
	}

	p, err := persona.NewPersona(&cfg.Persona)
	if err != nil {
		return nil, fmt.Errorf("加载人格失败: %w", err)
	}

	chatModel, err := llm.NewClient()
	if err != nil {
		return nil, fmt.Errorf("创建 LLM 客户端失败: %w", err)
	}
	chatModel = newTimedAgenticModel(chatModel, time.Duration(cfg.Agent.ThinkTimeoutSeconds)*time.Second)

	rootCtx, cancel := context.WithCancel(context.Background())
	a := &Agent{
		ctx:                  rootCtx,
		cancel:               cancel,
		persona:              p,
		memory:               mem,
		model:                chatModel,
		bot:                  botClient,
		sessions:             newSessionStore(mem),
		gates:                newGateStore(),
		pendingGroupThinks:   make(map[int64]*pendingThink),
		pendingPrivateThinks: make(map[int64]*pendingThink),
		privateRunning:       make(map[int64]bool),
		pendingRecalls:       make(map[recallCommit]pendingRecall),
		replyCache:           newAgentTTLCache[string, onebot.ReplyInfo](replyCacheCapacity, replyCacheTTL),
		media: media.New(media.Options{
			Dir:        cfg.Media.StoragePath,
			MaxBytesMB: cfg.Media.MaxBytesMB,
			Quality:    float32(cfg.Media.ImageQuality),
			NarrowEdge: cfg.Media.NarrowEdge,
		}),
	}
	a.groupCommits = newCommitQueueSet(a.commitOne, func(targetID int64) zap.Field { return zap.Int64("group_id", targetID) })
	a.privateCommits = newCommitQueueSet(a.commitOne, func(targetID int64) zap.Field { return zap.Int64("target_id", targetID) })
	constructed := false
	defer func() {
		if constructed {
			return
		}
		a.shutdown()
	}()

	zap.L().Info("人格已加载", zap.String("name", a.persona.GetName()))

	a.groupConcurrency = NewGroupThinkConcurrency(a.ctx, cfg.Agent.MaxCoroutine, a.thinkGroup)

	a.mcpMgr = mcp.NewMCPManager()
	if err := a.mcpMgr.LoadFromConfig(a.ctx, "config/mcp.json"); err != nil {
		zap.L().Error("加载 MCP 配置失败", zap.Error(err))
	}

	if err := a.initTools(); err != nil {
		return nil, err
	}
	if err := a.initAgents(); err != nil {
		return nil, err
	}
	go a.replyCache.Start()
	constructed = true
	return a, nil
}

// initTools 构造独立清单，并收集两种会话需要屏蔽的工具名称
func (a *Agent) initTools() error {
	if err := a.initGroupTools(); err != nil {
		return err
	}
	if err := a.initPrivateTools(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, list := range [][]tool.BaseTool{a.groupTools, a.privateTools} {
		for _, t := range list {
			info, err := t.Info(a.ctx)
			if err != nil || info == nil {
				continue
			}
			name := strings.TrimSpace(info.Name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			a.toolNames = append(a.toolNames, name)
		}
	}
	return nil
}

// initGroupTools 显式声明群聊工具，不从私聊工具二次过滤
func (a *Agent) initGroupTools() error {
	return a.buildTools(&a.groupTools, []func() (tool.BaseTool, error){
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
	})
}

// initPrivateTools 显式声明私聊工具，不暴露群成员和群操作能力
func (a *Agent) initPrivateTools() error {
	return a.buildTools(&a.privateTools, []func() (tool.BaseTool, error){
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
	})
}

// buildTools 构造内置工具并追加全部 MCP 扩展工具
func (a *Agent) buildTools(dst *[]tool.BaseTool, builders []func() (tool.BaseTool, error)) error {
	for _, build := range builders {
		t, err := build()
		if err != nil {
			return err
		}
		*dst = append(*dst, t)
	}
	if a.mcpMgr == nil {
		return nil
	}
	mcpTools := a.mcpMgr.GetTools()
	if len(mcpTools) == 0 {
		return nil
	}
	*dst = append(*dst, mcpTools...)
	return nil
}

// initAgents 构造群聊与私聊两个 Agent
func (a *Agent) initAgents() error {
	groupAgent, err := a.newChatAgent(a.groupTools, a.groupGenModelInput)
	if err != nil {
		return err
	}
	privateAgent, err := a.newChatAgent(a.privateTools, a.privateGenModelInput)
	if err != nil {
		return err
	}
	a.groupAgent = groupAgent
	a.privateAgent = privateAgent
	return nil
}

// newChatAgent 两种会话共用步数、错误处理与执行约束，工具清单各自独立
func (a *Agent) newChatAgent(chatTools []tool.BaseTool, genModelInput adk.TypedGenModelInput[*schema.AgenticMessage]) (*adk.TypedChatModelAgent[*schema.AgenticMessage], error) {
	cfg := config.Get()
	maxStep := cfg.Agent.MaxStep
	if maxStep <= 0 {
		maxStep = 13
	}
	argumentsHandler, err := tools.NewToolArgumentsHandler(a.ctx, chatTools)
	if err != nil {
		return nil, err
	}
	return adk.NewTypedChatModelAgent[*schema.AgenticMessage](a.ctx, &adk.TypedChatModelAgentConfig[*schema.AgenticMessage]{
		Model:         a.model,
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

func (a *Agent) Start() error {
	if err := a.restoreSessions(); err != nil {
		return err
	}
	a.bot.OnMessage(a.onMessage)
	a.bot.OnRecall(a.onRecall)

	a.wg.Add(1)
	go a.conversationThinkLoop()
	zap.L().Info("Agent 已启动")
	return nil
}

func (a *Agent) Stop() {
	a.shutdown()
	zap.L().Info("Agent 已停止")
}

func (a *Agent) shutdown() {
	a.stopping.Store(true)
	// 1. 停止 OneBot 接收并等待所有已分发事件处理完成，事件生产者全部退出
	if a.bot != nil {
		if err := a.bot.Close(); err != nil {
			zap.L().Warn("关闭 OneBot 连接失败", zap.Error(err))
		}
	}
	// 2. 取消 Agent 上下文并停止思考调度，等待所有 thinkGroup（含本地发言生产者）退出
	a.cancel()
	a.clearPendingThinks()
	a.stopPrivateTimers()
	if a.groupConcurrency != nil {
		a.groupConcurrency.Close()
	}
	a.thinkWG.Wait()
	// 3. 关闭提交队列并排空：此时没有生产者，队列消息以排空上下文完成落库
	a.groupCommits.close()
	a.privateCommits.close()

	a.wg.Wait()
	a.stopCaches()
	if a.mcpMgr != nil {
		a.mcpMgr.Close()
	}
}

func (a *Agent) OneBotConnected() bool {
	return a != nil && a.bot != nil && a.bot.IsConnected()
}

func (a *Agent) BotSelfID() int64 {
	if a == nil || a.bot == nil {
		return 0
	}
	return a.bot.GetSelfID()
}

func newAgentTTLCache[K comparable, V any](capacity int, ttl time.Duration) *ttlcache.Cache[K, V] {
	return ttlcache.New(
		ttlcache.WithTTL[K, V](ttl),
		ttlcache.WithCapacity[K, V](uint64(capacity)),
		ttlcache.WithDisableTouchOnHit[K, V](),
	)
}

func (a *Agent) stopCaches() {
	if a.replyCache != nil {
		a.replyCache.Stop()
	}
}

func (a *Agent) MCPToolCount() int {
	if a == nil || a.mcpMgr == nil {
		return 0
	}
	return len(a.mcpMgr.GetTools())
}
