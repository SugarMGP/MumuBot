package agent

import (
	"context"
	"strings"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/persona"
	"mumu-bot/internal/tools"

	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

func (a *Agent) onPrivateMessage(msg *onebot.ConversationMessage) {
	a.privateCommits.enqueue(msg.TargetID, msg.ArrivalSeq, commitItem{msg: msg})
}

func (a *Agent) addPrivateBuffer(msg *onebot.ConversationMessage) {
	a.privateMu.Lock()
	defer a.privateMu.Unlock()
	messages, trimmedSeq := appendMessageBuffer(a.privateBuffers[msg.TargetID], msg, messageBufferLimit())
	a.privateBuffers[msg.TargetID] = messages
	if trimmedSeq > 0 {
		a.privateTrimmedSeq[msg.TargetID] = max(a.privateTrimmedSeq[msg.TargetID], trimmedSeq)
	}
	if msg.MessageID != 0 && msg.UserID == a.bot.GetSelfID() {
		// 自身发言是会话边界：该序号及此前的消息不再留到下一轮
		a.privateReadSeq[msg.TargetID] = max(a.privateReadSeq[msg.TargetID], msg.ArrivalSeq)
	}
}

func (a *Agent) schedulePrivateThink(targetID int64, receivedAt time.Time) {
	a.privateMu.Lock()
	defer a.privateMu.Unlock()
	if a.privateRunning[targetID] || a.privateStopped || a.stopping.Load() || a.ctx.Err() != nil {
		return
	}
	pending := a.pendingPrivateThinks[targetID]
	if pending == nil {
		pending = &pendingThink{}
		a.pendingPrivateThinks[targetID] = pending
	}
	delay := remainingDebounce(receivedAt, time.Now(), time.Duration(config.Get().Agent.ThinkDebounceMS)*time.Millisecond)
	pending.resetTimer(delay, func(p *pendingThink, generation uint64) {
		a.flushPrivateThink(targetID, p, generation)
	})
}

func (a *Agent) flushPrivateThink(targetID int64, expected *pendingThink, generation uint64) {
	a.privateMu.Lock()
	pending := a.pendingPrivateThinks[targetID]
	// 挂起的思考只能由最新一代定时器触发；进入 running 前同样持有锁，
	// schedulePrivateThink 会在 running 期间跳过创建挂起项，因此这里无需复查
	if pending != expected || pending.generation != generation || a.privateStopped || a.stopping.Load() || a.ctx.Err() != nil {
		a.privateMu.Unlock()
		return
	}
	delete(a.pendingPrivateThinks, targetID)
	a.privateRunning[targetID] = true
	a.thinkWG.Add(1)
	a.privateMu.Unlock()
	a.thinkPrivate(targetID)
}

func (a *Agent) thinkPrivate(targetID int64) {
	defer a.thinkWG.Done()
	defer func() {
		a.privateMu.Lock()
		delete(a.privateRunning, targetID)
		a.privateMu.Unlock()
	}()
	if !a.conversationAllowed(memory.ConversationKindPrivate, targetID) {
		return
	}
	cfg := config.Get()
	selfID := a.bot.GetSelfID()
	allMessages, readSeq, _ := a.getConversationSnapshot(memory.ConversationKindPrivate, targetID)
	_, messages := splitMessageSnapshot(allMessages, readSeq, selfID)
	if latestMessageWithID(messages) == nil {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, time.Duration(cfg.Agent.ThinkTimeoutSeconds)*time.Second)
	defer cancel()
	mem := a.memory.WithContext(ctx)
	snapshotMessageID := int64(0)
	if latest := latestMessageWithID(allMessages); latest != nil {
		snapshotMessageID = latest.MessageID
	}
	toolCtx := a.buildConversationToolContext(ctx, memory.ConversationKindPrivate, targetID, snapshotMessageID, allMessages)
	tc := tools.GetToolContext(toolCtx)
	chatContext := a.renderChatContext(allMessages, readSeq, tc)
	if chatContext == "" || (collectTextContext(messages) == "" && !hasDisplayContext(messages)) {
		// 本轮没有任何可读内容时不调用模型，只消费快照水位，避免后续调度反复空转
		a.privateMu.Lock()
		a.privateReadSeq[targetID] = max(a.privateReadSeq[targetID], tc.ThinkStartArrivalSeq)
		a.privateMu.Unlock()
		return
	}
	var relatedMemories []memory.KnowledgeItem
	var memoryRelations []memory.KnowledgeRelation
	var snapshotUpper uint
	if log, err := mem.GetMessageLogByScope(memory.ConversationKindPrivate, targetID, snapshotMessageID); err == nil {
		snapshotUpper = log.ID
		if query, err := a.memory.PrepareHybridQuery(toolCtx, collectTextFragments(messages)); err == nil {
			relatedMemories, _, memoryRelations = a.buildConversationMemoryContext(toolCtx, memory.ConversationKindPrivate, targetID, allMessages, query, snapshotUpper)
		}
	}
	friendName := ""
	for _, msg := range messages {
		if nickname := strings.TrimSpace(msg.Nickname); nickname != "" {
			friendName = nickname
		}
	}
	privateCtx := &persona.PrivatePromptContext{
		TargetID:           targetID,
		FriendName:         friendName,
		WorkingNote:        nil,
		TopicSummary:       "",
		RelatedMemories:    relatedMemories,
		MemoryRelations:    memoryRelations,
		SelfID:             selfID,
		MemorySubjectNames: a.memorySubjectNames(ctx, relatedMemories),
	}
	if note, err := a.memory.GetWorkingNoteScope(ctx, memory.ConversationKindPrivate, targetID); err == nil {
		privateCtx.WorkingNote = note
	}
	if snapshotUpper > 0 {
		if topic, err := a.memory.GetPrivateTopicStateAt(toolCtx, targetID, snapshotUpper); err == nil && topic != nil && strings.TrimSpace(topic.SummaryJSON) != "" {
			privateCtx.TopicSummary = topic.SummaryText()
		}
	}
	if mood, err := mem.GetMoodState(); err == nil {
		privateCtx.MoodState = &persona.MoodInfo{Valence: mood.Valence, Energy: mood.Energy, Sociability: mood.Sociability}
	}
	if profile, err := mem.GetMemberProfile(targetID); err == nil && profile != nil {
		info := &persona.FriendProfileInfo{
			Nickname:      strings.TrimSpace(profile.Nickname),
			Intimacy:      profile.Intimacy,
			IntimacyLevel: memory.IntimacyLevel(profile.Intimacy),
			IntimacyName:  memory.IntimacyLevelName(profile.Intimacy),
			LastSeenAt:    profile.LastSeenAt,
			MessageCount:  profile.MessageCount,
		}
		if names, err := mem.ListMemberNames(targetID); err == nil {
			for _, name := range names {
				if value := strings.TrimSpace(name.Value); value != "" {
					info.Aliases = append(info.Aliases, persona.FriendAliasInfo{Value: value, GroupID: name.GroupID})
				}
			}
		}
		privateCtx.FriendProfile = info
	}
	thinkPrompt := a.persona.GetPrivateThinkPrompt(privateCtx, chatContext)
	systemPrompt := a.persona.GetPrivateSystemPrompt()
	if cfg.Debug.ShowPrompt {
		zap.L().Debug("私聊系统提示词", zap.String("prompt", systemPrompt))
		zap.L().Debug("私聊思考提示词", zap.String("prompt", thinkPrompt))
	}
	result, err := a.privateReact.Generate(toolCtx, []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(thinkPrompt),
	}, chatOptions()...)
	a.privateMu.Lock()
	a.privateReadSeq[targetID] = max(a.privateReadSeq[targetID], tc.ReadThroughSeq(err == nil && result != nil))
	a.privateMu.Unlock()
	if err != nil || result == nil {
		zap.L().Warn("私聊思考失败", zap.Int64("target_id", targetID), zap.Error(err), zap.Bool("empty_result", result == nil))
		return
	}
	if cfg.Debug.ShowThinking && result.Content != "" {
		zap.L().Debug("私聊 Agent 输出", zap.Int64("target_id", targetID), zap.String("content", result.Content))
	}
}

