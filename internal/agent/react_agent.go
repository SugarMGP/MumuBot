package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelprofiles"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/tools"

	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
)

const (
	replyCacheTTL      = 30 * time.Minute
	replyCacheCapacity = 1024
)

type Agent struct {
	ctx            context.Context
	cancel         context.CancelFunc
	memory         *memory.Manager
	bot            *onebot.Client
	modelStore     *modelprofiles.Store
	mcpStore       *mcp.Store
	groupCommits   *commitQueueSet[commitItem]
	privateCommits *commitQueueSet[commitItem]
	sessions       *sessionStore
	gates          *gateStore

	groupConcurrency   *ThinkConcurrency
	privateConcurrency *ThinkConcurrency
	workerConcurrency  *ThinkConcurrency
	lifeConcurrency    *ThinkConcurrency
	workerExec         *workerExecutor
	workerCancelMu     sync.Mutex
	workerRuns         map[uint]workerRun
	workerEpochs       map[uint]uint64
	workerPoolMu       sync.Mutex

	generationMu sync.RWMutex
	generation   *Generation
	applyMu      sync.Mutex

	replyCache    *ttlcache.Cache[string, onebot.ReplyInfo]
	memberRosters *ttlcache.Cache[int64, *tools.MemberRoster]

	recallMu       sync.Mutex
	pendingRecalls map[recallCommit]pendingRecall

	pendingGroupThinks map[int64]*pendingThink
	pendingGroupMu     sync.Mutex

	pendingPrivateThinks map[int64]*pendingThink
	privateMu            sync.Mutex
	privateStopped       bool

	stopping   atomic.Bool
	lifeAsleep atomic.Bool
	wg         sync.WaitGroup

	idleMu    sync.Mutex
	idleWg    sync.WaitGroup
	idleSlots chan struct{}

	lastOrphanCleanup     time.Time
	lastToolCleanup       time.Time
	lastSnapshotCleanup   time.Time
	lastTrajectoryCleanup time.Time
}

type pendingThink struct {
	timer      *time.Timer
	generation uint64
}

// New 创建稳定 Agent；模型/MCP 在 generation 中延迟构建
func New(mem *memory.Manager, botClient *onebot.Client, models *modelprofiles.Store, mcpServers *mcp.Store, workerExecutorURL string) (*Agent, error) {
	if mem == nil || botClient == nil || botClient.GetSelfID() <= 0 {
		return nil, fmt.Errorf("OneBot 或记忆系统未就绪")
	}
	if models == nil || mcpServers == nil {
		return nil, fmt.Errorf("模型或 MCP 存储未初始化")
	}
	rootCtx, cancel := context.WithCancel(context.Background())
	a := &Agent{
		ctx:                  rootCtx,
		cancel:               cancel,
		memory:               mem,
		bot:                  botClient,
		modelStore:           models,
		mcpStore:             mcpServers,
		sessions:             newSessionStore(mem),
		gates:                newGateStore(),
		pendingGroupThinks:   make(map[int64]*pendingThink),
		pendingPrivateThinks: make(map[int64]*pendingThink),
		pendingRecalls:       make(map[recallCommit]pendingRecall),
		replyCache:           newAgentTTLCache[string, onebot.ReplyInfo](replyCacheCapacity, replyCacheTTL),
		idleSlots:            make(chan struct{}, 2),
		workerExec:           newWorkerExecutor(workerExecutorURL),
		workerRuns:           make(map[uint]workerRun),
		workerEpochs:         make(map[uint]uint64),
	}
	cfg := config.Snapshot()
	a.groupConcurrency = NewThinkConcurrency(a.ctx, cfg.Agent.MaxGroupConcurrency, a.thinkGroup)
	a.privateConcurrency = NewThinkConcurrency(a.ctx, cfg.Agent.MaxPrivateConcurrency, a.thinkPrivate)
	workerThinkMax := cfg.Agent.WorkerCount
	if workerThinkMax <= 0 {
		workerThinkMax = 1
	}
	a.workerConcurrency = NewThinkConcurrency(a.ctx, workerThinkMax, a.thinkWorker)
	a.lifeConcurrency = NewThinkConcurrency(a.ctx, 1, a.thinkLife)
	a.groupCommits = newCommitQueueSet(a.commitOne, func(targetID int64) zap.Field { return zap.Int64("group_id", targetID) })
	a.privateCommits = newCommitQueueSet(a.commitOne, func(targetID int64) zap.Field { return zap.Int64("target_id", targetID) })
	if err := mem.EnsureLife(context.Background()); err != nil {
		return nil, fmt.Errorf("初始化 life 会话失败: %w", err)
	}
	lifeState, err := mem.GetLifeState(context.Background())
	if err != nil {
		return nil, fmt.Errorf("读取 life 状态失败: %w", err)
	}
	a.lifeAsleep.Store(!lifeState.Awake)
	interrupted, err := mem.FailRunningWorkerSessions(a.ctx)
	if err != nil {
		return nil, fmt.Errorf("恢复 worker 状态失败: %w", err)
	}
	for _, worker := range interrupted {
		a.notifyLifeWorkerFailure(a.ctx, worker.ID, worker.LastError)
	}
	if err := a.SyncWorkers(cfg.Agent.WorkerCount); err != nil {
		return nil, fmt.Errorf("同步 worker 池失败: %w", err)
	}
	a.memberRosters = newAgentTTLCache[int64, *tools.MemberRoster](128, 10*time.Minute)
	a.bot.OnConnected(func() { a.memberRosters.DeleteAll() })
	go a.memberRosters.Start()
	go a.replyCache.Start()
	return a, nil
}

