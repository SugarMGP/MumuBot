package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/persona"
	"mumu-bot/internal/tools"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// errLifeSleepComplete 是成功入睡后的正常收尾信号，不是回合失败
var errLifeSleepComplete = errors.New("life 已安排睡眠，正常收尾")

// lifeFirstBootText 是第一次进入生活空间时的开放处境，不附固定日程
const lifeFirstBootText = "这是你自己的时间。看看现在几点、自己是什么状态，安排接下来想做的事和下一次回来的时间。"

// scheduleLifeThink 请求一次 life 回合；life 单并发，不占 worker 名额
func (a *Agent) scheduleLifeThink() {
	if a.lifeConcurrency == nil || a.stopping.Load() || a.ctx.Err() != nil {
		return
	}
	a.lifeConcurrency.Submit(memory.LifeTargetID)
}

// thinkLife 复用主模型、ReAct 和 transcript，只把工具表换成生活主线；
// 成功回合结束后继续消费新输入，失败由生活时钟按持久状态有限补试
func (a *Agent) thinkLife(targetID int64) {
	var failure error
	completedRun := false
	failureCode := "life_run_failed"
	defer func() {
		if a.ctx.Err() != nil {
			return
		}
		ctx := context.WithoutCancel(a.ctx)
		if failure != nil {
			a.recordLifeError(ctx, failureCode, failure.Error())
		} else if completedRun {
			if err := a.memory.SetLifeError(ctx, "", ""); err != nil {
				zap.L().Warn("清除 life 运行问题失败", zap.Error(err))
			}
		}
	}()
	generation := a.acquireGeneration()
	if generation == nil {
		failure = fmt.Errorf("模型未就绪")
		return
	}
	defer generation.wg.Done()
	state, err := newRunState("life")
	if err != nil {
		zap.L().Warn("加载 life 本轮配置失败", zap.Error(err))
		failure = err
		return
	}
	session := a.sessions.get(memory.ConversationKindLife, targetID)
	session.maintenanceMu.Lock()
	defer session.maintenanceMu.Unlock()
	selfChecked := false
	for {
		pending := session.PendingMessages()
		if !hasActionableMessage(pending) {
			return
		}
		// 睡眠准入与即时投信一致：只有时间事件或明确恢复允许启动；循环继续也不放宽
		if a.qqSleeping() && !session.HasSleepWakePending() {
			return
		}
		ctx := withRunState(a.ctx, state)
		ctx = withGeneration(ctx, generation)
		ctx, err = a.buildLifeToolContext(ctx, session)
		if err != nil {
			zap.L().Warn("构建 life 工具上下文失败", zap.Error(err))
			failure = err
			return
		}
		tc := tools.GetToolContext(ctx)
		tc.ThinkStartArrivalSeq = maxPendingSeq(pending)
		a.injectLifeMaintenanceHint(ctx, session)

		if state.steps.Remaining() <= 0 {
			zap.L().Warn("life 本轮步数已用完")
			failure = fmt.Errorf("本轮步数用完")
			return
		}
		lifeAgent, agentErr := a.newLifeAgent(ctx, generation, generation.lifeTools, state.steps.Remaining())
		if agentErr != nil {
			zap.L().Warn("构建 life 运行态失败", zap.Error(agentErr))
			failure = agentErr
			return
		}
		messages, assembleErr := a.assembleLifeMessages(ctx, session)
		if assembleErr != nil {
			zap.L().Warn("装配 life 上下文失败", zap.Error(assembleErr))
			failure, failureCode = assembleErr, "life_context_failed"
			return
		}
		if runErr := a.runAgent(ctx, lifeAgent, messages); runErr != nil {
			zap.L().Error("life 回合失败", zap.Error(runErr))
			failure = runErr
			return
		}
		completedRun = true
		if err := a.finishLifeTurn(ctx, session, tc, &selfChecked); err != nil {
			failure = err
			return
		}
	}
}

