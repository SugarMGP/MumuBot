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

// PrepareModelSwitch 用旧模型整理不兼容或超限历史，锁保持到保存与运行态切换结束
func (a *Agent) PrepareModelSwitch(ctx context.Context, targets map[string]ModelTarget) (func(), []string, error) {
	return a.prepareModelSwitch(ctx, targets, nil)
}

// PrepareModelSwitchFor 在 QQ 离线或 Agent 尚未激活时整理持久工作集
func PrepareModelSwitchFor(ctx context.Context, mem *memory.Manager, models *modelprofiles.Store, targets map[string]ModelTarget, mcpManager *mcp.Manager) (func(), []string, error) {
	a := &Agent{ctx: ctx, memory: mem, modelStore: models, sessions: newSessionStore(mem)}
	return a.prepareModelSwitch(ctx, targets, mcpManager)
}

func (a *Agent) prepareModelSwitch(ctx context.Context, targets map[string]ModelTarget, mcpManager *mcp.Manager) (release func(), affected []string, err error) {
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
	// 先只读预筛达到新窗口阈值的会话；无关会话不抢维护锁，也不被正在运行的长回合拖住
	candidates := make([]*Session, 0)
	seen := map[string]bool{}
	for _, role := range []string{"qq", "life", "worker"} {
		target := targets[role]
		window := target.ContextWindow
		if window <= 0 {
			continue
		}
		conversations, listErr := capacityTargets(ctx, a.memory, role)
		if listErr != nil {
			return release, nil, listErr
		}
		for _, conversation := range conversations {
			session := a.sessions.get(conversation.ConversationKind, conversation.TargetID)
			key := sessionKey(session.kind, session.targetID)
			if seen[key] {
				continue
			}
			_, chatTools := capacityRoleTools(available, session.kind)
			previewCtx := withGeneration(ctx, &Generation{roleModel: roleModel{contextWindow: window}})
			messages, previewErr := previewPersistedMessages(previewCtx, a.memory, state.persona, session.kind, session.targetID)
			if previewErr != nil {
				return release, nil, previewErr
			}
			selected := available.forKind(session.kind)
			changingProtocol := selected.model != nil && selected.provider != target.Provider
			needsCapacity := target.CheckCapacity && session.EstimatedRequestTokens(messages, modelToolInfos(ctx, chatTools)) >= capacityTrigger(window, state.config.Compactor.TriggerRatio)
			if !changingProtocol && !needsCapacity && lastIncompatibleMessage(messages, target.Provider) < 0 {
				continue
			}
			seen[key] = true
			candidates = append(candidates, session)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return sessionKey(candidates[i].kind, candidates[i].targetID) < sessionKey(candidates[j].kind, candidates[j].targetID)
	})
	for _, session := range candidates {
		if err := ctx.Err(); err != nil {
			return release, nil, err
		}
		session.maintenanceMu.Lock()
		locked = append(locked, session)
		acquired++
	}
	for _, session := range locked {
		role, chatTools := capacityRoleTools(available, session.kind)
		target := targets[role]
		window := target.ContextWindow
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
			if restoreErr := a.restoreTranscriptSession(session.kind, session.targetID, afterID); restoreErr != nil {
				return release, []string{key}, restoreErr
			}
		}
		// shortcut: 单次切换最多整理四段，异常候选不至于无限循环；确实更大的历史再改为按窗口收敛
		for pass := 0; pass < 4; pass++ {
			previewCtx := withGeneration(ctx, &Generation{roleModel: roleModel{contextWindow: window}})
			messages, previewErr := previewPersistedMessages(previewCtx, a.memory, state.persona, session.kind, session.targetID)
			if previewErr != nil {
				return release, []string{key}, previewErr
			}
			incompatible := lastIncompatibleMessage(messages, target.Provider) >= 0
			needsCapacity := target.CheckCapacity && session.EstimatedRequestTokens(messages, modelToolInfos(ctx, chatTools)) >= capacityTrigger(window, state.config.Compactor.TriggerRatio)
			if !incompatible && !needsCapacity {
				break
			}
			var candidate *compactionCandidate
			if incompatible {
				if role == "qq" {
					candidate, previewErr = session.BuildProtocolCompaction(target.Provider)
				} else {
					messages, previewErr = protocolSummaryPrefix(messages, target.Provider)
				}
				if previewErr != nil {
					return release, []string{key}, previewErr
				}
				if role == "qq" && candidate == nil {
					return release, []string{key}, fmt.Errorf("no closed protocol compaction candidate")
				}
			} else if role == "qq" {
				candidate = session.BuildCompactionCandidate(window, state.config.Compactor.RecentTailRatio)
				if candidate == nil {
					break
				}
			} else {
				messages = contextSummaryPrefix(session, messages)
				if len(messages) == 0 {
					break
				}
			}
			selected := available.forKind(session.kind)
			if selected.model == nil {
				selection, loadErr := a.modelStore.Selection(ctx)
				if loadErr != nil {
					return release, []string{key}, loadErr
				}
				selected, loadErr = a.loadModel(ctx, selection.ID(role))
				if loadErr != nil {
					return release, []string{key}, loadErr
				}
			}
			// 压缩使用原模型，但尾部与新代摘要按目标窗口计算
			selected.contextWindow = window
			compactGeneration := &Generation{roleModel: selected, lifeModel: selected, workerModel: selected}
			compactCtx := withGeneration(ctx, compactGeneration)
			if compactErr := a.compactForWindow(compactCtx, session, candidate, messages); compactErr != nil {
				return release, []string{key}, fmt.Errorf("整理 %s 以切换模型失败: %w", key, compactErr)
			}
			if role != "qq" {
				break
			}
		}
	}
	// 整理其它会话时仍会接收新输入，保存前只复查本次锁住的会话
	for _, session := range locked {
		role, chatTools := capacityRoleTools(available, session.kind)
		target := targets[role]
		window := target.ContextWindow
		previewCtx := withGeneration(ctx, &Generation{roleModel: roleModel{contextWindow: window}})
		messages, previewErr := previewPersistedMessages(previewCtx, a.memory, state.persona, session.kind, session.targetID)
		if previewErr != nil {
			return release, affected, previewErr
		}
		if lastIncompatibleMessage(messages, target.Provider) >= 0 || session.EstimatedRequestTokens(messages, modelToolInfos(ctx, chatTools)) > window {
			affected = append(affected, sessionKey(session.kind, session.targetID))
		}
	}
	return release, affected, nil
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
