package agent

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"mumu-bot/internal/config"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/persona"
	"mumu-bot/internal/tools"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
	flowagent "github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

func (a *Agent) thinkLoop() {
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
	cfg := config.Get()
	selfID := a.bot.GetSelfID()
	for _, gc := range cfg.Groups {
		if !gc.Enabled {
			continue
		}
		msgs, lastRead := a.getMessageSnapshot(gc.GroupID)
		if len(msgs) == 0 {
			continue
		}
		_, currentMessages := splitMessageSnapshot(msgs, lastRead, selfID)
		if len(currentMessages) == 0 {
			continue
		}
		receivedAt := currentMessages[len(currentMessages)-1].ReceivedAt
		if a.hasStrongInteraction(currentMessages) {
			a.scheduleThink(gc.GroupID, true, false, receivedAt)
			continue
		}
		lastMsg := latestPositiveMessage(currentMessages)
		if lastMsg == nil {
			continue
		}

		if time.Since(lastMsg.Time) > time.Duration(cfg.Agent.ObserveWindow)*time.Second {
			continue
		}
		speakProb := a.getSpeakProbability(gc.GroupID)
		if rand.Float64() > speakProb {
			continue
		}
		a.scheduleThink(gc.GroupID, false, true, receivedAt)
	}
}

