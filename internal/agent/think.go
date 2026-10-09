package agent

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelstats"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/persona"
	"mumu-bot/internal/tools"

	"github.com/cloudwego/eino/compose"
	flowagent "github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/schema"
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
		msgs, lastRead := a.getMessageSnapshot(groupID)
		if len(msgs) == 0 {
			continue
		}
		_, currentMessages := splitMessageSnapshot(msgs, lastRead, a.bot.GetSelfID())
		if len(currentMessages) == 0 {
			continue
		}
		lastMessage := latestMessageWithID(currentMessages)
		if lastMessage == nil {
			continue
		}
		receivedAt := lastMessage.ReceivedAt
		if a.hasStrongInteraction(currentMessages) {
			a.scheduleGroupThink(groupID, true, false, receivedAt)
			continue
		}
		speakProb := a.getSpeakProbability(groupID)
		if rand.Float64() > speakProb {
			continue
		}
		a.scheduleGroupThink(groupID, false, true, receivedAt)
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
		buffer, readSeq, _ := a.getConversationSnapshot(memory.ConversationKindPrivate, target.TargetID)
		_, unread := splitMessageSnapshot(buffer, readSeq, a.bot.GetSelfID())
		if latest := latestMessageWithID(unread); latest != nil {
			a.schedulePrivateThink(target.TargetID, latest.ReceivedAt)
		}
	}
}

