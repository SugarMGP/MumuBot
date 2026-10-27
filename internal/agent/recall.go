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

// pendingRecall 撤回早于原消息到达时的待补偿记录，等原消息落库后补记
// shortcut: 待补偿记录只保存在当前进程，明确需要跨重启恢复时再持久化
type pendingRecall struct {
	expiresAt time.Time
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

// commitRecall 先更新会话里的撤回标记，再补记数据库；数据库暂时失败只记日志，
// 原消息落库或后续撤回事件还会再补一次，模型上下文里的撤回说明不依赖数据库
func (a *Agent) commitRecall(recall *recallCommit) {
	a.recallMu.Lock()
	now := time.Now()
	a.prunePendingRecallsLocked(now)
	a.pendingRecalls[*recall] = pendingRecall{expiresAt: now.Add(recallPendingTTL)}
	a.recallMu.Unlock()
	a.markSessionRecalled(recall.kind, recall.targetID, recall.messageID)
	ctx, cancel := a.persistenceContext()
	defer cancel()
	a.applyPendingRecall(ctx, &onebot.ConversationMessage{ConversationKind: recall.kind, TargetID: recall.targetID, MessageID: recall.messageID})
}

// applyPendingRecall 原消息已经落库时补记撤回；数据库失败保留待补偿记录，等后续事件再试
func (a *Agent) applyPendingRecall(ctx context.Context, msg *onebot.ConversationMessage) *onebot.ConversationMessage {
	key := recallCommit{kind: msg.ConversationKind, targetID: msg.TargetID, messageID: msg.MessageID}
	a.recallMu.Lock()
	_, ok := a.pendingRecalls[key]
	a.recallMu.Unlock()
	if !ok {
		return msg
	}
	log, _, err := a.memory.WithContext(ctx).MarkMessageRecalledScope(key.kind, key.targetID, key.messageID)
	if err != nil {
		zap.L().Warn("补记消息撤回失败，等待后续事件重试", zap.String("conversation_kind", key.kind), zap.Int64("target_id", key.targetID), zap.Int64("message_id", key.messageID), zap.Error(err))
		return recalledConversationMessage(msg)
	}
	if log != nil {
		a.recallMu.Lock()
		delete(a.pendingRecalls, key)
		a.recallMu.Unlock()
	}
	return recalledConversationMessage(msg)
}

// prunePendingRecallsLocked 清理已经过期的待补偿记录，调用方需已持锁
func (a *Agent) prunePendingRecallsLocked(now time.Time) {
	for key, pending := range a.pendingRecalls {
		if now.After(pending.expiresAt) {
			delete(a.pendingRecalls, key)
		}
	}
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
