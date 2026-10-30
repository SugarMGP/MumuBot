package agent

import (
	"context"
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
	groupIDs, err := a.memory.ListActiveGroupIDs(a.ctx)
	if err != nil {
		zap.L().Warn("读取可思考群聊失败", zap.Error(err))
		return
	}
	for _, groupID := range groupIDs {
		session := a.sessions.get(memory.ConversationKindGroup, groupID)
		pending := session.PendingMessages()
		if len(pending) == 0 {
			continue
		}
		a.scheduleGroupThink(groupID, latestReceivedAt(pending))
	}
	targets, err := a.memory.ListConversationTargets(a.ctx, memory.ConversationKindPrivate, true)
	if err != nil {
		zap.L().Warn("读取可思考私聊失败", zap.Error(err))
		return
	}
	for _, target := range targets {
		if target.Blocked {
			continue
		}
		session := a.sessions.get(memory.ConversationKindPrivate, target.TargetID)
		pending := session.PendingMessages()
		if len(pending) == 0 {
			continue
		}
		a.schedulePrivateThink(target.TargetID, latestReceivedAt(pending))
	}
}

// latestReceivedAt 取待处理消息里最新的一条进入时间，防抖从消息进来到达开始算
func latestReceivedAt(pending []*sessionUser) time.Time {
	var latest time.Time
	for _, user := range pending {
		if user.ReceivedAt.After(latest) {
			latest = user.ReceivedAt
		}
	}
	return latest
}

func (a *Agent) scheduleGroupThink(groupID int64, receivedAt time.Time) {
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
	if !ok || pending != expected || pending.generation != generation || a.stopping.Load() || a.ctx.Err() != nil {
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
	generation := a.acquireGeneration()
	if generation == nil {
		return
	}
	defer generation.wg.Done()
	state, err := newRunState()
	if err != nil {
		zap.L().Warn("加载本轮配置失败", zap.Int64("group_id", groupID), zap.Error(err))
		return
	}
	if !a.conversationAllowed(memory.ConversationKindGroup, groupID) {
		return
	}
	if a.bot.IsSelfMuted(groupID) {
		return
	}
	session := a.sessions.get(memory.ConversationKindGroup, groupID)
	pending := session.PendingMessages()
	if len(pending) == 0 || !hasActionableMessage(pending) {
		return
	}
	forced := a.hasStrongInteraction(pending)
	if !forced && !a.gateAllow(groupID, session, pending, false) {
		return
	}

	ctx := withRunState(a.ctx, state)
	ctx = withGeneration(ctx, generation)
	ctx = a.buildConversationToolContext(ctx, memory.ConversationKindGroup, groupID, session)
	tc := tools.GetToolContext(ctx)
	tc.ThinkStartArrivalSeq = maxPendingSeq(pending)

	messages, err := session.Assemble(ctx, a.memory)
	if err != nil {
		zap.L().Warn("装配群聊上下文失败", zap.Int64("group_id", groupID), zap.Error(err))
		return
	}
	if runErr := a.runAgent(ctx, generation.groupAgent, messages); runErr != nil {
		if isContextOverflowError(runErr) && a.compactAfterOverflow(ctx, memory.ConversationKindGroup, groupID, generation.groupTools, a.groupGenModelInput, session) {
			if retryErr := a.retryAfterOverflow(ctx, memory.ConversationKindGroup, groupID, generation.groupAgent, session); retryErr == nil {
				return
			}
		}
		zap.L().Error("群聊思考失败", zap.Int64("group_id", groupID), zap.Error(runErr))
		return
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
		if user != nil && (user.MessageID != 0 || user.IsAgentMessage) {
			return true
		}
	}
	return false
}

func (a *Agent) hasStrongInteraction(pending []*sessionUser) bool {
	for _, user := range pending {
		if user.IsAgentMessage {
			return true
		}
		if user.Mentioned {
			return true
		}
		if p := a.currentPersona(); p != nil && p.IsMentioned(user.Content) {
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
		Nickname:         a.currentPersona().GetName(),
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
		Nickname:         a.currentPersona().GetName(),
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
		Nickname:         a.currentPersona().GetName(),
		MessageParts:     []onebot.MessagePart{{Kind: "image", Index: 0}},
		Images:           []onebot.ImageInfo{{URL: imageURL}},
		Time:             time.Now(),
		ArrivalSeq:       arrivalSeq,
	})
	return nil
}
