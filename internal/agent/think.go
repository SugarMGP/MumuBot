package agent

import (
	"context"
	"errors"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/tools"

	"go.uber.org/zap"
)

func (a *Agent) conversationThinkLoop() {
	defer a.wg.Done()
	interval := time.Duration(config.Snapshot().Agent.ThinkInterval) * time.Second
	if interval <= 0 {
		interval = 15 * time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-timer.C:
			a.thinkCycle()
			next := time.Duration(config.Snapshot().Agent.ThinkInterval) * time.Second
			if next <= 0 {
				next = 15 * time.Second
			}
			timer.Reset(next)
		}
	}
}

func (a *Agent) thinkCycle() {
	// 单个查询失败不能拦住 worker 扫描和生活时钟的独立推进
	if !a.qqSleeping() {
		if groupIDs, err := a.memory.ListActiveGroupIDs(a.ctx); err != nil {
			zap.L().Warn("读取可思考群聊失败", zap.Error(err))
		} else {
			for _, groupID := range groupIDs {
				session := a.sessions.get(memory.ConversationKindGroup, groupID)
				pending := session.PendingMessages()
				if !hasActionableMessage(pending) || !session.FailedBatchAllowsRetry(pending) {
					continue
				}
				a.scheduleGroupThink(groupID, latestReceivedAt(pending))
			}
		}
		if targets, err := a.memory.ListConversationTargets(a.ctx, memory.ConversationKindPrivate, true); err != nil {
			zap.L().Warn("读取可思考私聊失败", zap.Error(err))
		} else {
			for _, target := range targets {
				if target.Blocked {
					continue
				}
				session := a.sessions.get(memory.ConversationKindPrivate, target.TargetID)
				pending := session.PendingMessages()
				if !hasActionableMessage(pending) || !session.FailedBatchAllowsRetry(pending) {
					continue
				}
				a.schedulePrivateThink(target.TargetID, latestReceivedAt(pending))
			}
		}
	}
	a.scanWorkerThinks()
	if !a.qqSleeping() {
		a.scanIdleCompactions()
	}
}

// latestReceivedAt 取待处理消息里最新的一条进入时间，防抖从消息进来到达开始算
func latestReceivedAt(pending []*sessionUser) time.Time {
	var latest time.Time
	for _, user := range pending {
		if user == nil || user.IsMaintenanceHint || user.IsObservation {
			continue
		}
		if user.ReceivedAt.After(latest) {
			latest = user.ReceivedAt
		}
	}
	return latest
}

func (a *Agent) scheduleGroupThink(groupID int64, receivedAt time.Time) {
	if a.qqSleeping() {
		return
	}
	debounce := time.Duration(config.Snapshot().Agent.ThinkDebounceMS) * time.Millisecond
	delay := remainingDebounce(receivedAt, time.Now(), debounce)

	a.pendingGroupMu.Lock()
	defer a.pendingGroupMu.Unlock()
	if a.stopping.Load() || a.ctx.Err() != nil || a.groupConcurrency.IsRunning(groupID) {
		return
	}
	pending := a.pendingGroupThinks[groupID]
	if pending == nil {
		pending = &pendingThink{}
		a.pendingGroupThinks[groupID] = pending
	}
	pending.resetTimer(delay, func(p *pendingThink, generation uint64) {
		a.flushPendingThink(groupID, p, generation)
	})
}

// resetTimer 在会话锁内递增代次，旧回调即使已启动也不能消费新批次
func (p *pendingThink) resetTimer(delay time.Duration, fire func(*pendingThink, uint64)) {
	p.generation++
	generation := p.generation
	if p.timer != nil {
		p.timer.Stop()
	}
	p.timer = time.AfterFunc(delay, func() { fire(p, generation) })
}

func remainingDebounce(receivedAt, now time.Time, debounce time.Duration) time.Duration {
	if receivedAt.IsZero() {
		return debounce
	}
	delay := receivedAt.Add(debounce).Sub(now)
	if delay < 0 {
		return 0
	}
	return delay
}

