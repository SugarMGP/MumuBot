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
	agentThinkTimeout            = 60 * time.Second
	contextClassificationTimeout = 30 * time.Second
	replyCacheTTL                = 30 * time.Minute
	replyCacheCapacity           = 1024
	visionCacheTTL               = 6 * time.Hour
	visionCacheCapacity          = 512
)

type Agent struct {
	ctx               context.Context
	cancel            context.CancelFunc
	persona           *persona.Persona
	memory            *memory.Manager
	model             model.ToolCallingChatModel
	vision            *llm.VisionClient
	bot               *onebot.Client
	react             *react.Agent
	contextClassifier model.BaseChatModel
	tools             []tool.BaseTool
	mcpMgr            *mcp.Manager
	concurrencyMgr    *ConcurrencyManager

	jargonMgr *jargon.Manager
	learner   *learning.Learner

	replyCache  *ttlcache.Cache[int64, onebot.ReplyInfo]
	visionCache *ttlcache.Cache[string, string]
	topicMgr    *topic.Manager

	buffers         map[int64][]*onebot.GroupMessage
	lastReadMessage map[int64]*onebot.GroupMessage
	buffersMu       sync.RWMutex

	pendingThinks map[int64]*pendingThink
	pendingMu     sync.Mutex

	startupMu         sync.Mutex
	startupRecovering bool
	startupQueue      []startupEvent

	wg sync.WaitGroup
}

type pendingThink struct {
	timer             *time.Timer
	probabilityPassed bool
	generation        uint64
}

type startupEvent struct {
	message    *onebot.GroupMessage
	groupID    int64
	messageID  int64
	operatorID int64
}

func New(mem *memory.Manager) (*Agent, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, fmt.Errorf("配置未加载")
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

	botClient := onebot.NewClient()

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
		pendingThinks:   make(map[int64]*pendingThink),
		lastReadMessage: make(map[int64]*onebot.GroupMessage),
		replyCache:      newAgentTTLCache[int64, onebot.ReplyInfo](replyCacheCapacity, replyCacheTTL),
		visionCache:     newAgentTTLCache[string, string](visionCacheCapacity, visionCacheTTL),
	}
	a.topicMgr = topic.NewManager(rootCtx, topic.NewDBStore(mem.GetDB(), mem.EmbeddingProvider(), mem), topicModel)
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
		learner, err := learning.New(mem, a.jargonMgr)
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
	if err := a.initContextClassifier(); err != nil {
		return nil, err
	}
	go a.replyCache.Start()
	go a.visionCache.Start()
	constructed = true
	return a, nil
}

func (a *Agent) initTools() error {
	toolBuilders := []func() (tool.BaseTool, error){
		func() (tool.BaseTool, error) { return tools.NewSaveMemoryTool() },
		func() (tool.BaseTool, error) { return tools.NewQueryMemoryTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchJargonTool() },
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
	agent, err := react.NewAgent(a.ctx, &react.AgentConfig{
		ToolCallingModel: a.model,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:               a.tools,
			ExecuteSequentially: true,
			ToolArgumentsHandler: func(ctx context.Context, name, arguments string) (string, error) {
				return tools.CanonicalizeToolArguments(arguments)
			},
			ToolCallMiddlewares: []compose.ToolMiddleware{{Invokable: tools.ToolDedupMiddleware()}},
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

func (a *Agent) initContextClassifier() error {
	classifier, err := llm.NewClientForTier(llm.TierLow)
	if err != nil {
		return err
	}
	if classifier == nil {
		return fmt.Errorf("分类模型未初始化")
	}
	a.contextClassifier = classifier
	return nil
}

func (a *Agent) Start() {
	cfg := config.Get()
	if cfg.Learning.Enabled && a.learner != nil {
		a.learner.Start(a.ctx)
	}

	a.startupMu.Lock()
	a.startupRecovering = true
	a.startupQueue = nil
	a.startupMu.Unlock()

	a.bot.OnMessage(a.handleIncomingMessage)
	a.bot.OnRecall(a.handleIncomingRecall)
	if err := a.bot.Connect(); err != nil {
		zap.L().Fatal("OneBot 连接失败", zap.Error(err))
	}

	a.loadBuffersFromDB()
	groupIDs := make([]int64, 0, len(cfg.Groups))
	for _, group := range cfg.Groups {
		if group.Enabled {
			groupIDs = append(groupIDs, group.GroupID)
		}
	}
	a.topicMgr.RecoverPendingAssignments(groupIDs)
	a.drainStartupEvents()
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
			bufSize = 15
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

func (a *Agent) handleIncomingMessage(msg *onebot.GroupMessage) {
	a.startupMu.Lock()
	if a.startupRecovering {
		a.startupQueue = append(a.startupQueue, startupEvent{message: msg})
		a.startupMu.Unlock()
		return
	}
	a.startupMu.Unlock()

	a.onMessage(msg)
}

func (a *Agent) handleIncomingRecall(groupID, messageID, operatorID int64) {
	a.startupMu.Lock()
	if a.startupRecovering {
		a.startupQueue = append(a.startupQueue, startupEvent{groupID: groupID, messageID: messageID, operatorID: operatorID})
		a.startupMu.Unlock()
		return
	}
	a.startupMu.Unlock()

	a.onRecall(groupID, messageID, operatorID)
}

func (a *Agent) drainStartupEvents() {
	for {
		a.startupMu.Lock()
		if len(a.startupQueue) == 0 {
			a.startupRecovering = false
			a.startupMu.Unlock()
			return
		}
		pending := a.startupQueue
		a.startupQueue = nil
		a.startupMu.Unlock()

		for _, event := range pending {
			if event.message != nil {
				a.onMessage(event.message)
			} else {
				a.onRecall(event.groupID, event.messageID, event.operatorID)
			}
		}
	}
}

func (a *Agent) Stop() {
	a.shutdown()
	a.wg.Wait()
	zap.L().Info("Agent 已停止")
}

func (a *Agent) shutdown() {
	if a.bot != nil {
		if err := a.bot.Close(); err != nil {
			zap.L().Warn("关闭 OneBot 连接失败", zap.Error(err))
		}
	}
	a.cancel()
	a.clearPendingThinks()
	a.stopCaches()
	if a.learner != nil {
		a.learner.Stop()
	}
	if a.concurrencyMgr != nil {
		a.concurrencyMgr.Close()
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
