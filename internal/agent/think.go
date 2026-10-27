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
	ticker := time.NewTicker(time.Duration(config.Get().Agent.ThinkInterval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.thinkCycle()
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
		a.scheduleGroupThink(groupID, a.hasStrongInteraction(pending), time.Now())
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
		if len(session.PendingMessages()) == 0 {
			continue
		}
		a.schedulePrivateThink(target.TargetID, time.Now())
	}
}

func (a *Agent) scheduleGroupThink(groupID int64, isMention bool, receivedAt time.Time) {
	debounce := time.Duration(config.Get().Agent.ThinkDebounceMS) * time.Millisecond
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
	if !a.conversationAllowed(memory.ConversationKindGroup, groupID) {
		return
	}
	if a.bot.IsSelfMuted(groupID) {
		return
	}
	cfg := config.Get()
	ctxWithTimeout, cancel := context.WithTimeout(a.ctx, time.Duration(cfg.Agent.ThinkTimeoutSeconds)*time.Second)
	defer cancel()

	session := a.sessions.get(memory.ConversationKindGroup, groupID)
	pending := session.PendingMessages()
	if len(pending) == 0 {
		return
	}
	forced := a.hasStrongInteraction(pending)
	if !forced && !a.gateAllow(groupID, session, pending, false) {
		return
	}

	ctx := a.buildConversationToolContext(ctxWithTimeout, memory.ConversationKindGroup, groupID, session)
	tc := tools.GetToolContext(ctx)
	tc.ThinkStartArrivalSeq = maxPendingSeq(pending)

	messages, err := session.Assemble(ctx, a.memory, sessionBudget())
	if err != nil {
		zap.L().Warn("装配群聊上下文失败", zap.Int64("group_id", groupID), zap.Error(err))
		return
	}
	if runErr := a.runAgent(ctx, a.groupAgent, messages); runErr != nil {
		// 发言成功但轮次失败时也要推进消费水位，避免同一批消息被重复回复
		if seq := tc.ReadThroughSeq(false); seq > 0 {
			session.MarkConsumed(seq)
		}
		if errors.Is(ctxWithTimeout.Err(), context.DeadlineExceeded) {
			zap.L().Warn("群聊思考超时", zap.Int64("group_id", groupID), zap.Int("timeout_seconds", cfg.Agent.ThinkTimeoutSeconds))
		} else {
			zap.L().Error("群聊思考失败", zap.Int64("group_id", groupID), zap.Error(runErr))
		}
		return
	}
	session.MarkConsumed(maxPendingSeq(pending))
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

func (a *Agent) hasStrongInteraction(pending []*sessionUser) bool {
	for _, user := range pending {
		if user.Mentioned {
			return true
		}
		if user.ArrivalSeq == 0 {
			continue
		}
		if a.persona.IsMentioned(user.Content) {
			return true
		}
	}
	return false
}

// latestMessageWithID 按缓冲顺序反向查找，不比较 OneBot message_id
func latestMessageWithID(messages []*onebot.ConversationMessage) *onebot.ConversationMessage {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] != nil && messages[i].MessageID != 0 {
			return messages[i]
		}
	}
	return nil
}

func (a *Agent) doSpeak(ctx context.Context, groupID int64, content string, replyTo int64, mentions []int64) error {
	cfg := config.Get()
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
	tools.GetToolContext(ctx).MarkActionSucceeded()

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
		Nickname:         a.persona.GetName(),
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
	zap.L().Info("发言成功", zap.Int64("group_id", groupID), zap.String("content", content))
	return nil
}

func (a *Agent) doSendSticker(ctx context.Context, groupID int64, fileName string, description string) error {
	msgID, arrivalSeq, err := a.bot.SendImageMessage(ctx, groupID, fileName, true)
	if err != nil {
		zap.L().Error("发送表情包失败", zap.Int64("group_id", groupID), zap.String("file_name", fileName), zap.Error(err))
		return err
	}
	tools.GetToolContext(ctx).MarkActionSucceeded()

	msg := &onebot.ConversationMessage{
		ConversationKind: memory.ConversationKindGroup,
		MessageID:        msgID,
		TargetID:         groupID,
		UserID:           a.bot.GetSelfID(),
		Nickname:         a.persona.GetName(),
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
	tools.GetToolContext(ctx).MarkActionSucceeded()
	a.onMessage(&onebot.ConversationMessage{
		ConversationKind: memory.ConversationKindGroup,
		MessageID:        msgID,
		TargetID:         groupID,
		UserID:           a.bot.GetSelfID(),
		Nickname:         a.persona.GetName(),
		MessageParts:     []onebot.MessagePart{{Kind: "image", Index: 0}},
		Images:           []onebot.ImageInfo{{URL: imageURL}},
		Time:             time.Now(),
		ArrivalSeq:       arrivalSeq,
	})
	return nil
}
