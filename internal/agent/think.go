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
	"mumu-bot/internal/utils"
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
	for _, gc := range cfg.Groups {
		if !gc.Enabled {
			continue
		}
		msgs := a.getBuffer(gc.GroupID)
		if len(msgs) == 0 {
			continue
		}

		lastMsg := msgs[len(msgs)-1]

		a.processingMu.RLock()
		lastTime := a.lastProcessedTime[gc.GroupID]
		a.processingMu.RUnlock()
		if !lastTime.IsZero() && lastMsg.Time.Before(lastTime) {
			continue
		}

		if lastMsg.UserID == a.bot.GetSelfID() {
			continue
		}

		if a.persona.IsMentioned(lastMsg.Content) || lastMsg.IsMentioned {
			continue
		}

		if time.Since(lastMsg.Time) > time.Duration(cfg.Agent.ObserveWindow)*time.Second {
			continue
		}
		speakProb := a.getSpeakProbability(gc.GroupID)
		if rand.Float64() > speakProb {
			continue
		}
		a.scheduleThink(gc.GroupID, false, true)
	}
}

func (a *Agent) scheduleThink(groupID int64, isMention bool, fromLoop bool) {
	debounce := time.Duration(config.Get().Agent.ThinkDebounceMS) * time.Millisecond

	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	if pending, ok := a.pendingThinks[groupID]; ok {
		pending.isMention = pending.isMention || isMention
		pending.generation++
		gen := pending.generation
		pending.timer = time.AfterFunc(debounce, func() {
			a.flushPendingThink(groupID, gen)
		})
		return
	}

	if !fromLoop && !isMention {
		return
	}

	pending := &pendingThink{
		isMention:  isMention,
		generation: 1,
	}
	gen := pending.generation
	pending.timer = time.AfterFunc(debounce, func() {
		a.flushPendingThink(groupID, gen)
	})
	a.pendingThinks[groupID] = pending
}

func (a *Agent) flushPendingThink(groupID int64, generation uint64) {
	a.pendingMu.Lock()
	pending, ok := a.pendingThinks[groupID]
	if !ok || pending.generation != generation {
		a.pendingMu.Unlock()
		return
	}

	isMention := pending.isMention
	delete(a.pendingThinks, groupID)
	a.pendingMu.Unlock()

	a.concurrencyMgr.Submit(groupID, isMention)
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

			minProb := utils.ClampFloat64(limitCfg.MinProb, 0, 1)
			baseProb = utils.ClampFloat64(baseProb, minProb, 1)

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

func (a *Agent) think(groupID int64, isMention bool) {
	if err := a.ctx.Err(); err != nil {
		return
	}
	if a.bot.IsSelfMuted(groupID) {
		return
	}
	a.processingMu.Lock()
	if a.processing[groupID] {
		a.processingMu.Unlock()
		return
	}
	a.processing[groupID] = true
	lastProcessedTime := a.lastProcessedTime[groupID]
	a.lastProcessedTime[groupID] = time.Now()
	a.processingMu.Unlock()

	defer func() {
		a.processingMu.Lock()
		a.processing[groupID] = false
		a.processingMu.Unlock()
	}()

	cfg := config.Get()

	buffer := a.getBuffer(groupID)
	latestMessageID := int64(0)
	if len(buffer) > 0 && buffer[len(buffer)-1] != nil {
		latestMessageID = buffer[len(buffer)-1].MessageID
	}

	ctx := a.buildToolContext(a.ctx, groupID, latestMessageID)

	chatContext := a.buildChatContext(buffer, lastProcessedTime)
	if chatContext == "" {
		return
	}

	promptCtx := &persona.PromptContext{
		GroupID: groupID,
	}
	promptCtx.GroupInfo = a.buildGroupContext(groupID)

	classification, err := a.classifyContext(ctx, buffer)
	if err != nil {
		zap.L().Debug("上下文分类失败", zap.Int64("group_id", groupID), zap.Error(err))
	}
	topicQuery := ""
	if classification != nil {
		topicQuery = classification.TopicQuery
	}

	topicPromptCtx, err := a.topicMgr.BuildPromptContext(ctx, groupID, buffer, topicQuery)
	if err != nil {
		zap.L().Warn("构建话题工作记忆失败", zap.Int64("group_id", groupID), zap.Error(err))
	} else {
		promptCtx.TopicMemory = topicPromptCtx.Prompt
	}

	if cfg.Agent.EnableActiveRetrieval {
		promptCtx.RelatedMemories, promptCtx.CrossGroupExperiences = a.buildMemoryContext(ctx, groupID, topicPromptCtx.RetrievalQuery)
	}

	if mood, err := a.memory.GetMoodState(); err == nil {
		promptCtx.MoodState = &persona.MoodInfo{
			Valence:     mood.Valence,
			Energy:      mood.Energy,
			Sociability: mood.Sociability,
		}
	}

	if a.jargonMgr != nil {
		promptCtx.JargonMatches = a.jargonMgr.Match(collectTextContext(buffer))
	}
	promptCtx.StyleHints = a.buildStyleHintContext(groupID, classification)

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
	}

	if cfg.Debug.ShowThinking && result != nil && result.Content != "" {
		zap.L().Debug("Agent 输出", zap.Int64("group_id", groupID), zap.String("content", result.Content))
	}
}

func (a *Agent) buildToolContext(ctx context.Context, groupID, messageID int64) context.Context {
	return tools.WithToolContext(ctx, &tools.ToolContext{
		GroupID:   groupID,
		MemoryMgr: a.memory,
		Bot:       a.bot,
		MessageID: messageID,
		SpeakCallback: func(callCtx context.Context, gid int64, content string, replyTo int64, mentions []int64) (int64, error) {
			return a.doSpeak(callCtx, gid, content, replyTo, mentions)
		},
		SendStickerCallback: func(callCtx context.Context, gid int64, filePath string, description string) (int64, error) {
			return a.doSendSticker(callCtx, gid, filePath, description)
		},
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
		MessageID:   msgID,
		GroupID:     groupID,
		UserID:      a.bot.GetSelfID(),
		Nickname:    a.persona.GetName(),
		Content:     content,
		Time:        time.Now(),
		MessageType: "group",
	}

	if replyTo > 0 {
		msg.Reply = &onebot.ReplyInfo{MessageID: replyTo}
	}

	if len(mentions) > 0 {
		msg.AtList = mentions
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
		MessageID:   msgID,
		GroupID:     groupID,
		UserID:      a.bot.GetSelfID(),
		Nickname:    a.persona.GetName(),
		Content:     "",
		Time:        time.Now(),
		MessageType: "group",
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