// buildLifeToolContext 构造生活主线的工具上下文：没有 QQ 场合的发言回调，只有共享能力
func (a *Agent) buildLifeToolContext(ctx context.Context, session *Session) (context.Context, error) {
	snapshot, err := a.memory.LatestMessageLogID(ctx)
	if err != nil {
		return ctx, fmt.Errorf("读取历史读取快照失败: %w", err)
	}
	limit := configFor(ctx).Agent.LifeDailyRequestLimit
	tc := &tools.ToolContext{
		LifeDailyRequestLimit:         &limit,
		ConversationKind:              memory.ConversationKindLife,
		TargetID:                      memory.LifeTargetID,
		MemoryMgr:                     a.memory.WithContext(ctx),
		Bot:                           a.bot,
		Session:                       session,
		SnapshotLogID:                 snapshot,
		SnapshotLogIDSet:              true,
		RunID:                         newRunID(),
		MemberRosterCallback:          a.memberRoster,
		MemoryReadVersions:            map[uint]uint64{},
		SessionMessageCallback:        a.deliverSessionMessage,
		SessionMessageContextCallback: a.deliverSessionMessageWithMode,
		SessionRunningCallback:        a.sessionRunning,
		SleepChangedCallback:          a.applyLifeAwake,
		CancelWorkerCallback:          a.cancelWorkerTurn,
		PersonalFiles:                 workerFileAdapter{exec: a.workerExec, refs: a.memory.RefStore()},
		SnapshotFiles:                 workerFileAdapter{exec: a.workerExec, refs: a.memory.RefStore()},
		ReadForwardCallback: func(callCtx context.Context, id string, index int) (any, error) {
			return a.readForward(callCtx, id, index)
		},
	}
	return tools.WithToolContext(ctx, tc), nil
}

// applyLifeAwake 把睡眠工具的持久结果同步到运行许可
func (a *Agent) applyLifeAwake(_ context.Context, awake bool) {
	a.lifeAsleep.Store(!awake)
}

// injectLifeMaintenanceHint 只提醒长期记忆维护，生活主线不写会话话题
func (a *Agent) injectLifeMaintenanceHint(ctx context.Context, session *Session) {
	cfg := configFor(ctx)
	if cfg == nil || session == nil {
		return
	}
	text, memoryTurn, topicTurn := session.MaintenanceDue(cfg.Maintenance.MemoryTurns, 0)
	if text == "" {
		return
	}
	if err := session.AppendMaintenanceHint(ctx, a.memory, text, memoryTurn, topicTurn); err != nil {
		zap.L().Warn("写入 life 维护提醒失败", zap.Error(err))
	}
}

// finishLifeTurn 在一轮正常结束后核对是否留下了下一次安排，没有就如实记下
func (a *Agent) finishLifeTurn(ctx context.Context, session *Session, tc *tools.ToolContext, selfChecked *bool) error {
	checkCtx := context.WithoutCancel(ctx)
	hasNext, err := a.memory.LifeHasFutureSchedule(checkCtx)
	switch {
	case err != nil:
		return fmt.Errorf("核对 life 下一次安排失败: %w", err)
	case !hasNext:
		if budget := runStateFrom(ctx); budget != nil && budget.steps != nil && budget.steps.Remaining() <= 0 {
			zap.L().Info("life 本轮步数已用完，不再追加无安排自检")
			break
		}
		if selfChecked != nil && !*selfChecked {
			*selfChecked = true
			nudge := "你这一轮成功结束了，但还没有启用的未来安排。现在确认一下：确实想暂停、不再安排下一次，还是漏了安排？想继续就用 planNext 或 manageSchedule；确实想暂停就不必额外补计划，这一轮之后不会再追问。"
			if appendErr := session.AppendLifeSelfCheck(checkCtx, a.memory, nudge); appendErr != nil {
				return fmt.Errorf("写入 life 无安排自检失败: %w", appendErr)
			}
			break
		}
		zap.L().Info("life 已经过自检，仍没有未来安排；尊重这个选择")
	}
	if tc != nil && session != nil {
		session.CompleteTurn(tc.MemoryMaintained, false)
	}
	return nil
}

// recordLifeError 保存真实问题与有次数上限的补试，不代替 life 安排生活
func (a *Agent) recordLifeError(ctx context.Context, code, detail string) {
	zap.L().Error("life 运行失败", zap.String("session", "life:1"), zap.String("code", code), zap.String("error", detail))
	if err := a.memory.SetLifeError(ctx, code, detail); err != nil {
		zap.L().Warn("写入 life 运行问题失败", zap.Error(err))
	}
}

// lifeInstructionText 把通用人格内容和生活职责拼成本代固定系统提示
func (a *Agent) lifeInstructionText(ctx context.Context) string {
	return a.personaFor(ctx).SessionInstruction(persona.ModeLife, memory.LifeTargetID)
}