func (a *Agent) flushPendingThink(groupID int64, expected *pendingThink, generation uint64) {
	a.pendingGroupMu.Lock()
	pending, ok := a.pendingGroupThinks[groupID]
	if !ok || pending != expected || pending.generation != generation || a.stopping.Load() || a.ctx.Err() != nil || a.qqSleeping() {
		a.pendingGroupMu.Unlock()
		return
	}

	delete(a.pendingGroupThinks, groupID)
	a.pendingGroupMu.Unlock()

	a.groupConcurrency.Submit(groupID)
}

func (a *Agent) clearPendingThinks() {
	a.pendingGroupMu.Lock()
	defer a.pendingGroupMu.Unlock()

	for groupID, pending := range a.pendingGroupThinks {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		delete(a.pendingGroupThinks, groupID)
	}
}

func (a *Agent) thinkGroup(groupID int64) {
	session := a.sessions.get(memory.ConversationKindGroup, groupID)
	session.maintenanceMu.Lock()
	defer session.maintenanceMu.Unlock()
	generation := a.acquireGeneration()
	if generation == nil {
		return
	}
	defer generation.wg.Done()
	state, err := newRunState("chat")
	if err != nil {
		zap.L().Warn("加载本轮配置失败", zap.Int64("group_id", groupID), zap.Error(err))
		return
	}
	if a.qqSleeping() || !a.conversationAllowed(memory.ConversationKindGroup, groupID) {
		return
	}
	if a.bot.IsSelfMuted(groupID) {
		return
	}
	pending := session.PendingMessages()
	if len(pending) == 0 || !hasActionableMessage(pending) {
		return
	}
	forced := a.hasStrongInteraction(pending)
	if !a.gateAllow(groupID, session, pending, forced) {
		return
	}

	ctx := withRunState(a.ctx, state)
	ctx = withGeneration(ctx, generation)
	ctx, err = a.buildConversationToolContext(ctx, memory.ConversationKindGroup, groupID, session)
	if err != nil {
		zap.L().Warn("构建群聊工具上下文失败", zap.Int64("group_id", groupID), zap.Error(err))
		return
	}
	tc := tools.GetToolContext(ctx)
	a.injectMaintenanceHint(ctx, session)

	if state.steps.Remaining() <= 0 {
		zap.L().Warn("群聊本轮步数已用完", zap.Int64("group_id", groupID))
		return
	}
	chatAgent, err := a.newChatAgent(generation, generation.groupTools, state.steps.Remaining())
	if err != nil {
		zap.L().Warn("构建群聊运行态失败", zap.Int64("group_id", groupID), zap.Error(err))
		return
	}
	messages, err := a.assembleMessages(ctx, session)
	if err != nil {
		zap.L().Warn("装配群聊上下文失败", zap.Int64("group_id", groupID), zap.Error(err))
		return
	}
	if runErr := a.runAgent(ctx, chatAgent, messages); runErr != nil {
		if isContextOverflowError(runErr) {
			if a.compactAfterOverflow(ctx, memory.ConversationKindGroup, groupID, generation.groupTools, a.passthroughModelInput, session) {
				if tc != nil {
					tc.TopicMaintained = true
				}
				if retryErr := a.retryAfterOverflow(ctx, memory.ConversationKindGroup, groupID, state, generation.groupTools, session); retryErr == nil {
					a.finishSuccessfulThink(ctx, session, tc)
					return
				} else {
					session.MarkFailedAfterRun(pending, tc != nil && tc.ModelRequested)
					zap.L().Error("群聊溢出重试失败", zap.Int64("group_id", groupID), zap.Error(retryErr))
					return
				}
			}
			if !session.NeedsCompactionBeforeModel() {
				session.MarkFailedAfterRun(pending, tc != nil && tc.ModelRequested)
			}
			return
		}
		if errors.Is(runErr, errCompactionPending) || session.NeedsCompactionBeforeModel() {
			zap.L().Warn("群聊等待压缩重试", zap.Int64("group_id", groupID), zap.Error(runErr))
			return
		}
		session.MarkFailedAfterRun(pending, tc != nil && tc.ModelRequested)
		zap.L().Error("群聊思考失败", zap.Int64("group_id", groupID), zap.Error(runErr))
		return
	}
	a.finishSuccessfulThink(ctx, session, tc)
}

