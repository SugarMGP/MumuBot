package agent

import (
	"context"
	"strconv"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"

	"go.uber.org/zap"
)

// commitRecall 先保留待补偿记录，再用有界事务标记；数据库失败不能丢失撤回请求
func (a *Agent) commitRecall(recall *recallCommit) {
	if recall.kind == "" {
		recall.kind = memory.ConversationKindGroup
	}
	key := recallScopeKey(recall.kind, recall.targetID)
	a.recallMu.Lock()
	if a.pendingRecalls[key] == nil {
		a.pendingRecalls[key] = make(map[int64]time.Time)
	}
	a.pendingRecalls[key][recall.messageID] = time.Now().Add(recallPendingTTL)
	a.recallMu.Unlock()
	ctx, cancel := a.persistenceContext()
	defer cancel()
	a.applyPendingRecall(ctx, &onebot.ConversationMessage{ConversationKind: recall.kind, TargetID: recall.targetID, MessageID: recall.messageID})
}

// applyPendingRecall 仅在数据库确认已撤回后移除待补偿项，不修改已发布的消息指针
func (a *Agent) applyPendingRecall(ctx context.Context, msg *onebot.ConversationMessage) *onebot.ConversationMessage {
	key := recallScopeKey(msg.ConversationKind, msg.TargetID)
	a.recallMu.Lock()
	deadline, ok := a.pendingRecalls[key][msg.MessageID]
	a.recallMu.Unlock()
	if !ok || time.Now().After(deadline) {
		return msg
	}
	mem := a.memory.WithContext(ctx)
	log, changed, err := mem.MarkMessageRecalledScope(msg.ConversationKind, msg.TargetID, msg.MessageID)
	if err == nil && !changed {
		log, err = mem.GetMessageLogByScope(msg.ConversationKind, msg.TargetID, msg.MessageID)
	}
	if err != nil {
		zap.L().Warn("补记消息撤回失败，保留待补偿", zap.String("conversation_kind", msg.ConversationKind), zap.Int64("target_id", msg.TargetID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
		return msg
	}
	if log == nil || log.RecalledAt == nil {
		return msg
	}
	a.recallMu.Lock()
	if a.pendingRecalls[key][msg.MessageID] == deadline {
		delete(a.pendingRecalls[key], msg.MessageID)
		if len(a.pendingRecalls[key]) == 0 {
			delete(a.pendingRecalls, key)
		}
	}
	a.recallMu.Unlock()
	a.syncRecalledMessage(log)
	return recalledConversationMessage(msg, log)
}

func recalledConversationMessage(msg *onebot.ConversationMessage, log *memory.MessageLog) *onebot.ConversationMessage {
	replacement := *msg
	replacement.Content = log.TextContent
	replacement.FinalContent = log.DisplayContent
	replacement.Reply = nil
	replacement.IsMentioned = false
	return &replacement
}

func recallScopeKey(kind string, targetID int64) string {
	return kind + ":" + strconv.FormatInt(targetID, 10)
}

// recallPruneLoop 定期清理过期的待补偿记录，保留有限 TTL
func (a *Agent) recallPruneLoop() {
	defer a.wg.Done()
	ticker := time.NewTicker(recallPendingTTL / 2)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.prunePendingRecalls()
		}
	}
}

func (a *Agent) prunePendingRecalls() {
	now := time.Now()
	a.recallMu.Lock()
	defer a.recallMu.Unlock()
	for key, recalls := range a.pendingRecalls {
		for messageID, deadline := range recalls {
			if now.After(deadline) {
				delete(recalls, messageID)
			}
		}
		if len(recalls) == 0 {
			delete(a.pendingRecalls, key)
		}
	}
}
