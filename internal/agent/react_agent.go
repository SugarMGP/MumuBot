package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/learning"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/persona"
	"mumu-bot/internal/tools"
	"mumu-bot/internal/topic"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
)

const (
	replyCacheTTL       = 30 * time.Minute
	replyCacheCapacity  = 1024
	visionCacheTTL      = 6 * time.Hour
	visionCacheCapacity = 512
)

type Agent struct {
	ctx              context.Context
	cancel           context.CancelFunc
	persona          *persona.Persona
	memory           *memory.Manager
	model            model.ToolCallingChatModel
	vision           *llm.VisionClient
	bot              *onebot.Client
	groupReact       *react.Agent
	privateReact     *react.Agent
	groupTools       []tool.BaseTool
	privateTools     []tool.BaseTool
	mcpMgr           *mcp.Manager
	groupConcurrency *GroupThinkConcurrency
	groupCommits     *commitQueueSet[groupCommitItem]
	privateCommits   *commitQueueSet[privateCommitItem]

	learner *learning.Learner

	toolNames []string

	replyCache           *ttlcache.Cache[string, onebot.ReplyInfo]
	visionCache          *ttlcache.Cache[string, string]
	topicMgr             *topic.Manager
	privateMu            sync.Mutex
	privateBuffers       map[int64][]*onebot.ConversationMessage
	pendingPrivateThinks map[int64]*pendingThink
	privateRunning       map[int64]bool
	privateReadSeq       map[int64]uint64
	privateTrimmedSeq    map[int64]uint64
	privateStopped       bool
	thinkWG              sync.WaitGroup

	groupBuffers    map[int64][]*onebot.ConversationMessage
	groupReadSeq    map[int64]uint64
	groupTrimmedSeq map[int64]uint64
	groupBuffersMu  sync.RWMutex

	recallMu       sync.Mutex
	pendingRecalls map[string]map[int64]time.Time

	pendingGroupThinks map[int64]*pendingThink
	pendingGroupMu     sync.Mutex

	stopping         atomic.Bool
	friendRequestsMu sync.Mutex
	wg               sync.WaitGroup
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

	visionClient, err := llm.NewVisionClient()
	if err != nil {
		return nil, fmt.Errorf("创建视觉模型客户端失败: %w", err)
	}
	zap.L().Info("Vision 已启用", zap.String("model", cfg.VisionLLM.Model))

	rootCtx, cancel := context.WithCancel(context.Background())
	a := &Agent{
		ctx:                  rootCtx,
		cancel:               cancel,
		persona:              p,
		memory:               mem,
		model:                chatModel,
		vision:               visionClient,
		bot:                  botClient,
		groupBuffers:         make(map[int64][]*onebot.ConversationMessage),
		pendingRecalls:       make(map[string]map[int64]time.Time),
		pendingGroupThinks:   make(map[int64]*pendingThink),
		groupReadSeq:         make(map[int64]uint64),
		privateBuffers:       make(map[int64][]*onebot.ConversationMessage),
		pendingPrivateThinks: make(map[int64]*pendingThink),
		privateRunning:       make(map[int64]bool),
		privateReadSeq:       make(map[int64]uint64),
		privateTrimmedSeq:    make(map[int64]uint64),
		groupTrimmedSeq:      make(map[int64]uint64),
		replyCache:           newAgentTTLCache[string, onebot.ReplyInfo](replyCacheCapacity, replyCacheTTL),
		visionCache:          newAgentTTLCache[string, string](visionCacheCapacity, visionCacheTTL),
	}
	a.groupCommits = newCommitQueueSet(a.commitOne, func(targetID int64) zap.Field { return zap.Int64("group_id", targetID) })
	a.privateCommits = newCommitQueueSet(a.commitPrivateItem, func(targetID int64) zap.Field { return zap.Int64("target_id", targetID) })
	a.topicMgr = topic.NewManager(mem.GetDB())
	constructed := false
	defer func() {
		if constructed {
			return
		}
		a.shutdown()
	}()

	zap.L().Info("人格已加载", zap.String("name", a.persona.GetName()))

	a.groupConcurrency = NewGroupThinkConcurrency(a.ctx, cfg.Agent.MaxCoroutine, a.thinkGroup)

	a.learner, err = learning.New(mem, botClient.GetSelfID)
	if err != nil {
		return nil, fmt.Errorf("初始化记忆整理失败: %w", err)
	}
	a.mcpMgr = mcp.NewMCPManager()
	if err := a.mcpMgr.LoadFromConfig(a.ctx, "config/mcp.json"); err != nil {
		zap.L().Error("加载 MCP 配置失败", zap.Error(err))
	}

	if err := a.initTools(); err != nil {
		return nil, err
	}
	if err := a.initGroupReact(); err != nil {
		return nil, err
	}
	if err := a.initPrivateReact(); err != nil {
		return nil, err
	}
	go a.replyCache.Start()
	go a.visionCache.Start()
	a.wg.Add(1)
	go a.recallPruneLoop()
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
		func() (tool.BaseTool, error) { return tools.NewSaveMemoryTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchMemoryTool() },
		func() (tool.BaseTool, error) { return tools.NewSaveWorkingNoteTool() },
		func() (tool.BaseTool, error) { return tools.NewGetRecentMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewGetNewMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewSpeakTool() },
		func() (tool.BaseTool, error) { return tools.NewStayQuietTool() },
		func() (tool.BaseTool, error) { return tools.NewGetGroupMemberDetailTool() },
		func() (tool.BaseTool, error) { return tools.NewUpdateMemberIntimacyTool() },
		func() (tool.BaseTool, error) { return tools.NewPokeTool() },
		func() (tool.BaseTool, error) { return tools.NewReactToMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewRecallMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchStickersTool() },
		func() (tool.BaseTool, error) { return tools.NewSendStickerTool() },
		func() (tool.BaseTool, error) { return tools.NewGetGroupNoticesTool() },
		func() (tool.BaseTool, error) { return tools.NewGetEssenceMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewGetMessageReactionsTool() },
		func() (tool.BaseTool, error) { return tools.NewUpdateMoodTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchWebTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchMemeTool() },
		func() (tool.BaseTool, error) { return tools.NewFetchWebTool() },
		func() (tool.BaseTool, error) { return tools.NewInspectImageTool() },
		func() (tool.BaseTool, error) { return tools.NewSendImageTool() },
	})
}