func (a *Agent) scheduleGroupThink(groupID int64, isMention, probabilityPassed bool, receivedAt time.Time) {
	debounce := time.Duration(config.Get().Agent.ThinkDebounceMS) * time.Millisecond
	delay := remainingDebounce(receivedAt, time.Now(), debounce)

	a.pendingGroupMu.Lock()
	defer a.pendingGroupMu.Unlock()
	if a.stopping.Load() || a.ctx.Err() != nil || a.groupConcurrency.IsRunning(groupID) {
		return
	}
	pending := a.pendingGroupThinks[groupID]
	if pending == nil {
		if !probabilityPassed && !isMention {
			return
		}
		pending = &pendingThink{}
		a.pendingGroupThinks[groupID] = pending
	}
	pending.probabilityPassed = pending.probabilityPassed || probabilityPassed
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

	a.groupConcurrency.Submit(groupID, pending.probabilityPassed)
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

func (a *Agent) getSpeakProbability(groupID int64) float64 {
	cfg := config.Get()
	baseProb := cfg.Chat.TalkFrequency
	if cfg.Chat.EnableTimeRules && len(cfg.Chat.TimeRules) > 0 {
		now := time.Now()
		hour := now.Hour()
		minute := now.Minute()
		currentMinutes := hour*60 + minute

		for _, rule := range cfg.Chat.TimeRules {
			if rule.GroupID != 0 && rule.GroupID != groupID {
				continue
			}
			var startHour, startMin, endHour, endMin int
			if _, err := fmt.Sscanf(rule.TimeRange, "%d:%d-%d:%d", &startHour, &startMin, &endHour, &endMin); err != nil {
				continue
			}
			startMinutes := startHour*60 + startMin
			endMinutes := endHour*60 + endMin

			if startMinutes <= endMinutes {
				if currentMinutes >= startMinutes && currentMinutes < endMinutes {
					baseProb = rule.TalkValue
					break
				}
			} else {
				if currentMinutes >= startMinutes || currentMinutes < endMinutes {
					baseProb = rule.TalkValue
					break
				}
			}
		}
	}

	limitCfg := cfg.Chat.RateLimit
	if limitCfg.Enabled && limitCfg.PeriodSec > 0 && limitCfg.MaxMessages > 0 {
		startTime := time.Now().Add(-time.Duration(limitCfg.PeriodSec) * time.Second)
		ctx, cancel := a.persistenceContext()
		defer cancel()
		count, err := a.memory.WithContext(ctx).GetMessageCountByTime(groupID, a.bot.GetSelfID(), startTime)
		if err == nil {
			maxMsgs := float64(limitCfg.MaxMessages)
			current := float64(count)

			var decay float64
			if current >= maxMsgs {
				decay = 0
			} else {
				decay = (maxMsgs - current) / maxMsgs
			}

			oldProb := baseProb
			baseProb *= decay

			minProb := min(max(limitCfg.MinProb, 0), oldProb)
			baseProb = min(max(baseProb, minProb), 1)

			if decay < 1.0 {
				zap.L().Debug("触发防话痨限制",
					zap.Int64("group_id", groupID),
					zap.Int64("recent_msgs", count),
					zap.Float64("decay", decay),
					zap.Float64("original_prob", oldProb),
					zap.Float64("new_prob", baseProb))
			}
		}
	}

	return baseProb
}

func (a *Agent) thinkGroup(groupID int64, probabilityPassed bool) {
	if !a.conversationAllowed(memory.ConversationKindGroup, groupID) {
		return
	}
	if a.bot.IsSelfMuted(groupID) {
		return
	}
	cfg := config.Get()
	ctxWithTimeout, cancelTimeout := context.WithTimeout(a.ctx, time.Duration(cfg.Agent.ThinkTimeoutSeconds)*time.Second)
	defer cancelTimeout()
	mem := a.memory.WithContext(ctxWithTimeout)
	selfID := a.bot.GetSelfID()

	buffer, readSeq := a.getMessageSnapshot(groupID)
	readMessages, currentMessages := splitMessageSnapshot(buffer, readSeq, selfID)
	if latestMessageWithID(currentMessages) == nil {
		return
	}
	isMention := a.hasStrongInteraction(currentMessages)
	var snapshotSeq uint64
	for _, msg := range buffer {
		if msg != nil && msg.UserID != selfID {
			snapshotSeq = max(snapshotSeq, msg.ArrivalSeq)
		}
	}
	semanticCurrent := collectTextContext(currentMessages) != ""
	hasCurrentContext := semanticCurrent || hasDisplayContext(currentMessages)
	if !isMention && !probabilityPassed {
		if !hasCurrentContext {
			a.commitReadSnapshot(groupID, snapshotSeq)
		}
		return
	}
	snapshotMessageID := int64(0)
	if message := latestMessageWithID(buffer); message != nil {
		snapshotMessageID = message.MessageID
	}
	ctx := a.buildConversationToolContext(ctxWithTimeout, memory.ConversationKindGroup, groupID, snapshotMessageID, buffer)
	tc := tools.GetToolContext(ctx)

	chatContext := a.renderChatContext(buffer, readSeq, tc)
	// 本轮消息没有可读内容时消费快照水位，避免后续调度反复空转
	if chatContext == "" || !hasCurrentContext {
		a.commitReadSnapshot(groupID, snapshotSeq)
		return
	}

	promptCtx := &persona.GroupPromptContext{}
	promptCtx.GroupInfo = a.buildGroupContext(groupID)
	if note, err := a.memory.GetWorkingNote(ctx, groupID); err != nil {
		zap.L().Warn("读取工作便签失败", zap.Error(err))
	} else {
		promptCtx.WorkingNote = note
	}

	if semanticCurrent && snapshotMessageID != 0 {
		retrievalQuery, retrievalErr := a.memory.PrepareHybridQuery(ctx, collectRetrievalTextFragments(readMessages, currentMessages, cfg.Agent.MessageBufferSize))
		if retrievalErr != nil {
			zap.L().Warn("构建原始上下文检索查询失败", zap.Int64("group_id", groupID), zap.Error(retrievalErr))
		}
		snapshotLog, snapshotErr := mem.GetMessageLogByID(groupID, snapshotMessageID)
		if snapshotErr != nil {
			zap.L().Warn("读取话题工作记忆快照上界失败", zap.Int64("group_id", groupID), zap.Int64("message_id", snapshotMessageID), zap.Error(snapshotErr))
		} else {
			snapshotIDs := make([]int64, 0, len(buffer))
			for _, msg := range buffer {
				if msg != nil && msg.MessageID != 0 {
					snapshotIDs = append(snapshotIDs, msg.MessageID)
				}
			}
			topicPrompt, err := a.topicMgr.BuildPromptContext(ctx, groupID, retrievalQuery, snapshotLog.ID, replyMessageIDs(currentMessages), snapshotIDs)
			if err != nil {
				zap.L().Warn("构建话题工作记忆失败", zap.Int64("group_id", groupID), zap.Error(err))
			} else {
				promptCtx.TopicMemory = topicPrompt
			}
			promptCtx.RelatedMemories, promptCtx.CrossGroupMemories, promptCtx.MemoryRelations = a.buildConversationMemoryContext(ctx, memory.ConversationKindGroup, groupID, buffer, retrievalQuery, snapshotLog.ID)
		}
		promptCtx.SelfID = selfID
		promptCtx.MemorySubjectNames = a.memorySubjectNames(ctx, promptCtx.RelatedMemories, promptCtx.CrossGroupMemories)
	}

	if mood, err := mem.GetMoodState(); err == nil {
		promptCtx.MoodState = &persona.MoodInfo{
			Valence:     mood.Valence,
			Energy:      mood.Energy,
			Sociability: mood.Sociability,
		}
	}

	recentPeople := a.buildRecentPeopleContext(ctx, buffer, groupID)

	systemPrompt := a.persona.GetGroupSystemPrompt()

	groupExtra, extraErr := a.memory.GetGroupExtraPrompt(ctx, groupID)
	if extraErr != nil {
		zap.L().Warn("读取群额外提示词失败", zap.Int64("group_id", groupID), zap.Error(extraErr))
	}

	thinkPrompt := a.persona.GetGroupThinkPrompt(promptCtx, chatContext, groupExtra, recentPeople)
	if isMention {
		thinkPrompt += "\n\n注意：有人提到你了，可能在找你说话，你可以看情况回复。"
	}
	msgs := []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(thinkPrompt),
	}
	if cfg.Debug.ShowPrompt {
		zap.L().Debug("系统提示词", zap.String("prompt", systemPrompt))
		zap.L().Debug("思考提示词", zap.String("prompt", thinkPrompt))
	}

	result, err := a.groupReact.Generate(ctx, msgs, chatOptions()...)
	a.commitReadSnapshot(groupID, tc.ReadThroughSeq(err == nil && result != nil))
	if err != nil {
		if errors.Is(ctxWithTimeout.Err(), context.DeadlineExceeded) {
			zap.L().Warn("思考超时", zap.Int64("group_id", groupID), zap.Int("timeout_seconds", cfg.Agent.ThinkTimeoutSeconds))
		} else if errors.Is(ctxWithTimeout.Err(), context.Canceled) || errors.Is(a.ctx.Err(), context.Canceled) {
			zap.L().Debug("思考已取消", zap.Int64("group_id", groupID))
		} else {
			zap.L().Error("思考失败", zap.Int64("group_id", groupID), zap.Error(err))
		}
		return
	}

	if cfg.Debug.ShowThinking && result != nil && result.Content != "" {
		zap.L().Debug("Agent 输出", zap.Int64("group_id", groupID), zap.String("content", result.Content))
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

// chatOptions 群聊与私聊共用主模型统计和工具日志回调
func chatOptions() []flowagent.AgentOption {
	cfg := config.Get()
	opts := []flowagent.AgentOption{flowagent.WithComposeOptions(compose.WithCallbacks(modelstats.Handler("react", cfg.ModelTiers.High.Model)))}
	if cfg.Debug.ShowToolCalls {
		opts = append(opts, flowagent.WithComposeOptions(compose.WithCallbacks(tools.NewToolLogHandler())))
	}
	return opts
}

func replyMessageIDs(messages []*onebot.ConversationMessage) []int64 {
	seen := make(map[int64]struct{})
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		if message == nil || message.Reply == nil || message.Reply.MessageID == 0 {
			continue
		}
		if _, ok := seen[message.Reply.MessageID]; ok {
			continue
		}
		seen[message.Reply.MessageID] = struct{}{}
		ids = append(ids, message.Reply.MessageID)
	}
	return ids
}

func (a *Agent) hasStrongInteraction(messages []*onebot.ConversationMessage) bool {
	for _, message := range messages {
		if message != nil && message.MessageID != 0 && (message.IsMentioned || a.persona.IsMentioned(message.Content)) {
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

func (a *Agent) commitReadSnapshot(groupID int64, seq uint64) {
	a.groupBuffersMu.Lock()
	defer a.groupBuffersMu.Unlock()
	a.groupReadSeq[groupID] = max(a.groupReadSeq[groupID], seq)
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

func (a *Agent) doSendSticker(ctx context.Context, groupID int64, filePath string, description string) error {
	msgID, arrivalSeq, err := a.bot.SendImageMessage(ctx, groupID, filePath, true)
	if err != nil {
		zap.L().Error("发送表情包失败", zap.Int64("group_id", groupID), zap.String("path", filePath), zap.Error(err))
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
