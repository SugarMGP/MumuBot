package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/persona"

	"go.uber.org/zap"
)

// runState 是一轮思考固定的配置和人格快照
type runState struct {
	config           *config.RuntimeConfig
	persona          *persona.Persona
	steps            *stepBudget
	logs             *llm.LogState
	lifeUsageChecked bool
}

// stepBudget 是一次 think 的共享模型步数预算，换 runner 不补充
type stepBudget struct {
	mu        sync.Mutex
	remaining int
}

func (b *stepBudget) Remaining() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.remaining
}

func (b *stepBudget) Consume() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining <= 0 {
		return false
	}
	b.remaining--
	return true
}

type runStateKey struct{}

func withRunState(ctx context.Context, state *runState) context.Context {
	return llm.WithLogState(context.WithValue(ctx, runStateKey{}, state), state.logs)
}

func runStateFrom(ctx context.Context) *runState {
	state, _ := ctx.Value(runStateKey{}).(*runState)
	return state
}

func newRunState(role string) (*runState, error) {
	cfg := config.Snapshot()
	maxStep := cfg.Agent.MaxStep
	switch role {
	case "life":
		maxStep = cfg.Agent.LifeMaxStep
	case "worker":
		maxStep = cfg.Agent.WorkerMaxStep
	}
	if maxStep <= 0 {
		return nil, fmt.Errorf("单轮步数配置无效")
	}
	p, err := persona.NewPersona(&cfg.Persona, cfg.Identity)
	if err != nil {
		return nil, fmt.Errorf("加载人格失败: %w", err)
	}
	return &runState{config: cfg, persona: p, steps: &stepBudget{remaining: maxStep}, logs: &llm.LogState{}}, nil
}

// personaFor 优先使用本轮快照，其次使用当前配置
func (a *Agent) personaFor(ctx context.Context) *persona.Persona {
	if state := runStateFrom(ctx); state != nil && state.persona != nil {
		return state.persona
	}
	return a.currentPersona()
}

// botName 返回机器人账号昵称，用于自身消息与 @ 渲染；取不到 QQ 昵称时用全局角色名
func (a *Agent) botName() string {
	if a.bot != nil {
		if name := a.bot.GetSelfName(); name != "" {
			return name
		}
	}
	if name := strings.TrimSpace(config.Snapshot().Identity.Name); name != "" {
		return name
	}
	return "沐沐"
}

func (a *Agent) currentPersona() *persona.Persona {
	snapshot := config.Snapshot()
	p, err := persona.NewPersona(&snapshot.Persona, snapshot.Identity)
	if err != nil {
		zap.L().Warn("加载当前人格失败", zap.Error(err))
		return nil
	}
	return p
}

// configFor 优先使用本轮快照，其次使用当前配置
func configFor(ctx context.Context) *config.RuntimeConfig {
	if state := runStateFrom(ctx); state != nil && state.config != nil {
		return state.config
	}
	return config.Snapshot()
}
