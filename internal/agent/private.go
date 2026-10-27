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
	ctxWithTimeout, cancel := context.WithTimeout(a.ctx, time.Duration(cfg.Agent.ThinkTimeoutSeconds)*time.Second)
	defer cancel()

	session := a.sessions.get(memory.ConversationKindPrivate, targetID)
	pending := session.PendingMessages()
	if len(pending) == 0 {
		return
	}
	ctx := a.buildConversationToolContext(ctxWithTimeout, memory.ConversationKindPrivate, targetID, session)
	tc := tools.GetToolContext(ctx)
	tc.ThinkStartArrivalSeq = maxPendingSeq(pending)

	messages, err := session.Assemble(ctx, a.memory, sessionBudget())
	if err != nil {
		zap.L().Warn("装配私聊上下文失败", zap.Int64("target_id", targetID), zap.Error(err))
		return
	}
	if runErr := a.runAgent(ctx, a.privateAgent, messages); runErr != nil {
		// 发言成功但轮次失败时也要推进消费水位，避免同一批消息被重复回复
		if seq := tc.ReadThroughSeq(false); seq > 0 {
			session.MarkConsumed(seq)
		}
		zap.L().Warn("私聊思考失败", zap.Int64("target_id", targetID), zap.Error(runErr))
		return
	}
	session.MarkConsumed(maxPendingSeq(pending))
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

func (a *Agent) doPrivateSticker(ctx context.Context, targetID int64, fileName string, description string) error {
	messageID, seq, err := a.bot.SendPrivateImageMessage(ctx, targetID, fileName, true)
	if err != nil {
		zap.L().Error("发送私聊表情包失败", zap.Int64("target_id", targetID), zap.String("file_name", fileName), zap.Error(err))
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