// Start 启动回调与调度循环；没有模型时保持事件门关闭
func (a *Agent) Start() error {
	a.bot.OnMessage(a.onMessage)
	a.bot.OnRecall(a.onRecall)

	a.wg.Add(1)
	go a.conversationThinkLoop()
	a.wg.Add(1)
	go a.lifeClockLoop()
	a.startRetentionLoop()
	zap.L().Info("Agent 已启动")
	return nil
}

// Stop 停止 Agent 并等待运行任务退出
func (a *Agent) Stop() {
	a.shutdown()
	zap.L().Info("Agent 已停止")
}

func (a *Agent) shutdown() {
	a.stopping.Store(true)
	if a.bot != nil {
		if err := a.bot.Close(); err != nil {
			zap.L().Warn("关闭 OneBot 连接失败", zap.Error(err))
		}
	}
	a.cancel()
	a.clearPendingThinks()
	a.stopPrivateTimers()
	if a.groupConcurrency != nil {
		a.groupConcurrency.Close()
	}
	if a.privateConcurrency != nil {
		a.privateConcurrency.Close()
	}
	a.cancelWorkerRuns()
	if a.workerConcurrency != nil {
		a.workerConcurrency.Close()
	}
	if a.lifeConcurrency != nil {
		a.lifeConcurrency.Close()
	}
	a.groupCommits.close()
	a.privateCommits.close()

	a.wg.Wait()
	a.idleMu.Lock()
	a.idleWg.Wait()
	a.idleMu.Unlock()
	a.stopCaches()
	a.closeGeneration()
}

func (a *Agent) closeGeneration() {
	a.generationMu.Lock()
	generation := a.generation
	a.generation = nil
	a.generationMu.Unlock()
	if generation != nil {
		generation.wg.Wait()
		generation.close()
	}
}

// qqSleeping 表示 QQ 当前不接入新的聊天输入和新回合
func (a *Agent) qqSleeping() bool { return a.lifeAsleep.Load() }

// sessionRunning 返回会话是否正在运行一个回合，供会话目录展示真实概况
func (a *Agent) sessionRunning(kind string, targetID int64) bool {
	switch kind {
	case memory.ConversationKindGroup:
		return a.groupConcurrency != nil && a.groupConcurrency.IsRunning(targetID)
	case memory.ConversationKindPrivate:
		return a.privateConcurrency != nil && a.privateConcurrency.IsRunning(targetID)
	case memory.ConversationKindWorker:
		return a.workerConcurrency != nil && a.workerConcurrency.IsRunning(targetID)
	case memory.ConversationKindLife:
		return a.lifeConcurrency != nil && a.lifeConcurrency.IsRunning(targetID)
	}
	return false
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
	if a.memberRosters != nil {
		a.memberRosters.Stop()
	}
	if a.replyCache != nil {
		a.replyCache.Stop()
	}
}

