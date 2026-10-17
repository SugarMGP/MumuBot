package agent

import (
	"context"
	"errors"
	"strconv"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// shortcut: 撤回补偿只保存在当前进程，明确需要跨重启恢复时再持久化
type pendingRecall struct {
	expiresAt time.Time
	queued    bool
}

// refreshReplyRecall 在按序发布前核对并行解析的引用，读取失败只省略引用原文，不丢弃消息正文
func (a *Agent) refreshReplyRecall(ctx context.Context, msg *onebot.ConversationMessage) error {
	if msg.Reply == nil || msg.Reply.MessageID == 0 {
		return nil
	}
	key := recallCommit{kind: msg.ConversationKind, targetID: msg.TargetID, messageID: msg.Reply.MessageID}
	a.recallMu.Lock()
	_, recalled := a.pendingRecalls[key]
	a.recallMu.Unlock()
	var err error
	if !recalled {
		replyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var log memory.MessageLog
		err = a.memory.GetDB().WithContext(replyCtx).Select("recalled_at").Where("conversation_kind=? AND target_id=? AND one_bot_message_id=?", key.kind, key.targetID, key.messageID).Take(&log).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = nil
		}
		recalled = log.RecalledAt != nil
	}
	if recalled || err != nil {
		reply := *msg.Reply
		reply.Content = ""
		if recalled {
			reply.Content = memory.RecalledMessageDisplayContent
		}
		msg.Reply = &reply
	}
	return err
}

// commitRecall 先更新已确认撤回的运行时展示，再登记补偿；所有数据库操作由会话提交协程执行
func (a *Agent) commitRecall(recall *recallCommit) {
	a.recallMu.Lock()
	if _, exists := a.pendingRecalls[*recall]; !exists {
		a.pendingRecalls[*recall] = pendingRecall{expiresAt: time.Now().Add(recallPendingTTL)}
	}
	a.recallMu.Unlock()
	a.syncRecalledMessage(recall.kind, recall.targetID, recall.messageID)
	ctx, cancel := a.persistenceContext()
	defer cancel()
	a.applyPendingRecall(ctx, &onebot.ConversationMessage{ConversationKind: recall.kind, TargetID: recall.targetID, MessageID: recall.messageID})
}

// applyPendingRecall 数据库失败保留待处理；只有确认原消息仍缺失时才按 TTL 清理
func (a *Agent) applyPendingRecall(ctx context.Context, msg *onebot.ConversationMessage) *onebot.ConversationMessage {
	key := recallCommit{kind: msg.ConversationKind, targetID: msg.TargetID, messageID: msg.MessageID}
	a.recallMu.Lock()
	pending, ok := a.pendingRecalls[key]
	a.recallMu.Unlock()
	if !ok {
		return msg
	}
	log, _, err := a.memory.WithContext(ctx).MarkMessageRecalledScope(key.kind, key.targetID, key.messageID)
	if err != nil {
		zap.L().Warn("补记消息撤回失败，等待重试", zap.String("conversation_kind", key.kind), zap.Int64("target_id", key.targetID), zap.Int64("message_id", key.messageID), zap.Error(err))
		return recalledConversationMessage(msg)
	}
	if log != nil || time.Now().After(pending.expiresAt) {
		a.recallMu.Lock()
		delete(a.pendingRecalls, key)
		a.recallMu.Unlock()
	}
	return recalledConversationMessage(msg)
}

func recalledConversationMessage(msg *onebot.ConversationMessage) *onebot.ConversationMessage {
	return &onebot.ConversationMessage{
		ConversationKind: msg.ConversationKind,
		TargetID:         msg.TargetID,
		MessageID:        msg.MessageID,
		UserID:           msg.UserID,
		Nickname:         msg.Nickname,
		GroupCard:        msg.GroupCard,
		Time:             msg.Time,
		ReceivedAt:       msg.ReceivedAt,
		ArrivalSeq:       msg.ArrivalSeq,
		FinalContent:     memory.RecalledMessageDisplayContent,
	}
}

func recallScopeKey(kind string, targetID int64) string {
	return kind + ":" + strconv.FormatInt(targetID, 10)
}

// recallRetryLoop 只投递补偿任务，不与会话提交协程并发修改原文和缓冲
func (a *Agent) recallRetryLoop() {
	defer a.recallWG.Done()
	ticker := time.NewTicker(recallPendingTTL / 2)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.retryPendingRecalls()
		}
	}
}

func (a *Agent) retryPendingRecalls() {
	a.recallMu.Lock()
	var recalls []recallCommit
	for key, pending := range a.pendingRecalls {
		if !pending.queued {
			pending.queued = true
			a.pendingRecalls[key] = pending
			recalls = append(recalls, key)
		}
	}
	a.recallMu.Unlock()
	for _, recall := range recalls {
		if a.ctx.Err() != nil {
			return
		}
		item := commitItem{retry: &recall}
		if recall.kind == memory.ConversationKindPrivate {
			a.privateCommits.enqueue(recall.targetID, 0, item)
		} else {
			a.groupCommits.enqueue(recall.targetID, 0, item)
		}
	}
}
