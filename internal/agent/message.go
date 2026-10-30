package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"

	"go.uber.org/zap"
)

// onMessage 分流共享消息回调，群聊和私聊入口各自只处理本类会话
func (a *Agent) onMessage(msg *onebot.ConversationMessage) {
	if msg == nil {
		return
	}
	if msg.ConversationKind == "" {
		msg.ConversationKind = memory.ConversationKindGroup
	}
	ctx, cancel := a.persistenceContext()
	defer cancel()
	var mediaRefs []media.Ref
	var forwards []forwardDraft
	if msg.ParseFailed || msg.UserID <= 0 || !a.messageAllowed(ctx, msg) {
		a.SkipConversationEvent(msg.ConversationKind, msg.TargetID, msg.ArrivalSeq)
		return
	}
	if msg.ReceivedAt.IsZero() {
		msg.ReceivedAt = time.Now()
	}
	if msg.MessageID == 0 {
		if !a.onInteractionMessage(msg) {
			a.SkipConversationEvent(msg.ConversationKind, msg.TargetID, msg.ArrivalSeq)
			return
		}
	} else {
		mediaRefs, forwards = a.prepareMessageContent(ctx, msg)
		selfID := a.bot.GetSelfID()
		msg.IsMentioned = msg.IsMentioned || a.persona.IsMentioned(msg.Content) || (msg.Reply != nil && msg.Reply.SenderID == selfID)
	}
	if msg.ConversationKind == memory.ConversationKindPrivate {
		a.onPrivateMessage(msg, mediaRefs, forwards)
		return
	}
	a.onGroupMessage(msg, mediaRefs, forwards)
}

func (a *Agent) onGroupMessage(msg *onebot.ConversationMessage, mediaRefs []media.Ref, forwards []forwardDraft) {
	a.groupCommits.enqueue(msg.TargetID, msg.ArrivalSeq, commitItem{msg: msg, media: mediaRefs, forwards: forwards})
}

// onRecall 撤回事件入口：带到达序号进入提交队列，与会话内消息保持顺序
func (a *Agent) onRecall(kind string, targetID, messageID int64, arrivalSeq uint64) {
	if kind == "" {
		kind = memory.ConversationKindGroup
	}
	if targetID <= 0 {
		return
	}
	if messageID == 0 {
		// 无效撤回同样消费序号，避免会话重排等待永久缺口
		a.SkipConversationEvent(kind, targetID, arrivalSeq)
		return
	}
	// 撤回修正已有原文，不受会话拉黑、退群或数据库暂时不可用影响
	recall := &recallCommit{kind: kind, targetID: targetID, messageID: messageID}
	if kind == memory.ConversationKindPrivate {
		a.privateCommits.enqueue(targetID, arrivalSeq, commitItem{recall: recall})
		return
	}
	a.groupCommits.enqueue(targetID, arrivalSeq, commitItem{recall: recall})
}

// onInteractionMessage 只构造戳一戳的展示内容，返回是否有效；缓冲和思考调度由提交队列统一处理
func (a *Agent) onInteractionMessage(msg *onebot.ConversationMessage) bool {
	if msg.UserID <= 0 || len(msg.AtList) == 0 || msg.AtList[0] <= 0 {
		return false
	}
	targetID := msg.AtList[0]
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	targetName := a.resolveMentionDisplayName(ctx, msg, targetID)
	msg.Content = ""
	msg.IsMentioned = false
	msg.FinalContent = fmt.Sprintf("戳了戳 %s(%d)", targetName, targetID)
	return true
}

func botMentionDisplayName(botName string) string {
	if botName = strings.TrimSpace(botName); botName != "" {
		return botName + "(你)"
	}
	return "机器人(你)"
}

// afterMessagePersisted 只由新建的非自身消息调用，重复事件不增加画像计数
func (a *Agent) afterMessagePersisted(ctx context.Context, msg *onebot.ConversationMessage) {
	// 思考调度前先更新成员昵称和会话基础信息
	a.updateMember(ctx, msg)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if err := a.markMessageRead(msg); err != nil {
			zap.L().Error("标记消息已读失败", zap.Int64("message_id", msg.MessageID), zap.Error(err))
		}
	}()
}