// assembleLifeMessages 装配代头 + 摘要检查点 + 检查点之后的 transcript 与待处理输入
func (a *Agent) assembleLifeMessages(ctx context.Context, session *Session) ([]*schema.AgenticMessage, error) {
	row, err := a.memory.GetLifeState(ctx)
	if err != nil {
		return nil, err
	}
	if err := session.ensureGenerationHead(ctx, a.memory, a.lifeInstructionText(ctx), ""); err != nil {
		return nil, err
	}
	through := row.ContextThroughID
	checkpoint := make([]*schema.AgenticMessage, 0)
	hasCheckpoint := len(row.ContextSummary) > 0 && string(row.ContextSummary) != "[]"
	if hasCheckpoint {
		if err := sonic.Unmarshal(row.ContextSummary, &checkpoint); err != nil {
			zap.L().Warn("解析 life 摘要检查点失败，回退完整 transcript", zap.Error(err))
			checkpoint = checkpoint[:0]
			hasCheckpoint = false
			through = 0
		}
	}

	session.mu.Lock()
	entries := append([]sessionEntry(nil), session.seen...)
	pending := append([]*sessionUser(nil), session.pending...)
	session.mu.Unlock()

	messages := make([]*schema.AgenticMessage, 0, len(entries)+len(pending)+len(checkpoint))
	heads := make([]sessionEntry, 0, 4)
	rest := make([]sessionEntry, 0, len(entries))
	for _, entry := range entries {
		if isHeadEntry(entry.msg) {
			heads = append(heads, entry)
			continue
		}
		rest = append(rest, entry)
	}
	for _, entry := range orderEntries(heads) {
		if entry.msg == nil {
			continue
		}
		tagTranscriptEntryID(entry.msg, entry.transcriptID)
		messages = append(messages, entry.msg)
	}
	if hasCheckpoint {
		messages = append(messages, checkpoint...)
	}
	for _, entry := range rest {
		if entry.msg == nil {
			continue
		}
		if hasCheckpoint && entry.transcriptID > 0 && entry.transcriptID <= through {
			continue
		}
		tagTranscriptEntryID(entry.msg, entry.transcriptID)
		messages = append(messages, entry.msg)
	}
	for _, user := range pending {
		if user == nil || user.Msg == nil {
			continue
		}
		if hasCheckpoint && user.TranscriptID > 0 && user.TranscriptID <= through {
			continue
		}
		tagTranscriptEntryID(user.Msg, user.TranscriptID)
		messages = append(messages, user.Msg)
	}
	if tc := tools.GetToolContext(ctx); tc != nil {
		if seq := maxPendingSeq(pending); seq > tc.InjectedArrivalSeq {
			tc.InjectedArrivalSeq = seq
		}
	}
	return messages, nil
}

// newLifeSummarizer 使用生活模型的 Eino 摘要；检查点只保存摘要正文，
// 代头在 Finalize 里装入当前请求，换代后不留旧人格
func (a *Agent) newLifeSummarizer(ctx context.Context, generation *Generation) (adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage], error) {
	if generation == nil || generation.lifeModel.contextWindow <= 0 {
		return nil, nil
	}
	cfg := configFor(ctx)
	ratio := cfg.Compactor.TriggerRatio
	if ratio <= 0 {
		return nil, nil
	}
	triggerTokens := int(float64(generation.lifeModel.contextWindow) * ratio)
	if triggerTokens <= 0 {
		return nil, nil
	}
	var pendingHead []*schema.AgenticMessage
	return summarization.NewTyped(a.ctx, &summarization.TypedConfig[*schema.AgenticMessage]{
		Model:           generation.lifeModel.model,
		Trigger:         &summarization.TriggerCondition{ContextTokens: triggerTokens},
		UserInstruction: persona.Render(lifeSummaryInstruction, persona.IdentityFromConfig(cfg.Identity)),
		Finalize: func(ctx context.Context, _ []*schema.AgenticMessage, summary *schema.AgenticMessage) ([]*schema.AgenticMessage, error) {
			text := strings.TrimSpace(agenticMessageText(summary))
			if text == "" {
				return nil, fmt.Errorf("life 摘要正文为空，保留原上下文")
			}
			head, err := buildLifeHead(ctx, a.memory.WithContext(ctx), a.lifeInstructionText(ctx))
			if err != nil {
				return nil, err
			}
			pendingHead = head
			// 摘要作为背景系统行，不再附带“继续旧任务”的默认编码助手收尾
			note := schema.UserAgenticMessage(systemNote("上下文摘要", text))
			note.Extra = map[string]any{"kind": kindLifeSummary}
			return append(append(make([]*schema.AgenticMessage, 0, len(head)+1), head...), note), nil
		},
		Callback: func(ctx context.Context, before, after adk.TypedChatModelAgentState[*schema.AgenticMessage]) error {
			tc := tools.GetToolContext(ctx)
			if tc == nil || tc.Session == nil {
				return fmt.Errorf("life 摘要缺少工具上下文")
			}
			session, ok := tc.Session.(*Session)
			if !ok || session == nil {
				return fmt.Errorf("life 摘要缺少会话")
			}
			head := pendingHead
			pendingHead = nil
			return a.saveLifeContext(ctx, session, before, after, head)
		},
	})
}

