package agent

import (
	"context"
	"fmt"
	"sort"

	"mumu-bot/internal/llm"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelprofiles"
	"mumu-bot/internal/tools"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// PrepareContextWindows 用旧模型整理超限历史；调用方在保存并切换运行态后释放会话锁
func (a *Agent) PrepareContextWindows(ctx context.Context, windows map[string]int) (func(), []string, error) {
	return a.prepareContextWindows(ctx, windows, nil)
}

// PrepareContextWindowsFor 在 QQ 离线或 Agent 尚未激活时整理持久工作集
func PrepareContextWindowsFor(ctx context.Context, mem *memory.Manager, models *modelprofiles.Store, windows map[string]int, mcpManager *mcp.Manager) (func(), []string, error) {
	a := &Agent{ctx: ctx, memory: mem, modelStore: models, sessions: newSessionStore(mem)}
	return a.prepareContextWindows(ctx, windows, mcpManager)
}

func (a *Agent) prepareContextWindows(ctx context.Context, windows map[string]int, mcpManager *mcp.Manager) (release func(), affected []string, err error) {
	ctx, cancel := context.WithCancel(ctx)
	stopShutdownWatch := context.AfterFunc(a.ctx, cancel)
	locked := make([]*Session, 0)
	acquired := 0
	var generation *Generation
	release = func() {
		stopShutdownWatch()
		cancel()
		for i := acquired - 1; i >= 0; i-- {
			locked[i].maintenanceMu.Unlock()
		}
		if generation != nil {
			generation.wg.Done()
		}
	}
	state, err := newRunState("chat")
	if err != nil {
		return release, nil, err
	}
	ctx = withRunState(ctx, state)
	for _, role := range []string{"qq", "life", "worker"} {
		if windows[role] <= 0 {
			continue
		}
		targets, listErr := capacityTargets(ctx, a.memory, role)
		if listErr != nil {
			return release, nil, listErr
		}
		for _, target := range targets {
			locked = append(locked, a.sessions.get(target.ConversationKind, target.TargetID))
		}
	}
	// 多场合切换按固定顺序拿锁，等待当前回合结束后才取模型快照
	sort.Slice(locked, func(i, j int) bool {
		return sessionKey(locked[i].kind, locked[i].targetID) < sessionKey(locked[j].kind, locked[j].targetID)
	})
	for _, session := range locked {
		if err := ctx.Err(); err != nil {
			return release, nil, err
		}
		session.maintenanceMu.Lock()
		acquired++
	}
	generation = a.acquireGeneration()
	available := generation
	if available == nil {
		group, private, life, buildErr := buildToolLists(mcpManager)
		if buildErr != nil {
			return release, nil, buildErr
		}
		worker, buildErr := buildTools(workerToolBuilders())
		if buildErr != nil {
			return release, nil, buildErr
		}
		available = &Generation{groupTools: group, privateTools: private, lifeTools: life, workerTools: worker}
	}
	for _, session := range locked {
		role, chatTools := capacityRoleTools(available, session.kind)
		window := windows[role]
		previewCtx := withGeneration(ctx, &Generation{roleModel: roleModel{contextWindow: window}})
		messages, previewErr := previewPersistedMessages(previewCtx, a.memory, state.persona, session.kind, session.targetID)
		if previewErr != nil {
			return release, affected, previewErr
		}
		infos := modelToolInfos(ctx, chatTools)
		if requestTokens(messages, infos) < int(float64(window)*state.config.Compactor.TriggerRatio) {
			continue
		}
		key := sessionKey(session.kind, session.targetID)
		if generation == nil {
			afterID := uint(0)
			switch session.kind {
			case memory.ConversationKindLife:
				row, getErr := a.memory.GetLifeState(ctx)
				if getErr != nil {
					return release, []string{key}, getErr
				}
				afterID = row.ContextThroughID
			case memory.ConversationKindWorker:
				row, getErr := a.memory.GetWorkerSessionByID(ctx, uint(session.targetID))
				if getErr != nil {
					return release, []string{key}, getErr
				}
				afterID = row.ContextThroughID
				if row.ContextStartID > 0 {
					afterID = max(afterID, row.ContextStartID-1)
				}
			}
			if err := a.restoreTranscriptSession(session.kind, session.targetID, afterID); err != nil {
				return release, []string{key}, err
			}
			messages, err = previewPersistedMessages(previewCtx, a.memory, state.persona, session.kind, session.targetID)
			if err != nil {
				return release, []string{key}, err
			}
		}
		var candidate *compactionCandidate
		if role == "qq" {
			candidate = session.BuildCompactionCandidate(window, state.config.Compactor.RecentTailRatio)
			if candidate == nil {
				continue
			}
		} else {
			messages = contextSummaryPrefix(session, messages)
			if len(messages) == 0 {
				continue
			}
		}
		selected := available.forKind(session.kind)
		if selected.model == nil {
			selection, loadErr := a.modelStore.Selection(ctx)
			if loadErr != nil {
				return release, []string{key}, loadErr
			}
			selected, err = a.loadModel(ctx, selection.ID(role))
			if err != nil {
				return release, []string{key}, err
			}
		}
		// 压缩使用原模型，但尾部与新代摘要按目标窗口计算
		selected.contextWindow = window
		compactGeneration := &Generation{roleModel: selected, lifeModel: selected, workerModel: selected}
		compactCtx := withGeneration(ctx, compactGeneration)
		if err := a.compactForWindow(compactCtx, session, candidate, messages); err != nil {
			return release, []string{key}, fmt.Errorf("整理 %s 以切换模型失败: %w", key, err)
		}
	}
	// 整理其它会话时仍会接收新输入，保存前统一复查一次
	for _, session := range locked {
		role, chatTools := capacityRoleTools(available, session.kind)
		window := windows[role]
		previewCtx := withGeneration(ctx, &Generation{roleModel: roleModel{contextWindow: window}})
		messages, err := previewPersistedMessages(previewCtx, a.memory, state.persona, session.kind, session.targetID)
		if err != nil {
			return release, affected, err
		}
		if requestTokens(messages, modelToolInfos(ctx, chatTools)) > window {
			affected = append(affected, sessionKey(session.kind, session.targetID))
		}
	}
	return release, affected, nil
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
	if candidate != nil {
		cfg := configFor(ctx)
		attempts, remaining := 0, cfg.Compactor.MaxSteps
		return a.runCompactionWithRepair(ctx, session, candidate, &attempts, &remaining)
	}
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
	if err == nil {
		zap.L().Info("已为模型切换整理上下文", zap.String("session", sessionKey(session.kind, session.targetID)))
	}
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
