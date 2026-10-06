package agent

import (
	"context"
	"errors"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/topic"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	commitQueueSize    = 256
	pendingCommitSize  = 256
	recallPendingTTL   = 5 * time.Minute
	persistenceTimeout = 30 * time.Second
)

type recallCommit struct {
	kind      string
	targetID  int64
	messageID int64
}

// persistenceContext 已解析事件和成功发言独立于推理取消，每次数据库处理都有期限
func (a *Agent) persistenceContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(a.ctx), persistenceTimeout)
}

func (a *Agent) messageAllowed(ctx context.Context, msg *onebot.ConversationMessage) bool {
	if msg.MessageID != 0 && msg.UserID == a.bot.GetSelfID() {
		return true
	}
	allowed, err := a.memory.ConversationAllowed(ctx, msg.ConversationKind, msg.TargetID)
	if err != nil {
		zap.L().Warn("事件许可检查失败", zap.String("conversation_kind", msg.ConversationKind), zap.Int64("target_id", msg.TargetID), zap.Error(err))
	}
	return err == nil && allowed
}

// persistConversationMessage 在队列消费时锁住会话许可，已成功发送的自身消息必须保留
func (a *Agent) persistConversationMessage(ctx context.Context, msg *onebot.ConversationMessage) (*memory.MessageLog, bool, error) {
	var log *memory.MessageLog
	created := false
	err := a.memory.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if msg.MessageID == 0 || msg.UserID != a.bot.GetSelfID() {
			if err := memory.LockConversationAllowed(tx, msg.ConversationKind, msg.TargetID); err != nil {
				return err
			}
		}
		if msg.MessageID == 0 {
			created = true
			return nil
		}
		if msg.ConversationKind == memory.ConversationKindGroup {
			var err error
			log, created, err = topic.NewManager(tx.Session(&gorm.Session{DisableNestedTransaction: true})).PersistMessage(ctx, msg, msg.IsMentioned)
			return err
		}
		log = &memory.MessageLog{ConversationKind: memory.ConversationKindPrivate, OneBotMessageID: msg.MessageID, TargetID: msg.TargetID, UserID: msg.UserID, Nickname: msg.Nickname, TextContent: msg.Content, DisplayContent: msg.FinalContent, IsMentioned: msg.IsMentioned, MessageTime: msg.Time}
		if msg.Reply != nil && msg.Reply.MessageID != 0 {
			replyTo := msg.Reply.MessageID
			log.ReplyToMessageID = &replyTo
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(log)
		if result.Error != nil {
			return result.Error
		}
		created = result.RowsAffected > 0
		if !created {
			return tx.Where("conversation_kind=? AND target_id=? AND one_bot_message_id=?", msg.ConversationKind, msg.TargetID, msg.MessageID).First(log).Error
		}
		return nil
	})
	return log, created, err
}

// commitConversationMessage 群私消息、观察与重复事件共用持久化、撤回补偿及发布边界
func (a *Agent) commitConversationMessage(msg *onebot.ConversationMessage) {
	if msg == nil {
		return
	}
	ctx, cancel := a.persistenceContext()
	defer cancel()
	log, created, err := a.persistConversationMessage(ctx, msg)
	if errors.Is(err, memory.ErrConversationUnavailable) {
		return
	}
	if err != nil {
		zap.L().Error("提交消息失败", zap.String("conversation_kind", msg.ConversationKind), zap.Int64("target_id", msg.TargetID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
		return
	}
	if log != nil && log.RecalledAt != nil {
		msg = recalledConversationMessage(msg, log)
		a.syncRecalledMessage(log)
	}
	if msg.MessageID != 0 {
		msg = a.applyPendingRecall(ctx, msg)
	}
	if !created {
		return
	}
	if msg.ConversationKind == memory.ConversationKindPrivate {
		a.addPrivateBuffer(msg)
	} else {
		a.addBuffer(msg)
	}
	if msg.MessageID == 0 || msg.UserID == a.bot.GetSelfID() || a.stopping.Load() || a.ctx.Err() != nil {
		return
	}
	a.afterMessagePersisted(ctx, msg)
	if msg.ConversationKind == memory.ConversationKindPrivate {
		a.schedulePrivateThink(msg.TargetID, msg.ReceivedAt)
	} else {
		a.scheduleGroupThink(msg.TargetID, msg.IsMentioned, false, msg.ReceivedAt)
	}
}

// groupCommitItem 提交队列项：消息、戳一戳、撤回统一按群内到达序号重排提交；
// skip 用于消费不会产生实际处理的序号（解析失败、无效事件、未启用群），避免重排器死等
type groupCommitItem struct {
	groupID int64
	skip    bool
	recall  *recallCommit
	msg     *onebot.ConversationMessage
}

// enqueueCommit 把解析完成的消息、撤回或跳过项投入该群提交队列
// 队列满时背压等待；空闲回收由提交队列统一处理
func (a *Agent) enqueueCommit(seq uint64, item groupCommitItem) {
	a.groupCommits.enqueue(item.groupID, seq, item)
}

// enqueueCommitSkip 消费一个不会产生实际处理的到达序号
func (a *Agent) enqueueCommitSkip(groupID int64, seq uint64) {
	a.enqueueCommit(seq, groupCommitItem{groupID: groupID, skip: true})
}
func (a *Agent) commitOne(item groupCommitItem) {
	switch {
	case item.skip:
	case item.recall != nil:
		a.commitRecall(item.recall)
	default:
		a.commitConversationMessage(item.msg)
	}
}
