package agent

import (
	"context"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/tools"

	"go.uber.org/zap"
)

func (a *Agent) onPrivateMessage(msg *onebot.ConversationMessage, mediaRefs []media.Ref, forwards []forwardDraft) {
	a.privateCommits.enqueue(msg.TargetID, msg.ArrivalSeq, commitItem{msg: msg, media: mediaRefs, forwards: forwards})
}

func (a *Agent) schedulePrivateThink(targetID int64, receivedAt time.Time) {
	a.privateMu.Lock()
	defer a.privateMu.Unlock()
	if a.privateStopped || a.stopping.Load() || a.ctx.Err() != nil || a.privateConcurrency.IsRunning(targetID) {
		return
	}
	pending := a.pendingPrivateThinks[targetID]
	if pending == nil {
		pending = &pendingThink{}
		a.pendingPrivateThinks[targetID] = pending
	}
	delay := remainingDebounce(receivedAt, time.Now(), time.Duration(config.Snapshot().Agent.ThinkDebounceMS)*time.Millisecond)
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
	a.privateMu.Unlock()
	a.privateConcurrency.Submit(targetID)
}

func (a *Agent) thinkPrivate(targetID int64) {
	generation := a.acquireGeneration()
	if generation == nil {
		return
	}
	defer generation.wg.Done()
	state, err := newRunState()
	if err != nil {
		zap.L().Warn("加载本轮配置失败", zap.Int64("target_id", targetID), zap.Error(err))
		return
	}
	if !a.conversationAllowed(memory.ConversationKindPrivate, targetID) {
		return
	}
	session := a.sessions.get(memory.ConversationKindPrivate, targetID)
	pending := session.PendingMessages()
	if len(pending) == 0 || !hasActionableMessage(pending) {
		return
	}
	ctx := withRunState(a.ctx, state)
	ctx = withGeneration(ctx, generation)
	ctx = a.buildConversationToolContext(ctx, memory.ConversationKindPrivate, targetID, session)
	tc := tools.GetToolContext(ctx)
	tc.ThinkStartArrivalSeq = maxPendingSeq(pending)

	messages, err := session.Assemble(ctx, a.memory)
	if err != nil {
		zap.L().Warn("装配私聊上下文失败", zap.Int64("target_id", targetID), zap.Error(err))
		return
	}
	if runErr := a.runAgent(ctx, generation.privateAgent, messages); runErr != nil {
		if isContextOverflowError(runErr) && a.compactAfterOverflow(ctx, memory.ConversationKindPrivate, targetID, generation.privateTools, a.privateGenModelInput, session) {
			if retryErr := a.retryAfterOverflow(ctx, memory.ConversationKindPrivate, targetID, generation.privateAgent, session); retryErr == nil {
				return
			}
		}
		zap.L().Warn("私聊思考失败", zap.Int64("target_id", targetID), zap.Error(runErr))
		return
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
	msg := &onebot.ConversationMessage{ConversationKind: memory.ConversationKindPrivate, MessageID: messageID, TargetID: targetID, UserID: a.bot.GetSelfID(), Nickname: a.personaFor(ctx).GetName(), Content: content, MessageParts: []onebot.MessagePart{{Kind: "text", Text: content}}, Time: time.Now(), ReceivedAt: time.Now(), ArrivalSeq: seq}
	if replyTo != 0 {
		msg.Reply = &onebot.ReplyInfo{MessageID: replyTo}
	}
	a.onMessage(msg)
	return nil
}

func (a *Agent) doPrivateSticker(ctx context.Context, targetID int64, fileName string, description string) error {
	messageID, seq, err := a.bot.SendPrivateImageMessage(ctx, targetID, fileName, true)
	if err != nil {
		zap.L().Error("发送私聊表情包失败", zap.Int64("target_id", targetID), zap.String("file_name", fileName), zap.Error(err))
		return err
	}
	a.onMessage(&onebot.ConversationMessage{ConversationKind: memory.ConversationKindPrivate, MessageID: messageID, TargetID: targetID, UserID: a.bot.GetSelfID(), Nickname: a.personaFor(ctx).GetName(), Images: []onebot.ImageInfo{{SubType: 1, Desc: description}}, MessageParts: []onebot.MessagePart{{Kind: "image", Index: 0}}, Time: time.Now(), ReceivedAt: time.Now(), ArrivalSeq: seq})
	return nil
}

func (a *Agent) doPrivateImage(ctx context.Context, targetID int64, imageURL string) error {
	messageID, seq, err := a.bot.SendPrivateImageMessage(ctx, targetID, imageURL, false)
	if err != nil {
		return err
	}
	a.onMessage(&onebot.ConversationMessage{ConversationKind: memory.ConversationKindPrivate, MessageID: messageID, TargetID: targetID, UserID: a.bot.GetSelfID(), Nickname: a.personaFor(ctx).GetName(), Images: []onebot.ImageInfo{{URL: imageURL}}, MessageParts: []onebot.MessagePart{{Kind: "image", Index: 0}}, Time: time.Now(), ReceivedAt: time.Now(), ArrivalSeq: seq})
	return nil
}