func (a *Agent) scheduleThink(groupID int64, isMention, probabilityPassed bool, receivedAt time.Time) {
	if a.concurrencyMgr.IsRunning(groupID) {
		zap.L().Debug("群思考正在执行，忽略新触发", zap.Int64("group_id", groupID))
		return
	}
	debounce := time.Duration(config.Get().Agent.ThinkDebounceMS) * time.Millisecond
	delay := remainingDebounce(receivedAt, time.Now(), debounce)

	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	if pending, ok := a.pendingThinks[groupID]; ok {
		pending.probabilityPassed = pending.probabilityPassed || probabilityPassed
		pending.generation++
		gen := pending.generation
		if pending.timer != nil {
			pending.timer.Stop()
		}
		pending.timer = time.AfterFunc(delay, func() {
			a.flushPendingThink(groupID, gen)
		})
		return
	}

	if !probabilityPassed && !isMention {
		return
	}

	pending := &pendingThink{
		probabilityPassed: probabilityPassed,
		generation:        1,
	}
	gen := pending.generation
	pending.timer = time.AfterFunc(delay, func() {
		a.flushPendingThink(groupID, gen)
	})
	a.pendingThinks[groupID] = pending
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

func (a *Agent) flushPendingThink(groupID int64, generation uint64) {
	a.pendingMu.Lock()
	pending, ok := a.pendingThinks[groupID]
	if !ok || pending.generation != generation {
		a.pendingMu.Unlock()
		return
	}

	delete(a.pendingThinks, groupID)
	a.pendingMu.Unlock()

	a.concurrencyMgr.Submit(groupID, pending.probabilityPassed)
}

func (a *Agent) clearPendingThinks() {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()

	for groupID, pending := range a.pendingThinks {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		delete(a.pendingThinks, groupID)
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
		count, err := a.memory.GetMessageCountByTime(groupID, a.bot.GetSelfID(), startTime)
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

func (a *Agent) think(groupID int64, probabilityPassed bool) {
	if err := a.ctx.Err(); err != nil {
		return
	}
	if a.bot.IsSelfMuted(groupID) {
		return
	}
	cfg := config.Get()
	selfID := a.bot.GetSelfID()

	buffer, lastReadMessage := a.getMessageSnapshot(groupID)
	readMessages, currentMessages := splitMessageSnapshot(buffer, lastReadMessage, selfID)
	if len(currentMessages) == 0 {
		return
	}
	isMention := a.hasStrongInteraction(currentMessages)
	var snapshotMessage *onebot.GroupMessage
	if len(buffer) > 0 {
		snapshotMessage = buffer[len(buffer)-1]
	}
	semanticCurrent := collectTextContext(currentMessages) != ""
	hasCurrentContext := semanticCurrent || strings.TrimSpace(renderChatContext(currentMessages, nil, selfID)) != ""
	if !isMention && !probabilityPassed {
		if !hasCurrentContext {
			a.commitReadSnapshot(groupID, snapshotMessage)
		}
		return
	}
	snapshotMessageID := int64(0)
	if message := latestPositiveMessage(buffer); message != nil {
		snapshotMessageID = message.MessageID
	}
	evidenceMessageID := int64(0)
	for i := len(currentMessages) - 1; i >= 0; i-- {
		msg := currentMessages[i]
		if msg != nil && msg.MessageID > 0 && msg.UserID != selfID && strings.TrimSpace(msg.Content) != "" {
			evidenceMessageID = msg.MessageID
			break
		}
	}

	ctx := a.buildToolContext(a.ctx, groupID, snapshotMessageID, evidenceMessageID)
	tc := tools.GetToolContext(ctx)

	chatContext := renderChatContext(buffer, lastReadMessage, selfID)
	if chatContext == "" {
		return
	}

	promptCtx := &persona.PromptContext{
		GroupID: groupID,
	}
	promptCtx.GroupInfo = a.buildGroupContext(groupID)

	if !semanticCurrent {
		if !hasCurrentContext {
			a.commitReadSnapshot(groupID, snapshotMessage)
			return
		}
	}

	if semanticCurrent && snapshotMessageID > 0 {
		retrievalQuery, retrievalErr := a.memory.PrepareHybridQuery(ctx, collectRetrievalTextFragments(readMessages, currentMessages, cfg.Agent.MessageBufferSize))
		if retrievalErr != nil {
			zap.L().Warn("构建原始上下文检索查询失败", zap.Int64("group_id", groupID), zap.Error(retrievalErr))
		}
		snapshotLog, snapshotErr := a.memory.GetMessageLogByID(snapshotMessageID)
		if snapshotErr != nil {
			zap.L().Warn("读取话题工作记忆快照上界失败", zap.Int64("group_id", groupID), zap.Int64("message_id", snapshotMessageID), zap.Error(snapshotErr))
		} else {
			topicPrompt, err := a.topicMgr.BuildPromptContext(ctx, groupID, retrievalQuery, snapshotLog.ID, replyMessageIDs(currentMessages))
			if err != nil {
				zap.L().Warn("构建话题工作记忆失败", zap.Int64("group_id", groupID), zap.Error(err))
			} else {
				promptCtx.TopicMemory = topicPrompt
			}
		}

		promptCtx.RelatedMemories, promptCtx.CrossGroupExperiences = a.buildMemoryContext(ctx, groupID, retrievalQuery)
	}

	if mood, err := a.memory.GetMoodState(); err == nil {
		promptCtx.MoodState = &persona.MoodInfo{
			Valence:     mood.Valence,
			Energy:      mood.Energy,
			Sociability: mood.Sociability,
		}
	}

	if semanticCurrent && a.jargonMgr != nil {
		promptCtx.JargonMatches = a.jargonMgr.Match(groupID, collectTextContext(currentMessages))
	}
	recentPeople := a.buildRecentPeopleContext(buffer, groupID)

	systemPrompt := a.persona.GetSystemPrompt()

	groupExtra := ""
	if gc := cfg.GetGroupConfig(groupID); gc != nil && gc.ExtraPrompt != "" {
		groupExtra = gc.ExtraPrompt
	}

	thinkPrompt := a.persona.GetThinkPrompt(promptCtx, chatContext, groupExtra, recentPeople)
	if isMention {
		thinkPrompt += "\n\n注意：有人提到你了，可能在找你说话，你可以看情况回复。"
	}

	if cfg.Debug.ShowPrompt {
		zap.L().Debug("系统提示词", zap.String("prompt", systemPrompt))
		zap.L().Debug("思考提示词", zap.String("prompt", thinkPrompt))
	}

	msgs := []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(thinkPrompt),
	}

	ctxWithTimeout, cancelTimeout := context.WithTimeout(ctx, agentThinkTimeout)
	defer cancelTimeout()

	opts := make([]flowagent.AgentOption, 0, 1)
	if cfg.Debug.ShowToolCalls {
		opts = append(opts, flowagent.WithComposeOptions(compose.WithCallbacks(tools.NewToolLogHandler())))
	}

	result, err := a.react.Generate(ctxWithTimeout, msgs, opts...)
	if err != nil {
		if errors.Is(ctxWithTimeout.Err(), context.DeadlineExceeded) {
			zap.L().Warn("思考超时", zap.Int64("group_id", groupID), zap.Duration("timeout", agentThinkTimeout))
		} else if errors.Is(ctxWithTimeout.Err(), context.Canceled) || errors.Is(a.ctx.Err(), context.Canceled) {
			zap.L().Debug("思考已取消", zap.Int64("group_id", groupID))
		} else {
			zap.L().Error("思考失败", zap.Int64("group_id", groupID), zap.Error(err))
		}
		if shouldCommitReadSnapshot(err, tc != nil && tc.Acted()) {
			a.commitReadSnapshot(groupID, snapshotMessage)
		}
		return
	}
	a.commitReadSnapshot(groupID, snapshotMessage)

	if cfg.Debug.ShowThinking && result != nil && result.Content != "" {
		zap.L().Debug("Agent 输出", zap.Int64("group_id", groupID), zap.String("content", result.Content))
	}
}

func replyMessageIDs(messages []*onebot.GroupMessage) []int64 {
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

func shouldCommitReadSnapshot(generateErr error, acted bool) bool {
	return generateErr == nil || acted
}

func (a *Agent) hasStrongInteraction(messages []*onebot.GroupMessage) bool {
	for _, message := range messages {
		if message != nil && (message.IsMentioned || a.persona.IsMentioned(message.Content)) {
			return true
		}
	}
	return false
}

func latestPositiveMessage(messages []*onebot.GroupMessage) *onebot.GroupMessage {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] != nil && messages[i].MessageID > 0 {
			return messages[i]
		}
	}
	return nil
}

func (a *Agent) commitReadSnapshot(groupID int64, message *onebot.GroupMessage) {
	if message == nil {
		return
	}
	a.buffersMu.Lock()
	defer a.buffersMu.Unlock()
	if message.MessageID > 0 {
		for _, current := range a.buffers[groupID] {
			if current != nil && current.MessageID == message.MessageID {
				message = current
				break
			}
		}
	}
	a.lastReadMessage[groupID] = message
}

func (a *Agent) buildToolContext(ctx context.Context, groupID, snapshotMessageID, evidenceMessageID int64) context.Context {
	return tools.WithToolContext(ctx, &tools.ToolContext{
		GroupID:           groupID,
		MemoryMgr:         a.memory,
		Bot:               a.bot,
		SnapshotMessageID: snapshotMessageID,
		EvidenceMessageID: evidenceMessageID,
		SpeakCallback: func(callCtx context.Context, gid int64, content string, replyTo int64, mentions []int64) (int64, error) {
			return a.doSpeak(callCtx, gid, content, replyTo, mentions)
		},
		SendStickerCallback: func(callCtx context.Context, gid int64, filePath string, description string) (int64, error) {
			return a.doSendSticker(callCtx, gid, filePath, description)
		},
		MessageRecalledCallback: a.syncRecalledMessage,
	})
}

func (a *Agent) doSpeak(ctx context.Context, groupID int64, content string, replyTo int64, mentions []int64) (int64, error) {
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
			return 0, ctx.Err()
		case <-timer.C:
		}
	}

	msgID, err := a.bot.SendGroupMessage(ctx, groupID, content, replyTo, mentions)
	if err != nil {
		zap.L().Error("发言失败", zap.Int64("group_id", groupID), zap.Error(err))
		return 0, err
	}

	msg := &onebot.GroupMessage{
		MessageID: msgID,
		GroupID:   groupID,
		UserID:    a.bot.GetSelfID(),
		Nickname:  a.persona.GetName(),
		Content:   content,
		Time:      time.Now(),
	}

	if replyTo > 0 {
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
	return msgID, nil
}

func (a *Agent) doSendSticker(ctx context.Context, groupID int64, filePath string, description string) (int64, error) {
	msgID, err := a.bot.SendImageMessage(ctx, groupID, filePath, true)
	if err != nil {
		zap.L().Error("发送表情包失败", zap.Int64("group_id", groupID), zap.String("path", filePath), zap.Error(err))
		return 0, err
	}

	msg := &onebot.GroupMessage{
		MessageID: msgID,
		GroupID:   groupID,
		UserID:    a.bot.GetSelfID(),
		Nickname:  a.persona.GetName(),
		Content:   "",
		Time:      time.Now(),
		Images: []onebot.ImageInfo{
			{
				SubType: 1,
				Desc:    description,
			},
		},
	}
	a.onMessage(msg)
	zap.L().Info("发送表情包成功", zap.Int64("group_id", groupID), zap.String("desc", description))
	return msgID, nil
}
