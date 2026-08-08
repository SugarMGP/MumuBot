package agent

import (
	"context"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/jargon"
	"mumu-bot/internal/learning"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/persona"
	"mumu-bot/internal/tools"
	"mumu-bot/internal/topic"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
)

const (
	agentThinkTimeout   = 60 * time.Second
	replyCacheTTL       = 30 * time.Minute
	replyCacheCapacity  = 1024
	visionCacheTTL      = 6 * time.Hour
	visionCacheCapacity = 512
)

type Agent struct {
	ctx            context.Context
	cancel         context.CancelFunc
	persona        *persona.Persona
	memory         *memory.Manager
	model          model.ToolCallingChatModel
	vision         *llm.VisionClient
	bot            *onebot.Client
	react          *react.Agent
	tools          []tool.BaseTool
	mcpMgr         *mcp.Manager
	concurrencyMgr *ConcurrencyManager

	jargonMgr *jargon.Manager
	learner   *learning.Learner

	replyCache  *ttlcache.Cache[int64, onebot.ReplyInfo]
	visionCache *ttlcache.Cache[string, string]
	topicMgr    *topic.Manager

	buffers         map[int64][]*onebot.GroupMessage
	lastReadMessage map[int64]*onebot.GroupMessage
	buffersMu       sync.RWMutex

	commitMu     sync.Mutex
	commitQueues map[int64]chan commitItem
	commitWG     sync.WaitGroup

	recallMu       sync.Mutex
	pendingRecalls map[int64]map[int64]time.Time

	pendingThinks map[int64]*pendingThink
	pendingMu     sync.Mutex

	wg sync.WaitGroup
}

type pendingThink struct {
	timer             *time.Timer
	probabilityPassed bool
	generation        uint64
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

	chatModel, err := llm.NewClientForTier(llm.TierHigh)
	if err != nil {
		return nil, fmt.Errorf("创建 LLM 客户端失败: %w", err)
	}

	topicModel, err := llm.NewClientForTier(llm.TierLow)
	if err != nil {
		return nil, fmt.Errorf("创建话题摘要模型失败: %w", err)
	}

	var visionClient *llm.VisionClient
	if cfg.VisionLLM.Enabled {
		visionClient, err = llm.NewVisionClient()
		if err != nil {
			zap.L().Error("Vision 客户端创建失败，视觉理解不可用", zap.Error(err))
		} else if visionClient != nil {
			zap.L().Info("Vision 已启用", zap.String("model", cfg.VisionLLM.Model))
		}
	}

	rootCtx, cancel := context.WithCancel(context.Background())
	a := &Agent{
		ctx:             rootCtx,
		cancel:          cancel,
		persona:         p,
		memory:          mem,
		model:           chatModel,
		vision:          visionClient,
		bot:             botClient,
		buffers:         make(map[int64][]*onebot.GroupMessage),
		commitQueues:    make(map[int64]chan commitItem),
		pendingRecalls:  make(map[int64]map[int64]time.Time),
		pendingThinks:   make(map[int64]*pendingThink),
		lastReadMessage: make(map[int64]*onebot.GroupMessage),
		replyCache:      newAgentTTLCache[int64, onebot.ReplyInfo](replyCacheCapacity, replyCacheTTL),
		visionCache:     newAgentTTLCache[string, string](visionCacheCapacity, visionCacheTTL),
	}
	a.topicMgr = topic.NewManager(rootCtx, topic.NewDBStore(mem.GetDB(), mem.EmbeddingProvider(), mem, botClient.GetSelfID), topicModel)
	constructed := false
	defer func() {
		if constructed {
			return
		}
		a.shutdown()
	}()

	zap.L().Info("人格已加载", zap.String("name", a.persona.GetName()))

	a.concurrencyMgr = NewConcurrencyManager(a.ctx, cfg.Agent.MaxCoroutine, a.think)

	a.jargonMgr = jargon.New(mem)

	if cfg.Learning.Enabled {
		learner, err := learning.New(mem, a.jargonMgr, botClient.GetSelfID)
		if err != nil {
			zap.L().Error("初始化后台学习系统失败", zap.Error(err))
		} else {
			a.learner = learner
		}
	}

	a.mcpMgr = mcp.NewMCPManager()
	if err := a.mcpMgr.LoadFromConfig(a.ctx, "config/mcp.json"); err != nil {
		zap.L().Error("加载 MCP 配置失败", zap.Error(err))
	}

	if err := a.initTools(); err != nil {
		return nil, err
	}
	if err := a.initReact(); err != nil {
		return nil, err
	}
	go a.replyCache.Start()
	go a.visionCache.Start()
	a.wg.Add(1)
	go a.recallPruneLoop()
	constructed = true
	return a, nil
}

func (a *Agent) initTools() error {
	toolBuilders := []func() (tool.BaseTool, error){
		func() (tool.BaseTool, error) { return tools.NewSaveMemoryTool() },
		func() (tool.BaseTool, error) { return tools.NewQueryMemoryTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchJargonTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchExpressionsTool() },
		func() (tool.BaseTool, error) { return tools.NewGetRecentMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewSpeakTool() },
		func() (tool.BaseTool, error) { return tools.NewStayQuietTool() },
		func() (tool.BaseTool, error) { return tools.NewGetGroupMemberDetailTool() },
		func() (tool.BaseTool, error) { return tools.NewPokeTool() },
		func() (tool.BaseTool, error) { return tools.NewReactToMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewRecallMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchStickersTool() },
		func() (tool.BaseTool, error) { return tools.NewSendStickerTool() },
		func() (tool.BaseTool, error) { return tools.NewGetGroupNoticesTool() },
		func() (tool.BaseTool, error) { return tools.NewGetEssenceMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewGetMessageReactionsTool() },
		func() (tool.BaseTool, error) { return tools.NewGetForwardMessageDetailTool() },
		func() (tool.BaseTool, error) { return tools.NewUpdateMoodTool() },
		func() (tool.BaseTool, error) { return tools.NewHttpRequestTool() },
	}

	for _, build := range toolBuilders {
		t, err := build()
		if err != nil {
			return err
		}
		a.tools = append(a.tools, t)
	}

	mcpTools := a.mcpMgr.GetTools()
	if len(mcpTools) > 0 {
		a.tools = append(a.tools, mcpTools...)
		zap.L().Info("已加载 MCP 工具", zap.Int("count", len(mcpTools)))
	}

	return nil
}

