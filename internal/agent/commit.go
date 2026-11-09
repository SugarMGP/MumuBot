package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/config"

	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"

	"github.com/bytedance/sonic"
	"github.com/jellydator/ttlcache/v3"
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

// SkipConversationEvent 消费未处理事件的到达序号，避免提交重排器等待永久缺口
func (a *Agent) SkipConversationEvent(kind string, targetID int64, arrivalSeq uint64) {
	item := commitItem{skip: true}
	if kind == memory.ConversationKindPrivate {
		a.privateCommits.enqueue(targetID, arrivalSeq, item)
	} else {
		a.groupCommits.enqueue(targetID, arrivalSeq, item)
	}
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
func (a *Agent) persistConversationMessage(ctx context.Context, msg *onebot.ConversationMessage, mediaRefs []media.Ref, forwards []forwardDraft, user *sessionUser) (*memory.MessageLog, bool, error) {
	var log *memory.MessageLog
	created := false
	err := a.memory.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if msg.MessageID == 0 || msg.UserID != a.bot.GetSelfID() {
			if err := memory.LockConversationAllowed(tx, msg.ConversationKind, msg.TargetID); err != nil {
				return err
			}
		}
		if msg.MessageID != 0 {
			log = &memory.MessageLog{
				ConversationKind: msg.ConversationKind, OneBotMessageID: msg.MessageID,
				TargetID: msg.TargetID, UserID: msg.UserID, Nickname: msg.Nickname,
				TextContent: msg.Content, DisplayContent: msg.FinalContent,
				IsMentioned: msg.IsMentioned, MessageTime: msg.Time, MediaRefs: mediaRefs, FileRefs: msg.Files,
			}
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
			if err := saveMessageForwards(tx, log, forwards); err != nil {
				return err
			}
		} else {
			created = true
		}
		if user == nil {
			return nil
		}
		if log != nil {
			user.LogID = log.ID
		}
		payload, err := sonic.Marshal(user.Msg)
		if err != nil {
			return err
		}
		entry, err := a.memory.AppendUserTranscript(tx, msg.ConversationKind, msg.TargetID, user.LogID, payload, "")
		if err != nil {
			return err
		}
		user.TranscriptID = entry.ID
		tagTranscriptEntryID(user.Msg, entry.ID)
		return nil
	})
	return log, created, err
}

// saveMessageForwards 把合并转发内容跟着消息一起落库，父级先于嵌套写入
func saveMessageForwards(tx *gorm.DB, log *memory.MessageLog, drafts []forwardDraft) error {
	if log == nil || len(drafts) == 0 {
		return nil
	}
	rows := make([]memory.MessageForward, 0, len(drafts))
	for _, draft := range drafts {
		var parent *string
		if draft.Parent != "" {
			parent = &draft.Parent
		}
		rows = append(rows, memory.MessageForward{
			Handle:       draft.Handle,
			MessageLogID: log.ID,
			ParentHandle: parent,
			Nodes:        draft.Nodes,
		})
	}
	return memory.SaveMessageForwards(tx, rows)
}

