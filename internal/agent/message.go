package agent

import (
	"context"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/utils"
	"slices"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
)

func (a *Agent) onMessage(msg *onebot.GroupMessage) {
	if msg == nil {
		return
	}
	if err := a.ctx.Err(); err != nil {
		return
	}
	cfg := config.Get()
	if !cfg.IsGroupEnabled(msg.GroupID) {
		return
	}
	if msg.MessageID == 0 {
		a.onInteractionMessage(msg)
		return
	}

	selfID := a.bot.GetSelfID()
	isMentioned := msg.IsMentioned || a.persona.IsMentioned(msg.Content)
	msg.IsMentioned = isMentioned
	msg.FinalContent = initialMessageContent(msg)

	item, created, persistErr := a.topicMgr.PersistMessage(a.ctx, msg, isMentioned)
	if persistErr != nil {
		zap.L().Error("写入话题工作记忆失败", zap.Int64("group_id", msg.GroupID), zap.Int64("message_id", msg.MessageID), zap.Error(persistErr))
		return
	}
	if item == nil || !created {
		return
	}

	if err := a.markMessageRead(msg.MessageID); err != nil {
		zap.L().Error("标记消息已读失败", zap.Int64("message_id", msg.MessageID), zap.Error(err))
	}
	if err := a.resolveReplyInfo(msg); err != nil {
		zap.L().Debug("解析回复消息失败", zap.Int64("group_id", msg.GroupID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
	}
	if msg.Reply != nil && msg.Reply.SenderID != 0 && selfID != 0 && msg.Reply.SenderID == selfID {
		isMentioned = true
		msg.IsMentioned = true
	}
	a.resolveForwardMessages(msg)
	parsedContent := a.parseMessageContent(msg)
	for _, t := range a.tools {
		info, _ := t.Info(a.ctx)
		parsedContent = strings.ReplaceAll(parsedContent, info.Name, "\"危险指令，已屏蔽\"")
	}
	msg.FinalContent = parsedContent
	var forwardsJSON *string
	if len(msg.Forwards) > 0 {
		if b, err := sonic.MarshalString(msg.Forwards); err == nil {
			forwardsJSON = &b
		}
	}
	current, err := a.topicMgr.UpdateMessagePresentation(a.ctx, item.ID, msg.FinalContent, forwardsJSON, isMentioned)
	if err != nil {
		zap.L().Error("更新消息展示内容失败", zap.Int64("group_id", msg.GroupID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
	} else if !current {
		return
	}

	a.addBuffer(msg)

	if msg.UserID == selfID {
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

func (a *Agent) onInteractionMessage(msg *onebot.GroupMessage) {
	if len(msg.AtList) == 0 || msg.AtList[0] <= 0 {
		return
	}
	targetID := msg.AtList[0]
	actorName := a.resolveRenderedDisplayName(msg.GroupID, msg.UserID, msg.GroupCard, msg.DisplayName, msg.Nickname)
	if actorName == "" {
		actorName = fmt.Sprintf("%d", msg.UserID)
	}
	targetName := a.resolveRenderedDisplayName(msg.GroupID, targetID, "", "", fmt.Sprintf("%d", targetID))
	msg.Content = ""
	msg.FinalContent = fmt.Sprintf("[%s] %s(%d) 戳了戳 %s(%d)\n", msg.Time.Format("15:04:05"), actorName, msg.UserID, targetName, targetID)
	a.addBuffer(msg)
	a.scheduleThink(msg.GroupID, msg.IsMentioned && msg.UserID != a.bot.GetSelfID(), false)
}

func (a *Agent) onRecall(groupID, messageID, operatorID int64) {
	if groupID <= 0 || messageID <= 0 || !config.Get().IsGroupEnabled(groupID) {
		return
	}
	log, changed, err := a.memory.MarkMessageRecalled(groupID, messageID)
	if err != nil {
		zap.L().Warn("标记群消息撤回失败", zap.Int64("group_id", groupID), zap.Int64("message_id", messageID), zap.Int64("operator_id", operatorID), zap.Error(err))
		return
	}
	if !changed {
		return
	}
	a.syncRecalledMessage(log)
	zap.L().Info("群消息已撤回", zap.Int64("group_id", groupID), zap.Int64("message_id", messageID), zap.Int64("operator_id", operatorID))
}

func initialMessageContent(msg *onebot.GroupMessage) string {
	if msg == nil {
		return ""
	}
	if content := strings.TrimSpace(msg.Content); content != "" {
		return content
	}
	switch {
	case len(msg.Images) > 0:
		return "[图片]"
	case len(msg.Videos) > 0:
		return "[视频]"
	case len(msg.Faces) > 0:
		return "[表情]"
	case msg.HasRecord:
		return "[语音]"
	case len(msg.FileNames) > 0:
		return "[文件]"
	case len(msg.Cards) > 0:
		return "[卡片]"
	case len(msg.ForwardIDs) > 0:
		return "[合并转发]"
	default:
		return ""
	}
}

func (a *Agent) markMessageRead(messageID int64) error {
	if a.bot == nil || messageID == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	return a.bot.MarkMsgAsRead(ctx, messageID)
}

func (a *Agent) resolveForwardMessages(msg *onebot.GroupMessage) {
	if msg == nil || a.bot == nil || len(msg.ForwardIDs) == 0 {
		return
	}
	for _, forwardID := range msg.ForwardIDs {
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		nodes, err := a.bot.GetForwardMsg(ctx, forwardID)
		cancel()
		if err != nil {
			zap.L().Debug("解析合并转发失败", zap.String("forward_id", forwardID), zap.Error(err))
			continue
		}
		msg.Forwards = append(msg.Forwards, nodes...)
	}
}

func (a *Agent) resolveReplyInfo(msg *onebot.GroupMessage) error {
	if msg == nil || msg.Reply == nil || msg.Reply.MessageID == 0 {
		return nil
	}
	if msg.Reply.Content != "" && msg.Reply.SenderID != 0 {
		a.replyCache.Set(msg.Reply.MessageID, *msg.Reply, ttlcache.DefaultTTL)
		return nil
	}

	if cached := a.replyCache.Get(msg.Reply.MessageID); cached != nil {
		clone := cached.Value()
		msg.Reply = &clone
		return nil
	}

	if reply := findReplyInfoInMessages(a.getBuffer(msg.GroupID), msg.Reply.MessageID); reply != nil {
		msg.Reply = reply
		a.replyCache.Set(reply.MessageID, *reply, ttlcache.DefaultTTL)
		return nil
	}

	log, err := a.memory.GetMessageLogByID(msg.Reply.MessageID)
	if err == nil {
		if reply := replyInfoFromMessageLog(log); reply != nil {
			msg.Reply = reply
			a.replyCache.Set(reply.MessageID, *reply, ttlcache.DefaultTTL)
			return nil
		}
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
	reply.Display = utils.FirstNonEmpty(reply.GroupCard, reply.Nickname)

	return reply, nil
}

func (a *Agent) addBuffer(msg *onebot.GroupMessage) {
	a.buffersMu.Lock()
	defer a.buffersMu.Unlock()
	bufSize := config.Get().Agent.MessageBufferSize
	if bufSize <= 0 {
		bufSize = 15
	}
	messages := append(a.buffers[msg.GroupID], msg)
	if len(messages) > bufSize {
		messages = slices.Delete(messages, 0, len(messages)-bufSize)
	}
	a.buffers[msg.GroupID] = messages
}

func (a *Agent) getBuffer(groupID int64) []*onebot.GroupMessage {
	a.buffersMu.RLock()
	defer a.buffersMu.RUnlock()
	return slices.Clone(a.buffers[groupID])
}

func (a *Agent) getMessageSnapshot(groupID int64) ([]*onebot.GroupMessage, *onebot.GroupMessage) {
	a.buffersMu.RLock()
	defer a.buffersMu.RUnlock()
	return slices.Clone(a.buffers[groupID]), a.lastReadMessage[groupID]
}

func (a *Agent) syncRecalledMessage(log *memory.MessageLog) {
	if log == nil {
		return
	}
	a.buffersMu.Lock()
	for i, msg := range a.buffers[log.GroupID] {
		if msg == nil || msg.MessageID != log.OneBotMessageID {
			continue
		}
		replacement := messageLogToBufferedGroupMessage(*log)
		a.buffers[log.GroupID][i] = replacement
		if a.lastReadMessage[log.GroupID] == msg {
			a.lastReadMessage[log.GroupID] = replacement
		}
		break
	}
	a.buffersMu.Unlock()
	a.replyCache.Delete(log.OneBotMessageID)
}

func (a *Agent) updateMember(msg *onebot.GroupMessage) {
	_, err := a.memory.GetOrCreateMemberProfile(msg.UserID, msg.Nickname, msg.Time)
	if err != nil {
		zap.L().Error("获取成员画像失败", zap.Error(err))
		return
	}
	if err := a.memory.RecordMemberName(msg.UserID, msg.GroupID, msg.GroupCard, msg.Time); err != nil {
		zap.L().Error("更新成员群名片失败", zap.Error(err))
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

	content := strings.TrimSpace(log.TextContent)
	if content == "" {
		content = strings.TrimSpace(log.DisplayContent)
	}

	return &onebot.ReplyInfo{
		MessageID: log.OneBotMessageID,
		Content:   content,
		SenderID:  log.UserID,
		Nickname:  log.Nickname,
	}
}
