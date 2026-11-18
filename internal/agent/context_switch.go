package agent

import (
	"context"
	"fmt"
	"sort"
	"time"

	"mumu-bot/internal/llm"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelprofiles"
	"mumu-bot/internal/tools"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ModelSwitchReport 描述实际整理进度，不承诺跨会话或批次回滚
type ModelSwitchReport struct {
	Applied         bool     `json:"-"`
	Sessions        []string `json:"sessions,omitempty"`
	Stage           string   `json:"stage,omitempty"`
	Compacted       bool     `json:"compacted"`
	Fallback        bool     `json:"fallback"`
	EstimatedTokens int      `json:"estimated_tokens,omitempty"`
	ContextWindow   int      `json:"context_window,omitempty"`
}

func (r *ModelSwitchReport) windowFailure(stage string, tokens, window int) error {
	r.Stage, r.EstimatedTokens, r.ContextWindow = stage, tokens, window
	return fmt.Errorf("model switch %s: estimated input %d, configured window %d", stage, tokens, window)
}

// PrepareModelSwitch 先尝试旧模型，再由目标模型接管，锁保留到保存和运行态切换结束
func (a *Agent) PrepareModelSwitch(ctx context.Context, targets map[string]ModelTarget) (func(), *ModelSwitchReport, error) {
	return a.prepareModelSwitch(ctx, targets, nil)
}

// PrepareModelSwitchFor 在 QQ 离线或 Agent 未激活时整理持久工作集
func PrepareModelSwitchFor(ctx context.Context, mem *memory.Manager, models *modelprofiles.Store, targets map[string]ModelTarget, mcpManager *mcp.Manager) (func(), *ModelSwitchReport, error) {
	a := &Agent{ctx: ctx, memory: mem, modelStore: models, sessions: newSessionStore(mem)}
	return a.prepareModelSwitch(ctx, targets, mcpManager)
}

func (a *Agent) prepareModelSwitch(ctx context.Context, targets map[string]ModelTarget, mcpManager *mcp.Manager) (release func(), report *ModelSwitchReport, err error) {
	ctx, cancel := context.WithCancel(ctx)
	stopShutdownWatch := context.AfterFunc(a.ctx, cancel)
	locked := make([]*Session, 0)
	var generation *Generation
	report = &ModelSwitchReport{Stage: "history"}
	release = func() {
		stopShutdownWatch()
		cancel()
		for i := len(locked) - 1; i >= 0; i-- {
			locked[i].endModelSwitch()
			locked[i].maintenanceMu.Unlock()
			if generation != nil && report.Applied {
				a.resumeAfterModelSwitch(locked[i])
			}
		}
		if generation != nil {
			generation.wg.Done()
		}
	}
	state, err := newRunState("chat")
	if err != nil {
		return release, report, err
	}
	ctx = withRunState(ctx, state)
	ctx = llm.WithRequestTracker(ctx, nil)
	generation = a.acquireGeneration()
	available := generation
	if available == nil {
		group, private, life, buildErr := buildToolLists(mcpManager)
		if buildErr != nil {
			return release, report, buildErr
		}
		worker, buildErr := buildTools(workerToolBuilders())
		if buildErr != nil {
			return release, report, buildErr
		}
		available = &Generation{groupTools: group, privateTools: private, lifeTools: life, workerTools: worker}
	}
	targetModels := map[uint]roleModel{}
	for _, role := range []string{"qq", "life", "worker"} {
		target, exists := targets[role]
		if !exists {
			continue
		}
		if _, exists := targetModels[target.ID]; exists {
			continue
		}
		selected, buildErr := buildRoleModel(target.Profile, target.APIKey)
		if buildErr != nil {
			report.Stage = "target"
			return release, report, buildErr
		}
		targetModels[target.ID] = selected
	}
	candidates := make([]*Session, 0)
	for _, role := range []string{"qq", "life", "worker"} {
		target, exists := targets[role]
		if !exists {
			continue
		}
		conversations, listErr := capacityTargets(ctx, a.memory, role)
		if listErr != nil {
			return release, report, listErr
		}
		for _, conversation := range conversations {
			session := a.sessions.get(conversation.ConversationKind, conversation.TargetID)
			if target.ID != available.forKind(session.kind).profileID || target.ConnectionChanged || target.KeyChanged {
				candidates = append(candidates, session)
				continue
			}
			_, chatTools := capacityRoleTools(available, session.kind)
			previewCtx := withGeneration(ctx, &Generation{roleModel: roleModel{contextWindow: target.ContextWindow}})
			messages, previewErr := previewPersistedMessages(previewCtx, a.memory, state.persona, session.kind, session.targetID)
			if previewErr != nil {
				return release, report, previewErr
			}
			incompatible, needsCapacity, _ := session.switchContextCheck(messages, modelToolInfos(ctx, chatTools), target, state.config.Compactor.TriggerRatio)
			if needsCapacity || incompatible {
				candidates = append(candidates, session)
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return sessionKey(candidates[i].kind, candidates[i].targetID) < sessionKey(candidates[j].kind, candidates[j].targetID)
	})
	selection, err := a.modelStore.Selection(ctx)
	if err != nil {
		return release, report, err
	}
	unavailable := map[uint]bool{}
	waitUntil := time.Now().Add(modelSwitchWait)
	for _, session := range candidates {
		report.Sessions = []string{sessionKey(session.kind, session.targetID)}
		if lockErr := session.lockForModelSwitch(ctx, waitUntil); lockErr != nil {
			report.Stage = "busy"
			return release, report, lockErr
		}
		locked = append(locked, session)
	}
	for _, session := range locked {
		role, chatTools := capacityRoleTools(available, session.kind)
		target := targets[role]
		report.Sessions = []string{sessionKey(session.kind, session.targetID)}
		if generation == nil {
			if restoreErr := a.restoreSwitchSession(ctx, session); restoreErr != nil {
				return release, report, restoreErr
			}
		}
		selected := available.forKind(session.kind)
		if selected.profileID == 0 {
			selected.profileID = selection.ID(role)
		}
		if compactErr := a.compactSwitchSession(ctx, session, target, selected, targetModels[target.ID], chatTools, unavailable, report); compactErr != nil {
			return release, report, compactErr
		}
	}
	for _, session := range locked {
		report.Sessions = []string{sessionKey(session.kind, session.targetID)}
		report.Stage = "storage"
		role, chatTools := capacityRoleTools(available, session.kind)
		target := targets[role]
		previewCtx := withGeneration(ctx, &Generation{roleModel: roleModel{contextWindow: target.ContextWindow}})
		messages, previewErr := previewPersistedMessages(previewCtx, a.memory, state.persona, session.kind, session.targetID)
		if previewErr != nil {
			return release, report, previewErr
		}
		incompatible, _, tokens := session.switchContextCheck(messages, modelToolInfos(ctx, chatTools), target, state.config.Compactor.TriggerRatio)
		if incompatible {
			report.Stage = "boundary"
			return release, report, fmt.Errorf("incompatible history remains after model switch preparation")
		}
		if tokens > target.ContextWindow {
			return release, report, report.windowFailure("remaining_window", tokens, target.ContextWindow)
		}
	}
	report.Sessions = nil
	return release, report, nil
}

func (a *Agent) restoreSwitchSession(ctx context.Context, session *Session) error {
	afterID := uint(0)
	switch session.kind {
	case memory.ConversationKindLife:
		row, err := a.memory.GetLifeState(ctx)
		if err != nil {
			return err
		}
		afterID = row.ContextThroughID
	case memory.ConversationKindWorker:
		row, err := a.memory.GetWorkerSessionByID(ctx, uint(session.targetID))
		if err != nil {
			return err
		}
		afterID = row.ContextThroughID
		if row.ContextStartID > 0 {
			afterID = max(afterID, row.ContextStartID-1)
		}
	}
	return a.restoreTranscriptSession(session.kind, session.targetID, afterID)
}

func capacityTrigger(window int, ratio float64) int {
	trigger := int(float64(window) * ratio)
	if trigger <= 0 || trigger > window {
		return window
	}
	return trigger
}

func capacityRoleTools(generation *Generation, kind string) (string, []tool.BaseTool) {
	switch kind {
	case memory.ConversationKindGroup:
		return "qq", generation.groupTools
	case memory.ConversationKindPrivate:
		return "qq", generation.privateTools
	case memory.ConversationKindWorker:
		return "worker", generation.workerTools
	default:
		return "life", generation.lifeTools
	}
}

func (a *Agent) compactForWindow(ctx context.Context, session *Session, candidate *compactionCandidate, messages []*schema.AgenticMessage) error {
	attempt := switchAttemptFrom(ctx)
	if attempt.remaining <= 0 {
		return errSwitchBudgetExhausted
	}
	if candidate != nil {
		attempts := 0
		return a.runCompactionWithRepair(ctx, session, candidate, &attempts, &attempt.remaining)
	}
	attempt.remaining--
	ctx = tools.WithToolContext(ctx, &tools.ToolContext{ConversationKind: session.kind, TargetID: session.targetID, Session: session})
	generation := generationFrom(ctx)
	ctx = llm.WithTask(ctx, session.kind, generation.forKind(session.kind).modelName)
	var summarizer adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage]
	var err error
	if session.kind == memory.ConversationKindLife {
		summarizer, err = a.newLifeSummarizer(ctx, generation)
	} else {
		summarizer, err = a.newWorkerSummarizer(ctx, generation)
	}
	if err != nil {
		return err
	}
	if summarizer == nil {
		return fmt.Errorf("摘要器未启用")
	}
	_, err = summarizer.(*summarization.TypedMiddleware[*schema.AgenticMessage]).Summarize(ctx, &adk.TypedChatModelAgentState[*schema.AgenticMessage]{Messages: messages})
	return err
}

// contextSummaryPrefix 不消费待处理输入，也不让检查点跨过它们或切断工具链
func contextSummaryPrefix(session *Session, messages []*schema.AgenticMessage) []*schema.AgenticMessage {
	firstPending := ^uint(0)
	session.mu.Lock()
	for _, pending := range session.pending {
		if pending.TranscriptID > 0 && pending.TranscriptID < firstPending {
			firstPending = pending.TranscriptID
		}
	}
	session.mu.Unlock()
	prefix := make([]*schema.AgenticMessage, 0, len(messages))
	openCalls := make(map[string]bool)
	closed := 0
	for _, message := range messages {
		if isHeadEntry(message) {
			continue
		}
		if id := transcriptEntryID(message); id >= firstPending {
			break
		}
		prefix = append(prefix, message)
		for _, block := range message.ContentBlocks {
			if block == nil {
				continue
			}
			if block.FunctionToolCall != nil {
				openCalls[block.FunctionToolCall.CallID] = true
			}
			if block.FunctionToolResult != nil {
				delete(openCalls, block.FunctionToolResult.CallID)
			}
		}
		if len(openCalls) == 0 {
			closed = len(prefix)
		}
	}
	return prefix[:closed]
}