func (a *Agent) markMessageRead(msg *onebot.ConversationMessage) error {
	if a.bot == nil || msg.MessageID == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	if msg.ConversationKind == memory.ConversationKindPrivate {
		return a.bot.MarkPrivateMsgAsRead(ctx, msg.TargetID, msg.MessageID)
	}
	return a.bot.MarkMsgAsRead(ctx, msg.MessageID)
}

func (a *Agent) resolveReplyInfo(ctx context.Context, msg *onebot.ConversationMessage) error {
	if msg == nil || msg.Reply == nil || msg.Reply.MessageID == 0 {
		return nil
	}
	key := replyCacheKey(msg.ConversationKind, msg.TargetID, msg.Reply.MessageID)
	if msg.Reply.Content != "" && msg.Reply.SenderID != 0 {
		return nil
	}

	if cached := a.replyCache.Get(key); cached != nil {
		clone := cached.Value()
		msg.Reply = &clone
		return nil
	}

	log, err := a.memory.WithContext(ctx).GetMessageLogByScope(msg.ConversationKind, msg.TargetID, msg.Reply.MessageID)
	if err == nil {
		if reply := replyInfoFromMessageLog(log); reply != nil {
			msg.Reply = reply
			return nil
		}
	}

	reply, err := a.fetchReplyInfo(ctx, msg.Reply.MessageID)
	if err != nil {
		return err
	}
	if reply != nil {
		msg.Reply = reply
	}
	return nil
}

func (a *Agent) fetchReplyInfo(ctx context.Context, messageID int64) (*onebot.ReplyInfo, error) {
	if a.bot == nil || messageID == 0 {
		return nil, nil
	}

	replyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	replyData, err := a.bot.GetMsg(replyCtx, messageID)
	if err != nil {
		return nil, err
	}
	if replyData == nil {
		return &onebot.ReplyInfo{MessageID: messageID}, nil
	}

	reply := &onebot.ReplyInfo{MessageID: messageID, Content: replyData.RawMessage}
	// UserID 保留生成代码的原始联合形态（整数报文），直接解码；Sender map 值已退化为 float64，只取字符串字段
	reply.SenderID = onebot.ParseIDFromRaw(replyData.UserID.Raw)
	if sender := replyData.Sender; sender != nil {
		if nick, ok := sender["nickname"].(string); ok {
			reply.Nickname = nick
		}
		if card, ok := sender["card"].(string); ok {
			reply.GroupCard = card
		}
	}
	return reply, nil
}

func replyCacheKey(kind string, targetID, messageID int64) string {
	if kind == "" {
		kind = memory.ConversationKindGroup
	}
	return recallScopeKey(kind, targetID) + ":" + strconv.FormatInt(messageID, 10)
}

func (a *Agent) updateMember(ctx context.Context, msg *onebot.ConversationMessage) {
	mem := a.memory.WithContext(ctx)
	_, err := mem.GetOrCreateMemberProfile(msg.UserID, msg.Nickname, msg.Time)
	if err != nil {
		zap.L().Error("获取成员画像失败", zap.Error(err))
		return
	}
	if msg.ConversationKind == memory.ConversationKindPrivate {
		return
	}
	if err := mem.RecordMemberName(msg.UserID, msg.TargetID, msg.GroupCard, msg.Time); err != nil {
		zap.L().Error("更新成员群名片失败", zap.Error(err))
	}
}

func replyInfoFromMessageLog(log *memory.MessageLog) *onebot.ReplyInfo {
	if log == nil {
		return nil
	}

	if log.RecalledAt != nil {
		return &onebot.ReplyInfo{
			MessageID: log.OneBotMessageID,
			Content:   memory.RecalledMessageDisplayContent,
			SenderID:  log.UserID,
			Nickname:  log.Nickname,
		}
	}
	content := strings.TrimSpace(log.DisplayContent)
	if content == "" {
		content = strings.TrimSpace(log.TextContent)
	}

	return &onebot.ReplyInfo{
		MessageID: log.OneBotMessageID,
		Content:   content,
		SenderID:  log.UserID,
		Nickname:  log.Nickname,
	}
}
