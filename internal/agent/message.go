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
	if msg.ReceivedAt.IsZero() {
		msg.ReceivedAt = time.Now()
	}
	if msg.MessageID == 0 {
		a.onInteractionMessage(msg)
		return
	}

	selfID := a.bot.GetSelfID()
	a.resolveBufferedReplyInfo(msg)
	isMentioned := msg.IsMentioned || a.persona.IsMentioned(msg.Content)
	if msg.Reply != nil && msg.Reply.SenderID != 0 && selfID != 0 && msg.Reply.SenderID == selfID {
		isMentioned = true
	}
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

	a.addBuffer(msg)

	if msg.UserID == selfID {
		return
	}

	if err := a.ctx.Err(); err != nil {
		return
	}
	a.scheduleThink(msg.GroupID, isMentioned, false, msg.ReceivedAt)

	completed := cloneMessageForCompletion(msg)
	a.wg.Add(1)
	go func(messageLogID uint, source, completion *onebot.GroupMessage, initiallyMentioned bool) {
		defer a.wg.Done()
		a.completeMessage(messageLogID, source, completion, initiallyMentioned)
	}(item.ID, msg, completed, isMentioned)
}

func (a *Agent) completeMessage(messageLogID uint, source, msg *onebot.GroupMessage, initiallyMentioned bool) {
	if err := a.ctx.Err(); err != nil {
		return
	}
	if err := a.markMessageRead(msg.MessageID); err != nil {
		zap.L().Error("标记消息已读失败", zap.Int64("message_id", msg.MessageID), zap.Error(err))
	}
	if err := a.ctx.Err(); err != nil {
		return
	}
	if err := a.resolveReplyInfo(msg); err != nil {
		zap.L().Debug("解析回复消息失败", zap.Int64("group_id", msg.GroupID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
	}
	selfID := a.bot.GetSelfID()
	if msg.Reply != nil && msg.Reply.SenderID != 0 && selfID != 0 && msg.Reply.SenderID == selfID {
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
	current, err := a.topicMgr.UpdateMessagePresentation(a.ctx, messageLogID, msg.FinalContent, forwardsJSON, msg.IsMentioned)
	if err != nil {
		zap.L().Error("更新消息展示内容失败", zap.Int64("group_id", msg.GroupID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
	} else {
		if !current {
			return
		}
		if a.replaceBufferedMessage(msg.MessageID, source, msg) && !initiallyMentioned && msg.IsMentioned {
			a.scheduleThink(msg.GroupID, true, false, msg.ReceivedAt)
		}
	}
	a.updateMember(msg)
}

func cloneMessageForCompletion(msg *onebot.GroupMessage) *onebot.GroupMessage {
	clone := *msg
	clone.Forwards = slices.Clone(msg.Forwards)
	if msg.Reply != nil {
		reply := *msg.Reply
		clone.Reply = &reply
	}
	return &clone
}

func (a *Agent) resolveBufferedReplyInfo(msg *onebot.GroupMessage) {
	if msg == nil || msg.Reply == nil || msg.Reply.MessageID == 0 || msg.Reply.SenderID != 0 {
		return
	}
	if reply := findReplyInfoInMessages(a.getBuffer(msg.GroupID), msg.Reply.MessageID); reply != nil {
		msg.Reply = reply
	}
}

func (a *Agent) onInteractionMessage(msg *onebot.GroupMessage) {
	if len(msg.AtList) == 0 || msg.AtList[0] <= 0 {
		return
	}
	targetID := msg.AtList[0]
	actorName := utils.FirstNonEmpty(msg.GroupCard, msg.DisplayName, msg.Nickname, fmt.Sprintf("%d", msg.UserID))
	targetName := fmt.Sprintf("%d", targetID)
	msg.Content = ""
	msg.FinalContent = fmt.Sprintf("[%s] %s(%d) 戳了戳 %s(%d)\n", msg.Time.Format("15:04:05"), actorName, msg.UserID, targetName, targetID)
	a.addBuffer(msg)
	a.scheduleThink(msg.GroupID, msg.IsMentioned && msg.UserID != a.bot.GetSelfID(), false, msg.ReceivedAt)
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
	parts := make([]string, 0, 8)
	if content := strings.TrimSpace(msg.Content); content != "" {
		parts = append(parts, content)
	}
	for _, userID := range msg.AtList {
		if userID == onebot.AtAllUserID {
			parts = append(parts, "@全体成员")
		} else if userID > 0 {
			parts = append(parts, fmt.Sprintf("@%d", userID))
		}
	}
	for _, face := range msg.Faces {
		if face.Name != "" {
			parts = append(parts, fmt.Sprintf("[表情:%s]", face.Name))
		} else if face.ID > 0 {
			parts = append(parts, fmt.Sprintf("[表情:%d]", face.ID))
		} else {
			parts = append(parts, "[表情]")
		}
	}
	for _, image := range msg.Images {
		if image.SubType == 1 {
			if image.Desc != "" {
				parts = append(parts, fmt.Sprintf("[表情包:%s]", image.Desc))
			} else {
				parts = append(parts, "[表情包]")
			}
		} else if image.Desc != "" {
			parts = append(parts, fmt.Sprintf("[图片:%s]", image.Desc))
		} else {
			parts = append(parts, "[图片]")
		}
	}
	for range msg.Videos {
		parts = append(parts, "[视频]")
	}
	if msg.HasRecord {
		parts = append(parts, "[语音]")
	}
	for _, name := range msg.FileNames {
		if name = strings.TrimSpace(name); name != "" {
			parts = append(parts, fmt.Sprintf("[文件:%s]", name))
		} else {
			parts = append(parts, "[文件]")
		}
	}
	for i := range msg.Cards {
		parts = append(parts, msg.Cards[i].Format())
	}
	for range msg.ForwardIDs {
		parts = append(parts, "[合并转发]")
	}
	reply := ""
	if msg.Reply != nil && msg.Reply.MessageID > 0 {
		reply = fmt.Sprintf(" [回复 #%d]", msg.Reply.MessageID)
	}
	displayName := utils.FirstNonEmpty(msg.GroupCard, msg.DisplayName, msg.Nickname, fmt.Sprintf("%d", msg.UserID))
	return fmt.Sprintf("[%s] #%d %s(%d):%s %s\n",
		msg.Time.Format("15:04:05"), msg.MessageID, displayName, msg.UserID, reply, strings.Join(parts, " "))
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

func (a *Agent) replaceBufferedMessage(messageID int64, source, replacement *onebot.GroupMessage) bool {
	if messageID == 0 || source == nil || replacement == nil {
		return false
	}
	a.buffersMu.Lock()
	defer a.buffersMu.Unlock()
	for i, current := range a.buffers[source.GroupID] {
		if current == nil || current.MessageID != messageID {
			continue
		}
		if current != source {
			return false
		}
		a.buffers[source.GroupID][i] = replacement
		if a.lastReadMessage[source.GroupID] == current {
			a.lastReadMessage[source.GroupID] = replacement
		}
		return true
	}
	return false
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