// commitConversationMessage 群私消息、观察与重复事件共用持久化、撤回补偿及发布边界
func (a *Agent) commitConversationMessage(item commitItem) {
	msg := item.msg
	if msg == nil {
		return
	}
	ctx, cancel := a.persistenceContext()
	defer cancel()
	replyErr := a.refreshReplyRecall(ctx, msg)
	if replyErr != nil {
		zap.L().Warn("核对引用撤回状态失败，省略引用原文", zap.String("conversation_kind", msg.ConversationKind), zap.Int64("target_id", msg.TargetID), zap.Int64("message_id", msg.MessageID), zap.Error(replyErr))
	}
	wakeCommand := a.isWakeCommand(msg)
	selfID := a.bot.GetSelfID()
	self := msg.MessageID != 0 && msg.UserID == selfID
	sleeping := a.qqSleeping() && msg.UserID != selfID
	var log *memory.MessageLog
	var created bool
	persist := func(user *sessionUser) (bool, error) {
		var err error
		log, created, err = a.persistConversationMessage(ctx, msg, item.media, item.forwards, user)
		return created, err
	}
	var err error
	if !self && !sleeping && !wakeCommand {
		display := a.applyPendingRecall(ctx, msg)
		err = a.sessions.get(msg.ConversationKind, msg.TargetID).AppendUser(display, item.media, persist)
	} else {
		_, err = persist(nil)
	}
	if errors.Is(err, memory.ErrConversationUnavailable) {
		return
	}
	if err != nil {
		zap.L().Error("提交消息失败", zap.String("conversation_kind", msg.ConversationKind), zap.Int64("target_id", msg.TargetID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
		return
	}
	if log != nil && log.RecalledAt != nil {
		msg = recalledConversationMessage(msg)
		a.markSessionRecalled(msg.ConversationKind, msg.TargetID, msg.MessageID)
	}
	if msg.MessageID != 0 {
		msg = a.applyPendingRecall(ctx, msg)
	}
	if !created {
		return
	}
	if wakeCommand {
		a.handleWakeCommand(ctx, msg)
		return
	}
	// 睡眠期间只保留原文、媒体和转发：不进入 QQ 工作集，也不启动新回合
	if sleeping {
		a.updateMember(ctx, msg)
		return
	}
	// 引用缓存只由按序提交写入，避免并行解析在撤回后写回旧值
	if replyErr == nil && msg.Reply != nil && msg.Reply.MessageID != 0 {
		a.replyCache.Set(replyCacheKey(msg.ConversationKind, msg.TargetID, msg.Reply.MessageID), *msg.Reply, ttlcache.DefaultTTL)
	}
	if msg.MessageID == 0 || self || a.stopping.Load() || a.ctx.Err() != nil {
		return
	}
	a.afterMessagePersisted(ctx, msg)
	if msg.ConversationKind == memory.ConversationKindPrivate {
		a.schedulePrivateThink(msg.TargetID, msg.ReceivedAt)
	} else {
		a.scheduleGroupThink(msg.TargetID, msg.ReceivedAt)
	}
}

// commitItem 提交队列项：消息、戳一戳、撤回统一按会话内到达序号重排提交；
// skip 用于消费不会产生实际处理的序号（解析失败、无效事件、未启用会话），避免重排器死等
type commitItem struct {
	skip     bool
	recall   *recallCommit
	msg      *onebot.ConversationMessage
	media    []media.Ref
	forwards []forwardDraft
}

// commitOne 群聊与私聊提交队列共用的分发逻辑，会话隔离由各自队列实例保证
func (a *Agent) commitOne(item commitItem) {
	switch {
	case item.skip:
	case item.recall != nil:
		a.commitRecall(item.recall)
	default:
		a.commitConversationMessage(item)
	}
}

func (a *Agent) isWakeCommand(msg *onebot.ConversationMessage) bool {
	if msg == nil || msg.ConversationKind != memory.ConversationKindPrivate || msg.UserID != config.Snapshot().Identity.MasterUserID {
		return false
	}
	if strings.TrimSpace(msg.Content) != "/wake" || len(msg.MessageParts) == 0 {
		return false
	}
	for _, part := range msg.MessageParts {
		if part.Kind != "text" {
			return false
		}
	}
	return true
}

// handleWakeCommand 处理已经在入口确认的主人私聊纯文本 /wake，不启动普通 Private ReAct
func (a *Agent) handleWakeCommand(ctx context.Context, msg *onebot.ConversationMessage) {
	state, err := a.memory.GetLifeState(ctx)
	if err != nil {
		zap.L().Warn("处理主人叫醒指令时读取 life 状态失败", zap.Error(err))
		return
	}
	if state.Awake {
		zap.L().Info("life 已经醒着，忽略重复叫醒指令", zap.Int64("user_id", msg.UserID))
		return
	}
	now := time.Now()
	summary := ""
	if state.SleepStartedAt != nil {
		summary = a.sleepSummaryText(ctx, *state.SleepStartedAt, now)
	}
	note := "主人从私聊发出了 /wake。" + "\n" + "真实来源：private:" + fmt.Sprintf("%d", msg.UserID)
	if summary != "" {
		note += "\n" + summary
	}
	// 先静默落信，确保叫醒后不会因为投递失败而丢掉这次醒来；
	// 只有信落稳后才改睡眠状态，最后再显式调度 life
	row, err := a.enqueueSessionMessage(ctx, memory.ConversationKindPrivate, msg.UserID,
		memory.ConversationKindLife, memory.LifeTargetID, note, false, true)
	if err != nil {
		zap.L().Warn("主人叫醒 life 前投信失败", zap.Error(err))
		return
	}
	if err := a.memory.WakeLife(ctx, now); err != nil {
		zap.L().Warn("主人叫醒 life 失败", zap.Error(err))
		a.recordLifeError(context.WithoutCancel(ctx), "wake_failed", err.Error())
		return
	}
	a.applyLifeAwake(ctx, true)
	a.sessions.get(memory.ConversationKindLife, memory.LifeTargetID).MarkAgentMessageWake(row.ID)
	if err := a.memory.MarkSessionMessageWake(context.WithoutCancel(ctx), row.ID); err != nil {
		zap.L().Warn("持久化叫醒消息的 wake 标记失败", zap.Error(err))
	}
	a.scheduleLifeThink()
}