// stopPrivateTimers 停止私聊防抖定时器并禁止新的私聊思考，供停机排空使用
func (a *Agent) stopPrivateTimers() {
	a.privateMu.Lock()
	defer a.privateMu.Unlock()
	a.privateStopped = true
	for targetID, pending := range a.pendingPrivateThinks {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		delete(a.pendingPrivateThinks, targetID)
	}
}

func (a *Agent) doPrivateSpeak(ctx context.Context, targetID int64, content string, replyTo int64) error {
	messageID, seq, err := a.bot.SendPrivateMessage(ctx, targetID, content, replyTo)
	if err != nil {
		return err
	}
	tools.GetToolContext(ctx).MarkActionSucceeded()
	msg := &onebot.ConversationMessage{ConversationKind: memory.ConversationKindPrivate, MessageID: messageID, TargetID: targetID, UserID: a.bot.GetSelfID(), Nickname: a.persona.GetName(), Content: content, MessageParts: []onebot.MessagePart{{Kind: "text", Text: content}}, Time: time.Now(), ReceivedAt: time.Now(), ArrivalSeq: seq}
	if replyTo != 0 {
		msg.Reply = &onebot.ReplyInfo{MessageID: replyTo}
	}
	a.onMessage(msg)
	return nil
}

func (a *Agent) doPrivateSticker(ctx context.Context, targetID int64, filePath string, description string) error {
	messageID, seq, err := a.bot.SendPrivateImageMessage(ctx, targetID, filePath, true)
	if err != nil {
		zap.L().Error("发送私聊表情包失败", zap.Int64("target_id", targetID), zap.String("path", filePath), zap.Error(err))
		return err
	}
	tools.GetToolContext(ctx).MarkActionSucceeded()
	a.onMessage(&onebot.ConversationMessage{ConversationKind: memory.ConversationKindPrivate, MessageID: messageID, TargetID: targetID, UserID: a.bot.GetSelfID(), Nickname: a.persona.GetName(), Images: []onebot.ImageInfo{{SubType: 1, Desc: description}}, MessageParts: []onebot.MessagePart{{Kind: "image", Index: 0}}, Time: time.Now(), ReceivedAt: time.Now(), ArrivalSeq: seq})
	return nil
}

func (a *Agent) doPrivateImage(ctx context.Context, targetID int64, imageURL string) error {
	messageID, seq, err := a.bot.SendPrivateImageMessage(ctx, targetID, imageURL, false)
	if err != nil {
		return err
	}
	tools.GetToolContext(ctx).MarkActionSucceeded()
	a.onMessage(&onebot.ConversationMessage{ConversationKind: memory.ConversationKindPrivate, MessageID: messageID, TargetID: targetID, UserID: a.bot.GetSelfID(), Nickname: a.persona.GetName(), Images: []onebot.ImageInfo{{URL: imageURL}}, MessageParts: []onebot.MessagePart{{Kind: "image", Index: 0}}, Time: time.Now(), ReceivedAt: time.Now(), ArrivalSeq: seq})
	return nil
}