// saveLifeContext 在同一事务里推进摘要边界、可见序号和代头，成功后再同步内存
func (a *Agent) saveLifeContext(ctx context.Context, session *Session, before, after adk.TypedChatModelAgentState[*schema.AgenticMessage], head []*schema.AgenticMessage) error {
	ids := transcriptIDs(before.Messages)
	through := uint(0)
	for _, id := range ids {
		if id > through {
			through = id
		}
	}
	payload, err := sonic.Marshal(stripHeadEntries(after.Messages))
	if err != nil {
		return err
	}
	heads, err := headEntries(head)
	if err != nil {
		return err
	}
	seqs, headIDs, candidates, err := a.memory.CommitLifeCheckpoint(ctx, payload, through, ids, heads)
	if err != nil {
		return err
	}
	a.releaseTranscriptCandidates(ctx, candidates)
	session.ApplyVisibleSeqs(seqs)
	session.ApplyCheckpoint(through)
	session.attachHeadMessages(head, headIDs)
	zap.L().Info("life 上下文已压缩", zap.Uint("through_id", through))
	return nil
}

// Running 报告会话是否正在运行，供后台展示真实状态
func (a *Agent) Running(kind string, targetID int64) bool { return a.sessionRunning(kind, targetID) }

// WakeLifeNow 显式结束睡眠，供后台恢复操作；已经醒着时保持原安排不动
func (a *Agent) WakeLifeNow(ctx context.Context) error {
	state, err := a.memory.GetLifeState(ctx)
	if err != nil {
		return err
	}
	if state.Awake {
		return nil
	}
	now := time.Now()
	summary := ""
	if state.SleepStartedAt != nil {
		summary = a.sleepSummaryText(ctx, *state.SleepStartedAt, now)
	}
	if err := a.memory.WakeLife(ctx, now); err != nil {
		return err
	}
	a.applyLifeAwake(ctx, true)
	note := "你已经在后台被叫醒。"
	if summary != "" {
		note += "\n" + summary
	}
	return a.RequestLifeRun(ctx, note)
}

// RequestLifeRun 投递一条后台恢复输入，让 life 自己决定接下来做什么
func (a *Agent) RequestLifeRun(ctx context.Context, note string) error {
	text := strings.TrimSpace(note)
	if text == "" {
		text = "管理后台请求现在开始一轮生活：看看当前时间和状态，安排接下来想做的事和下一次回来的时间。"
	} else {
		text = "管理后台请求： " + text
	}
	if _, err := a.enqueueSessionMessage(ctx, memory.ConversationKindLife, memory.LifeTargetID,
		memory.ConversationKindLife, memory.LifeTargetID, text, true, true); err != nil {
		return err
	}
	// 后台恢复是明确请求：睡眠中也要真的跑一轮，而不是只存下来
	a.scheduleLifeThink()
	return nil
}

// bootstrapLife 首次启动给一句开放处境；已有待处理输入时只请求一次运行
func (a *Agent) bootstrapLife(ctx context.Context) error {
	state, err := a.memory.GetLifeState(ctx)
	if err != nil {
		return err
	}
	if state.RetryAt != nil || state.RetryExhausted {
		return nil
	}
	session := a.sessions.get(memory.ConversationKindLife, memory.LifeTargetID)
	count, err := a.memory.CountTranscript(ctx, memory.ConversationKindLife, memory.LifeTargetID)
	if err != nil {
		return err
	}
	if count == 0 && !session.HasActionablePending() {
		if _, err := a.deliverSessionMessage(ctx, memory.ConversationKindLife, memory.LifeTargetID,
			memory.ConversationKindLife, memory.LifeTargetID, lifeFirstBootText, true); err != nil {
			return err
		}
	}
	// 重启后按同一睡眠准入恢复：普通来信等待，时间事件或明确恢复才启动
	if session.HasActionablePending() && (!a.qqSleeping() || session.HasSleepWakePending()) {
		a.scheduleLifeThink()
	}
	return nil
}