func (a *Agent) initReact() error {
	cfg := config.Get()
	maxStep := cfg.Agent.MaxStep
	if maxStep <= 0 {
		maxStep = 12
	}
	argumentsHandler, err := tools.NewToolArgumentsHandler(a.ctx, a.tools)
	if err != nil {
		return err
	}
	agent, err := react.NewAgent(a.ctx, &react.AgentConfig{
		ToolCallingModel: a.model,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:                a.tools,
			ExecuteSequentially:  true,
			ToolArgumentsHandler: argumentsHandler,
			ToolCallMiddlewares:  []compose.ToolMiddleware{{Invokable: tools.ToolDedupMiddleware()}},
		},
		MaxStep:            maxStep,
		ToolReturnDirectly: map[string]struct{}{"stayQuiet": {}},
	})
	if err != nil {
		return err
	}
	a.react = agent
	return nil
}

func (a *Agent) Start() {
	cfg := config.Get()
	a.bot.OnMessage(a.onMessage)
	a.bot.OnRecall(a.onRecall)
	if a.learner != nil {
		a.bot.OnConnected(func() { a.learner.Start(a.ctx) })
		a.learner.Start(a.ctx)
	}

	a.loadBuffersFromDB()
	groupIDs := make([]int64, 0, len(cfg.Groups))
	for _, group := range cfg.Groups {
		if group.Enabled {
			groupIDs = append(groupIDs, group.GroupID)
		}
	}
	a.topicMgr.RecoverPendingAssignments(groupIDs)
	a.wg.Add(1)
	go a.thinkLoop()
	zap.L().Info("Agent 已启动")
}

func (a *Agent) loadBuffersFromDB() {
	cfg := config.Get()
	for _, gc := range cfg.Groups {
		if !gc.Enabled {
			continue
		}

		bufSize := cfg.Agent.MessageBufferSize
		if bufSize <= 0 {
			bufSize = 30
		}

		logs := a.memory.GetRecentMessages(gc.GroupID, 0, bufSize, 0)
		if len(logs) == 0 {
			continue
		}

		messages := make([]*onebot.GroupMessage, 0, len(logs))
		for _, log := range logs {
			messages = append(messages, messageLogToBufferedGroupMessage(log))
		}
		a.buffersMu.Lock()
		a.buffers[gc.GroupID] = messages
		a.lastReadMessage[gc.GroupID] = messages[len(messages)-1]
		a.buffersMu.Unlock()

		zap.L().Info("已从数据库加载消息历史", zap.Int64("group_id", gc.GroupID), zap.Int("count", len(logs)))
	}
}

func messageLogToBufferedGroupMessage(log memory.MessageLog) *onebot.GroupMessage {
	msg := &onebot.GroupMessage{
		MessageID:    log.OneBotMessageID,
		GroupID:      log.GroupID,
		UserID:       log.UserID,
		Nickname:     log.Nickname,
		Content:      log.TextContent,
		FinalContent: log.DisplayContent,
		IsMentioned:  log.IsMentioned,
		Time:         log.MessageTime,
	}
	if log.ForwardPayload != nil {
		_ = sonic.UnmarshalString(*log.ForwardPayload, &msg.Forwards)
	}
	return msg
}

func (a *Agent) Stop() {
	a.shutdown()
	zap.L().Info("Agent 已停止")
}

func (a *Agent) shutdown() {
	// 1. 停止 OneBot 接收并等待所有已分发事件处理完成，事件生产者全部退出。
	if a.bot != nil {
		if err := a.bot.Close(); err != nil {
			zap.L().Warn("关闭 OneBot 连接失败", zap.Error(err))
		}
	}
	// 2. 取消 Agent 上下文并停止思考调度，等待所有 think（含本地发言生产者）退出，
	//    之后不会再有任何 enqueueCommit 调用。
	a.cancel()
	a.clearPendingThinks()
	if a.concurrencyMgr != nil {
		a.concurrencyMgr.Close()
	}
	// 3. 关闭提交队列并排空：此时没有生产者，队列消息以排空上下文完成落库。
	a.commitMu.Lock()
	for groupID, queue := range a.commitQueues {
		close(queue)
		delete(a.commitQueues, groupID)
	}
	a.commitMu.Unlock()
	a.commitWG.Wait()

	a.wg.Wait()
	a.stopCaches()
	if a.learner != nil {
		a.learner.Stop()
	}
	if a.topicMgr != nil {
		a.topicMgr.Close()
	}
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
	if a.visionCache != nil {
		a.visionCache.Stop()
	}
}

func (a *Agent) MCPToolCount() int {
	if a == nil || a.mcpMgr == nil {
		return 0
	}
	return len(a.mcpMgr.GetTools())
}

func (a *Agent) ReloadJargons() {
	if a == nil || a.jargonMgr == nil {
		return
	}
	a.jargonMgr.Reload()
}
