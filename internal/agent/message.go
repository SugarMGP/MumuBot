package agent

import (
	"context"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/topic"
	"mumu-bot/internal/utils"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
)

func (a *Agent) onMessage(msg *onebot.GroupMessage) {
	if err := a.ctx.Err(); err != nil {
		return
	}
	cfg := config.Get()
	if !cfg.IsGroupEnabled(msg.GroupID) {
		return
	}

	if err := a.resolveReplyInfo(msg); err != nil {
		zap.L().Debug("解析回复消息失败", zap.Int64("group_id", msg.GroupID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
	}

	selfID := a.bot.GetSelfID()
	isMentioned := msg.IsMentioned || a.persona.IsMentioned(msg.Content) || (msg.Reply != nil && msg.Reply.SenderID != 0 && selfID != 0 && msg.Reply.SenderID == selfID)

	forwardsJSON := ""
	if len(msg.Forwards) > 0 {
		if b, err := sonic.MarshalString(msg.Forwards); err == nil {
			forwardsJSON = b
		}
	}

	parsedContent := a.parseMessageContent(msg)

	for _, t := range a.tools {
		info, _ := t.Info(a.ctx)
		parsedContent = strings.ReplaceAll(parsedContent, info.Name, "\"危险指令，已屏蔽\"")
	}
	msg.FinalContent = parsedContent

	persistErr := a.topicMgr.PersistMessage(a.ctx, topic.PersistMessageInput{
		Message:      msg,
		IsMentioned:  isMentioned,
		ForwardsJSON: forwardsJSON,
	})
	if persistErr != nil {
		zap.L().Error("写入话题工作记忆失败", zap.Int64("group_id", msg.GroupID), zap.Int64("message_id", msg.MessageID), zap.Error(persistErr))
	}

	a.addBuffer(msg)

	if msg.UserID == a.bot.GetSelfID() {
		return
	}

	if err := a.ctx.Err(); err != nil {
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.updateMember(msg)
	}()

	a.scheduleThink(msg.GroupID, isMentioned, false)
}

func (a *Agent) resolveReplyInfo(msg *onebot.GroupMessage) error {
	if msg == nil || msg.Reply == nil || msg.Reply.MessageID == 0 {
		return nil
	}
	if msg.Reply.Content != "" && msg.Reply.SenderID != 0 {
		a.replyCache.Set(msg.Reply.MessageID, *msg.Reply, ttlcache.DefaultTTL)
		return nil
	}

	if reply := findReplyInfoInMessages(a.getBuffer(msg.GroupID), msg.Reply.MessageID); reply != nil {
		msg.Reply = reply
		a.replyCache.Set(reply.MessageID, *reply, ttlcache.DefaultTTL)
		return nil
	}

	log, err := a.memory.GetMessageLogByID(fmt.Sprintf("%d", msg.Reply.MessageID))
	if err == nil {
		if reply := replyInfoFromMessageLog(log); reply != nil {
			msg.Reply = reply
			a.replyCache.Set(reply.MessageID, *reply, ttlcache.DefaultTTL)
			return nil
		}
	}

	if cached := a.replyCache.Get(msg.Reply.MessageID); cached != nil {
		clone := cached.Value()
		msg.Reply = &clone
		return nil
	}

	reply, err := a.fetchReplyInfo(msg.Reply.MessageID)
	if err != nil {
		return err
	}
	if reply != nil {
		msg.Reply = reply
		a.replyCache.Set(reply.MessageID, *reply, ttlcache.DefaultTTL)
	}
	return nil
}

func (a *Agent) fetchReplyInfo(messageID int64) (*onebot.ReplyInfo, error) {
	if a.bot == nil || messageID == 0 {
		return nil, nil
	}

	replyCtx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()

	replyData, err := a.bot.GetMsg(replyCtx, messageID)
	if err != nil {
		return nil, err
	}
	if replyData == nil {
		return &onebot.ReplyInfo{MessageID: messageID}, nil
	}

	reply := &onebot.ReplyInfo{MessageID: messageID}
	if rawMsg, ok := replyData["raw_message"].(string); ok {
		reply.Content = rawMsg
	}
	if sender, ok := replyData["sender"].(map[string]interface{}); ok {
		if uid, ok := utils.ParseInt64Value(sender["user_id"]); ok {
			reply.SenderID = uid
		}
		if nick, ok := sender["nickname"].(string); ok {
			reply.Nickname = nick
		}
		if card, ok := sender["card"].(string); ok {
			reply.GroupCard = card
		}
	}
	reply.Display = displayNameForRenderedText(reply.GroupCard, reply.Nickname, "")

	return reply, nil
}

func displayNameForRenderedText(groupCard, fallbackName, qq string) string {
	return utils.FirstNonEmpty(groupCard, fallbackName, qq)
}

func (a *Agent) addBuffer(msg *onebot.GroupMessage) {
	a.buffersMu.Lock()
	buf, ok := a.buffers[msg.GroupID]
	if !ok {
		bufSize := config.Get().Agent.MessageBufferSize
		if bufSize <= 0 {
			bufSize = 15
		}
		buf = utils.NewRingBuffer[*onebot.GroupMessage](bufSize)
		a.buffers[msg.GroupID] = buf
	}
	a.buffersMu.Unlock()

	buf.Push(msg)
}

func (a *Agent) getBuffer(groupID int64) []*onebot.GroupMessage {
	a.buffersMu.RLock()
	buf, ok := a.buffers[groupID]
	a.buffersMu.RUnlock()

	if !ok || buf.IsEmpty() {
		return nil
	}
	return buf.GetAll()
}

func (a *Agent) updateMember(msg *onebot.GroupMessage) {
	if err := a.ctx.Err(); err != nil {
		return
	}
	p, err := a.memory.GetOrCreateMemberProfile(msg.UserID, msg.Nickname)
	if err != nil {
		zap.L().Error("获取成员画像失败", zap.Error(err))
		return
	}
	p.MsgCount++
	p.LastSpeak = msg.Time
	p.Nickname = msg.Nickname
	p.UpsertGroupCard(msg.GroupID, msg.GroupCard, msg.Time)
	if err := a.memory.UpdateMemberProfile(p); err != nil {
		zap.L().Error("更新成员画像失败", zap.Error(err))
	}
}

func findReplyInfoInMessages(msgs []*onebot.GroupMessage, messageID int64) *onebot.ReplyInfo {
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		if msg == nil || msg.MessageID != messageID {
			continue
		}

		content := strings.TrimSpace(msg.Content)
		if content == "" {
			content = strings.TrimSpace(msg.FinalContent)
		}

		return &onebot.ReplyInfo{
			MessageID: messageID,
			Content:   content,
			SenderID:  msg.UserID,
			Nickname:  msg.Nickname,
			GroupCard: msg.GroupCard,
			Display:   msg.DisplayName,
		}
	}
	return nil
}

func replyInfoFromMessageLog(log *memory.MessageLog) *onebot.ReplyInfo {
	if log == nil {
		return nil
	}

	content := strings.TrimSpace(log.OriginalContent)
	if content == "" {
		content = strings.TrimSpace(log.Content)
	}

	messageID, _ := strconv.ParseInt(log.MessageID, 10, 64)

	return &onebot.ReplyInfo{
		MessageID: messageID,
		Content:   content,
		SenderID:  log.UserID,
		Nickname:  log.Nickname,
	}
}