// finishSuccessfulThink 在一轮 think 完整结束后再判断压缩并推进维护计数
func (a *Agent) finishSuccessfulThink(ctx context.Context, session *Session, tc *tools.ToolContext) {
	session.ClearFailedBatch()
	compacted := a.compactAfterThink(ctx, session)
	if tc != nil {
		session.CompleteTurn(tc.MemoryMaintained, tc.TopicMaintained || compacted)
	}
}

// compactAfterThink 用上一条 assistant 的真实用量判断是否需要在下一轮前压缩
func (a *Agent) compactAfterThink(ctx context.Context, session *Session) bool {
	cfg := configFor(ctx)
	generation := generationFrom(ctx)
	if cfg == nil || generation == nil || generation.contextWindow <= 0 || session == nil {
		return false
	}
	total := session.LastResponseTokens()
	if total <= 0 || total < int(float64(generation.contextWindow)*cfg.Compactor.TriggerRatio) {
		return false
	}
	if session.CompactionCoolingDown(time.Now()) {
		return false
	}
	candidate := session.BuildCompactionCandidate(generation.contextWindow, cfg.Compactor.RecentTailRatio)
	if candidate == nil {
		return false
	}
	compactionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	stopShutdownWatch := context.AfterFunc(a.ctx, cancel)
	defer stopShutdownWatch()
	remaining := cfg.Compactor.MaxSteps
	if remaining <= 0 {
		remaining = 12
	}
	attempts := 0
	if err := a.runCompactionWithRepair(compactionCtx, session, candidate, &attempts, &remaining); err != nil {
		session.MarkCompactionFailure(time.Now())
		zap.L().Warn("一轮结束后压缩失败", zap.String("kind", session.kind), zap.Int64("target_id", session.targetID), zap.Error(err))
		return false
	}
	return true
}

// injectMaintenanceHint 在下一轮输入末尾追加持久化维护提醒
func (a *Agent) injectMaintenanceHint(ctx context.Context, session *Session) {
	cfg := configFor(ctx)
	if cfg == nil || session == nil {
		return
	}
	text, memoryTurn, topicTurn := session.MaintenanceDue(cfg.Maintenance.MemoryTurns, cfg.Maintenance.TopicTurns)
	if text == "" {
		return
	}
	if err := session.AppendMaintenanceHint(ctx, a.memory, text, memoryTurn, topicTurn); err != nil {
		zap.L().Warn("写入维护提醒失败", zap.String("kind", session.kind), zap.Int64("target_id", session.targetID), zap.Error(err))
	}
}

func (a *Agent) conversationAllowed(kind string, targetID int64) bool {
	if a.stopping.Load() || a.ctx.Err() != nil {
		return false
	}
	ctx, cancel := a.persistenceContext()
	defer cancel()
	allowed, err := a.memory.ConversationAllowed(ctx, kind, targetID)
	if err != nil {
		zap.L().Warn("思考启动许可检查失败", zap.String("conversation_kind", kind), zap.Int64("target_id", targetID), zap.Error(err))
	}
	return err == nil && allowed
}

// hasActionableMessage 观察事件只能进入后续上下文，不能单独触发回应
func hasActionableMessage(pending []*sessionUser) bool {
	for _, user := range pending {
		if user == nil {
			continue
		}
		if user.MessageID != 0 {
			return true
		}
		if user.IsSelfCheck {
			return true
		}
		if user.IsAgentMessage && user.Wake {
			return true
		}
	}
	return false
}

func (a *Agent) hasStrongInteraction(pending []*sessionUser) bool {
	for _, user := range pending {
		if user == nil {
			continue
		}
		// 静默信只保存，不能借“强交互”绕过群发言门控
		if user.IsAgentMessage {
			if user.Wake {
				return true
			}
			continue
		}
		if user.Mentioned {
			return true
		}
	}
	return false
}