// initPrivateTools 显式声明私聊工具，不暴露群成员和群操作能力
func (a *Agent) initPrivateTools() error {
	return a.buildTools(&a.privateTools, []func() (tool.BaseTool, error){
		func() (tool.BaseTool, error) { return tools.NewSaveMemoryTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchMemoryTool() },
		func() (tool.BaseTool, error) { return tools.NewSaveWorkingNoteTool() },
		func() (tool.BaseTool, error) { return tools.NewGetRecentMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewGetNewMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewPrivateSpeakTool() },
		func() (tool.BaseTool, error) { return tools.NewStayQuietTool() },
		func() (tool.BaseTool, error) { return tools.NewPrivateUpdateMemberIntimacyTool() },
		func() (tool.BaseTool, error) { return tools.NewUpdateMoodTool() },
		func() (tool.BaseTool, error) { return tools.NewPrivatePokeTool() },
		func() (tool.BaseTool, error) { return tools.NewRecallMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchStickersTool() },
		func() (tool.BaseTool, error) { return tools.NewSendStickerTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchWebTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchMemeTool() },
		func() (tool.BaseTool, error) { return tools.NewFetchWebTool() },
		func() (tool.BaseTool, error) { return tools.NewInspectImageTool() },
		func() (tool.BaseTool, error) { return tools.NewSendImageTool() },
		func() (tool.BaseTool, error) { return tools.NewSearchPrivateMessagesTool() },
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

func (a *Agent) initGroupReact() error {
	agent, err := a.newChatReact(a.groupTools)
	if err == nil {
		a.groupReact = agent
	}
	return err
}

func (a *Agent) initPrivateReact() error {
	agent, err := a.newChatReact(a.privateTools)
	if err == nil {
		a.privateReact = agent
	}
	return err
}

// newChatReact 两种会话共用步数、错误处理与执行约束，工具清单各自独立
func (a *Agent) newChatReact(chatTools []tool.BaseTool) (*react.Agent, error) {
	cfg := config.Get()
	maxStep := cfg.Agent.MaxStep
	if maxStep <= 0 {
		maxStep = 13
	}
	argumentsHandler, err := tools.NewToolArgumentsHandler(a.ctx, chatTools)
	if err != nil {
		return nil, err
	}
	return react.NewAgent(a.ctx, &react.AgentConfig{
		ToolCallingModel: a.model,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:                chatTools,
			ExecuteSequentially:  true,
			UnknownToolsHandler:  tools.UnknownToolHandler,
			ToolArgumentsHandler: argumentsHandler,
			ToolCallMiddlewares:  []compose.ToolMiddleware{{Invokable: tools.ToolErrorMiddleware()}, {Invokable: tools.ToolActionMiddleware(a.mcpMgr.ReadOnlyTools())}, {Invokable: tools.ToolDedupMiddleware()}},
		},
		MaxStep:            maxStep,
		ToolReturnDirectly: map[string]struct{}{"stayQuiet": {}},
	})
}

func (a *Agent) Start() error {
	if err := a.loadConversationBuffersFromDB(); err != nil {
		return err
	}
	a.bot.OnMessage(a.onMessage)
	a.bot.OnRecall(a.onRecall)
	if a.learner != nil {
		a.bot.OnConnected(func() { a.learner.Start(a.ctx) })
		a.learner.Start(a.ctx)
	}

	a.wg.Add(1)
	go a.conversationThinkLoop()
	zap.L().Info("Agent 已启动")
	return nil
}

func (a *Agent) loadConversationBuffersFromDB() error {
	cfg := config.Get()
	bufSize := cfg.Agent.MessageBufferSize
	if bufSize <= 0 {
		bufSize = 30
	}
	groupIDs, err := a.memory.ListActiveGroupIDs(a.ctx)
	if err != nil {
		return fmt.Errorf("读取可恢复群聊失败: %w", err)
	}
	for _, groupID := range groupIDs {

		logs, err := a.memory.GetRecentMessages(a.ctx, groupID, 0, bufSize, 0)
		if err != nil {
			return fmt.Errorf("恢复群 %d 消息失败: %w", groupID, err)
		}
		if len(logs) == 0 {
			continue
		}

		messages := a.restoreMessageBuffer(logs)
		a.groupBuffersMu.Lock()
		a.groupBuffers[groupID] = messages
		a.groupReadSeq[groupID] = 0
		a.groupBuffersMu.Unlock()

		zap.L().Info("已从数据库加载消息历史", zap.Int64("group_id", groupID), zap.Int("count", len(logs)))
	}
	privateTargets, err := a.memory.ListConversationTargets(a.ctx, memory.ConversationKindPrivate, true)
	if err != nil {
		return fmt.Errorf("读取可恢复私聊失败: %w", err)
	}
	for _, target := range privateTargets {
		logs, err := a.memory.GetRecentMessagesScope(a.ctx, memory.ConversationKindPrivate, target.TargetID, 0, bufSize, 0)
		if err != nil {
			return fmt.Errorf("恢复好友 %d 消息失败: %w", target.TargetID, err)
		}
		if len(logs) == 0 {
			continue
		}
		messages := a.restoreMessageBuffer(logs)
		a.privateMu.Lock()
		a.privateBuffers[target.TargetID] = messages
		a.privateMu.Unlock()
	}
	return nil
}

// restoreMessageBuffer 只补全窗口外的回复，窗口内消息由本轮编号关联
func (a *Agent) restoreMessageBuffer(logs []memory.MessageLog) []*onebot.ConversationMessage {
	loadedIDs := make(map[int64]bool, len(logs))
	for _, log := range logs {
		loadedIDs[log.OneBotMessageID] = true
	}
	messages := make([]*onebot.ConversationMessage, 0, len(logs))
	for _, log := range logs {
		msg := messageLogToBufferedConversationMessage(log)
		if msg.Reply != nil && !loadedIDs[msg.Reply.MessageID] {
			if replyLog, err := a.memory.GetMessageLogByScope(log.ConversationKind, log.TargetID, msg.Reply.MessageID); err == nil {
				msg.Reply = replyInfoFromMessageLog(replyLog)
			}
		}
		messages = append(messages, msg)
	}
	return messages
}

func messageLogToBufferedConversationMessage(log memory.MessageLog) *onebot.ConversationMessage {
	displayContent := log.DisplayContent
	if log.RecalledAt != nil {
		displayContent = memory.RecalledMessageDisplayContent
	}
	msg := &onebot.ConversationMessage{
		ConversationKind: log.ConversationKind,
		MessageID:        log.OneBotMessageID,
		TargetID:         log.TargetID,
		UserID:           log.UserID,
		Nickname:         log.Nickname,
		Content:          log.TextContent,
		FinalContent:     displayContent,
		IsMentioned:      log.IsMentioned,
		Time:             log.MessageTime,
	}
	if log.ReplyToMessageID != nil {
		msg.Reply = &onebot.ReplyInfo{MessageID: *log.ReplyToMessageID}
	}
	return msg
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
	// 2. 取消 Agent 上下文并停止思考调度，等待所有 thinkGroup（含本地发言生产者）退出，
	//    推理结束后不会再有发言提交生产者
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
	if a.learner != nil {
		a.learner.Stop()
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