// MCPToolCount 返回当前 generation 的 MCP 工具数
func (a *Agent) MCPToolCount() int {
	generation := a.currentGeneration()
	if generation == nil || generation.mcp == nil {
		return 0
	}
	return len(generation.mcp.GetTools())
}

// MCPStatuses 返回当前 generation 的逐服务加载状态
func (a *Agent) MCPStatuses() []mcp.ServerStatus {
	generation := a.currentGeneration()
	if generation == nil || generation.mcp == nil {
		return nil
	}
	return generation.mcp.Statuses()
}

// Ready 返回是否已构建 generation
func (a *Agent) Ready() bool {
	return a.currentGeneration() != nil
}

// Activate 构建首个 generation、恢复会话并放行事件
func (a *Agent) Activate() error {
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	if a.currentGeneration() != nil {
		return nil
	}
	generation, err := a.buildGeneration()
	if err != nil {
		return err
	}
	a.sessions.resetCalibration()
	a.setGeneration(generation)
	if err := a.restoreSessions(); err != nil {
		a.setGeneration(nil)
		generation.wg.Wait()
		generation.close()
		return fmt.Errorf("恢复聊天上下文失败: %w", err)
	}
	if err := a.bootstrapLife(a.ctx); err != nil {
		zap.L().Warn("生活主线启动失败，等待新的输入或恢复操作", zap.Error(err))
	}
	a.bot.ReleaseEventGate()
	return nil
}

// Reload 构建并替换 generation；没有当前代时等同首次激活
func (a *Agent) Reload() error {
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	generation, err := a.buildGeneration()
	if err != nil {
		return err
	}
	old := a.currentGeneration()
	first := old == nil
	a.setGeneration(generation)
	if old != nil && (old.profileID != generation.profileID || old.profileVersion != generation.profileVersion || old.lifeModel.profileID != generation.lifeModel.profileID || old.lifeModel.profileVersion != generation.lifeModel.profileVersion || old.workerModel.profileID != generation.workerModel.profileID || old.workerModel.profileVersion != generation.workerModel.profileVersion) {
		a.sessions.resetCalibration()
	}
	if first {
		if err := a.restoreSessions(); err != nil {
			a.setGeneration(nil)
			generation.wg.Wait()
			generation.close()
			return fmt.Errorf("恢复聊天上下文失败: %w", err)
		}
		if err := a.bootstrapLife(a.ctx); err != nil {
			zap.L().Warn("生活主线启动失败，等待新的输入或恢复操作", zap.Error(err))
		}
		a.bot.ReleaseEventGate()
	}
	return nil
}

// HasGeneration 表示当前 Agent 是否已持有可用的模型运行态
func (a *Agent) HasGeneration() bool {
	return a != nil && a.currentGeneration() != nil
}

func (a *Agent) currentGeneration() *Generation {
	a.generationMu.RLock()
	defer a.generationMu.RUnlock()
	return a.generation
}

func (a *Agent) acquireGeneration() *Generation {
	a.generationMu.RLock()
	generation := a.generation
	if generation != nil {
		generation.wg.Add(1)
	}
	a.generationMu.RUnlock()
	return generation
}

func (a *Agent) setGeneration(next *Generation) {
	a.generationMu.Lock()
	old := a.generation
	a.generation = next
	a.generationMu.Unlock()
	if old != nil {
		go func() {
			old.wg.Wait()
			old.close()
		}()
	}
}

func (a *Agent) currentMedia() *media.Manager {
	generation := a.currentGeneration()
	if generation != nil && generation.media != nil {
		return generation.media
	}
	return nil
}

// SetConcurrencyLimits 热更新群聊与私聊并发上限
func (a *Agent) SetConcurrencyLimits(group, private int) {
	if a.groupConcurrency != nil {
		a.groupConcurrency.SetMax(group)
	}
	if a.privateConcurrency != nil {
		a.privateConcurrency.SetMax(private)
	}
}