func (a *Agent) doSpeak(ctx context.Context, groupID int64, content string, replyTo int64, mentions []int64) error {
	cfg := configFor(ctx)
	if cfg.Chat.TypingSimulation {
		typingSpeed := cfg.Chat.TypingSpeed
		if typingSpeed <= 0 {
			typingSpeed = 6
		}
		delay := time.Duration(float64(len([]rune(content)))/float64(typingSpeed)*1000) * time.Millisecond
		if delay > 5*time.Second {
			delay = 5 * time.Second
		}
		if delay < 500*time.Millisecond {
			delay = 500 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}

	msgID, arrivalSeq, err := a.bot.SendGroupMessage(ctx, groupID, content, replyTo, mentions)
	if err != nil {
		zap.L().Error("发言失败", zap.Int64("group_id", groupID), zap.Error(err))
		return err
	}

	parts := make([]onebot.MessagePart, 0, len(mentions)+1)
	for _, userID := range mentions {
		if userID > 0 {
			parts = append(parts, onebot.MessagePart{Kind: "at", AtUserID: userID})
		}
	}
	if content != "" {
		parts = append(parts, onebot.MessagePart{Kind: "text", Text: content})
	}
	msg := &onebot.ConversationMessage{
		ConversationKind: memory.ConversationKindGroup,
		MessageID:        msgID,
		TargetID:         groupID,
		UserID:           a.bot.GetSelfID(),
		Nickname:         a.botName(),
		Content:          content,
		MessageParts:     parts,
		Time:             time.Now(),
		ArrivalSeq:       arrivalSeq,
	}

	if replyTo != 0 {
		msg.Reply = &onebot.ReplyInfo{MessageID: replyTo}
	}

	if len(mentions) > 0 {
		msg.AtList = mentions
		msg.AtNames = make(map[int64]string, len(mentions))
		for _, userID := range mentions {
			if userID > 0 {
				msg.AtNames[userID] = a.resolveMentionDisplayName(ctx, msg, userID)
			}
		}
	}

	a.onMessage(msg)
	a.sessions.get(memory.ConversationKindGroup, groupID).NoteSelfSpoke(msg.Time)
	a.gates.markReply(groupID)
	zap.L().Info("发言成功", zap.Int64("group_id", groupID), zap.String("content", content))
	return nil
}

func (a *Agent) doSendSticker(ctx context.Context, groupID int64, fileName string, description string) error {
	msgID, arrivalSeq, err := a.bot.SendImageMessage(ctx, groupID, fileName, true)
	if err != nil {
		zap.L().Error("发送表情包失败", zap.Int64("group_id", groupID), zap.String("file_name", fileName), zap.Error(err))
		return err
	}

	msg := &onebot.ConversationMessage{
		ConversationKind: memory.ConversationKindGroup,
		MessageID:        msgID,
		TargetID:         groupID,
		UserID:           a.bot.GetSelfID(),
		Nickname:         a.botName(),
		Content:          "",
		MessageParts:     []onebot.MessagePart{{Kind: "image", Index: 0}},
		Time:             time.Now(),
		ArrivalSeq:       arrivalSeq,
		Images: []onebot.ImageInfo{
			{
				SubType: 1,
				Desc:    description,
			},
		},
	}
	a.onMessage(msg)
	zap.L().Info("发送表情包成功", zap.Int64("group_id", groupID), zap.String("desc", description))
	return nil
}

func (a *Agent) doSendImageURL(ctx context.Context, groupID int64, imageURL string) error {
	msgID, arrivalSeq, err := a.bot.SendImageMessage(ctx, groupID, imageURL, false)
	if err != nil {
		return err
	}
	a.onMessage(&onebot.ConversationMessage{
		ConversationKind: memory.ConversationKindGroup,
		MessageID:        msgID,
		TargetID:         groupID,
		UserID:           a.bot.GetSelfID(),
		Nickname:         a.botName(),
		MessageParts:     []onebot.MessagePart{{Kind: "image", Index: 0}},
		Images:           []onebot.ImageInfo{{URL: imageURL}},
		Time:             time.Now(),
		ArrivalSeq:       arrivalSeq,
	})
	return nil
}
