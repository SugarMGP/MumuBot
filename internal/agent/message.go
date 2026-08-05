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

	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
)

const (
	commitQueueSize   = 256
	pendingCommitSize = 256
	recallPendingTTL  = 5 * time.Minute
)

type recallCommit struct {
	groupID    int64
	messageID  int64
	operatorID int64
}

// commitItem 提交队列项：消息、戳一戳、撤回统一按群内到达序号重排提交；
// skip 用于消费不会产生实际处理的序号（解析失败、无效事件、未启用群），避免重排器死等。
type commitItem struct {
	groupID     int64
	seq         uint64
	skip        bool
	recall      *recallCommit
	msg         *onebot.GroupMessage
	isMentioned bool
}

func (a *Agent) onMessage(msg *onebot.GroupMessage) {
	if msg == nil {
		return
	}
	if err := a.ctx.Err(); err != nil {
		return
	}
	if msg.ParseFailed {
		a.enqueueCommitSkip(msg.GroupID, msg.ArrivalSeq)
		return
	}
	cfg := config.Get()
	if !cfg.IsGroupEnabled(msg.GroupID) {
		a.enqueueCommitSkip(msg.GroupID, msg.ArrivalSeq)
		return
	}
	if msg.ReceivedAt.IsZero() {
		msg.ReceivedAt = time.Now()
	}
	if msg.MessageID == 0 {
		if !a.onInteractionMessage(msg) {
			a.enqueueCommitSkip(msg.GroupID, msg.ArrivalSeq)
			return
		}
		a.enqueueCommit(commitItem{groupID: msg.GroupID, seq: msg.ArrivalSeq, msg: msg, isMentioned: msg.IsMentioned && msg.UserID != a.bot.GetSelfID()})
		return
	}

	selfID := a.bot.GetSelfID()
	a.resolveBufferedReplyInfo(msg)
	if err := a.resolveReplyInfo(msg); err != nil {
		zap.L().Debug("解析回复消息失败", zap.Int64("group_id", msg.GroupID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
	}
	isMentioned := msg.IsMentioned || a.persona.IsMentioned(msg.Content)
	if msg.Reply != nil && msg.Reply.SenderID != 0 && selfID != 0 && msg.Reply.SenderID == selfID {
		isMentioned = true
	}
	msg.IsMentioned = isMentioned

	a.resolveForwardMessages(msg)
	parsedContent := a.parseMessageContent(msg)
	for _, t := range a.tools {
		info, err := t.Info(a.ctx)
		if err != nil || strings.TrimSpace(info.Name) == "" {
			continue
		}
		parsedContent = strings.ReplaceAll(parsedContent, info.Name, "\"危险指令，已屏蔽\"")
	}
	msg.FinalContent = parsedContent

	a.enqueueCommit(commitItem{groupID: msg.GroupID, seq: msg.ArrivalSeq, msg: msg, isMentioned: isMentioned})
}

// enqueueCommit 把解析完成的消息、撤回或跳过项投入该群提交队列。
// 序号为 0 的项（Agent 内部消息，如本地发言）不参与重排，直接提交。
// 提交队列满时背压等待，不静默丢弃；关闭后由 ctx 退出。
func (a *Agent) enqueueCommit(item commitItem) {
	if item.seq == 0 {
		a.commitOne(item)
		return
	}
	a.commitMu.Lock()
	queue := a.commitQueues[item.groupID]
	if queue == nil {
		queue = make(chan commitItem, commitQueueSize)
		a.commitQueues[item.groupID] = queue
		a.commitWG.Add(1)
		go a.commitWorker(queue)
	}
	a.commitMu.Unlock()

	select {
	case queue <- item:
	case <-a.ctx.Done():
	}
}

// enqueueCommitSkip 消费一个不会产生实际处理的到达序号。
func (a *Agent) enqueueCommitSkip(groupID int64, seq uint64) {
	a.enqueueCommit(commitItem{groupID: groupID, seq: seq, skip: true})
}

// commitWorker 每群一个提交协程：解析乱序完成后，按到达序号重排提交，
// 保证落库、撤回、入缓冲和思考调度的顺序与事件到达顺序一致。
// 视觉等慢解析已在提交前并行完成，提交阶段只做快操作。
// 乱序窗口（pending）有上限：超限时丢弃等待队列中最接近水位的项并把水位推进越过它，
// 被越过的序号（含仍在解析中的）到达时自然被跳过，不记录、不留下永久缺口，内存有界。
func (a *Agent) commitWorker(queue <-chan commitItem) {
	defer a.commitWG.Done()
	next := uint64(1)
	pending := make(map[uint64]commitItem)
	for item := range queue {
		switch {
		case item.seq > next:
			if len(pending) >= pendingCommitSize {
				minSeq := item.seq
				for seq := range pending {
					if seq < minSeq {
						minSeq = seq
					}
				}
				delete(pending, minSeq)
				next = minSeq + 1
				zap.L().Error("提交重排等待队列超限，丢弃最旧等待项并推进水位", zap.Int64("group_id", item.groupID), zap.Uint64("dropped_seq", minSeq), zap.Uint64("watermark", next), zap.Int("pending", len(pending)))
				if item.seq > next {
					pending[item.seq] = item
				}
			} else {
				pending[item.seq] = item
			}
		case item.seq == next:
			a.commitOne(item)
			next++
		}
		for {
			queued, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			a.commitOne(queued)
			next++
		}
	}
	if len(pending) > 0 {
		zap.L().Warn("停机排空结束，乱序等待项未提交", zap.Int("pending", len(pending)))
	}
}

func (a *Agent) commitOne(item commitItem) {
	switch {
	case item.skip:
	case item.recall != nil:
		a.commitRecall(item.recall)
	default:
		a.commitMessage(item)
	}
}

// commitRecall 按到达顺序执行撤回：正常事件顺序下，同群序号更小的消息已落库并入缓冲，
// 撤回总能命中数据库记录并在缓冲中找到对应消息。若原消息尚未落库（重连窗口、上游丢失或
// 事件乱序），登记待补偿记录，待消息落库后补记撤回。
func (a *Agent) commitRecall(recall *recallCommit) {
	log, changed, err := a.memory.MarkMessageRecalled(recall.groupID, recall.messageID)
	if err != nil {
		zap.L().Warn("标记群消息撤回失败", zap.Int64("group_id", recall.groupID), zap.Int64("message_id", recall.messageID), zap.Int64("operator_id", recall.operatorID), zap.Error(err))
		return
	}
	if !changed {
		// 原消息尚未落库（重连窗口、上游丢失或事件乱序），登记待补偿。
		zap.L().Debug("撤回时原消息未落库，登记待补偿", zap.Int64("group_id", recall.groupID), zap.Int64("message_id", recall.messageID))
		a.recallMu.Lock()
		if a.pendingRecalls[recall.groupID] == nil {
			a.pendingRecalls[recall.groupID] = make(map[int64]time.Time)
		}
		a.pendingRecalls[recall.groupID][recall.messageID] = time.Now().Add(recallPendingTTL)
		a.recallMu.Unlock()
		return
	}
	a.syncRecalledMessage(log)
	zap.L().Info("群消息已撤回", zap.Int64("group_id", recall.groupID), zap.Int64("message_id", recall.messageID), zap.Int64("operator_id", recall.operatorID))
}

// applyPendingRecall 消息落库后检查待补偿撤回记录，命中则补记撤回并同步缓冲展示。
func (a *Agent) applyPendingRecall(msg *onebot.GroupMessage) {
	a.recallMu.Lock()
	groupRecalls := a.pendingRecalls[msg.GroupID]
	deadline, ok := groupRecalls[msg.MessageID]
	if ok {
		delete(groupRecalls, msg.MessageID)
		if len(groupRecalls) == 0 {
			delete(a.pendingRecalls, msg.GroupID)
		}
	}
	a.recallMu.Unlock()
	if !ok || time.Now().After(deadline) {
		return
	}

	log, changed, err := a.memory.MarkMessageRecalled(msg.GroupID, msg.MessageID)
	if err != nil {
		zap.L().Warn("补记消息撤回失败", zap.Int64("group_id", msg.GroupID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
		return
	}
	if !changed {
		return
	}
	msg.FinalContent = log.DisplayContent
	a.syncRecalledMessage(log)
}

// recallPruneLoop 定期清理过期的待补偿撤回记录，防止永不落库的消息 ID 持续积累。
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
	for groupID, recalls := range a.pendingRecalls {
		for messageID, deadline := range recalls {
			if now.After(deadline) {
				delete(recalls, messageID)
			}
		}
		if len(recalls) == 0 {
			delete(a.pendingRecalls, groupID)
		}
	}
}

// onRecall 撤回事件入口：带到达序号进入提交队列，与同群消息保持顺序。
func (a *Agent) onRecall(groupID, messageID, operatorID int64, arrivalSeq uint64) {
	if groupID <= 0 || messageID <= 0 || !config.Get().IsGroupEnabled(groupID) {
		a.enqueueCommitSkip(groupID, arrivalSeq)
		return
	}
	a.enqueueCommit(commitItem{groupID: groupID, seq: arrivalSeq, recall: &recallCommit{groupID: groupID, messageID: messageID, operatorID: operatorID}})
}

func (a *Agent) commitMessage(item commitItem) {
	msg := item.msg
	selfID := a.bot.GetSelfID()
	if msg.MessageID != 0 {
		ctx := a.ctx
		if ctx.Err() != nil {
			ctx = context.Background()
		}
		log, created, err := a.topicMgr.PersistMessage(ctx, msg, item.isMentioned)
		if err != nil {
			zap.L().Error("写入话题工作记忆失败", zap.Int64("group_id", msg.GroupID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
			return
		}
		if log == nil || !created {
			return
		}
		a.addBuffer(msg)
		a.applyPendingRecall(msg)

		if msg.UserID == selfID {
			return
		}
		if a.ctx.Err() != nil {
			// 停机排空阶段：OneBot 已关闭，不再执行标已读和画像更新。
			return
		}
		// 只有确实落库成功且非机器人自身的消息，才执行标已读和画像更新。
		a.wg.Add(1)
		go func(messageID int64) {
			defer a.wg.Done()
			if err := a.markMessageRead(messageID); err != nil {
				zap.L().Error("标记消息已读失败", zap.Int64("message_id", messageID), zap.Error(err))
			}
			a.updateMember(msg)
		}(msg.MessageID)
	} else {
		a.addBuffer(msg)
	}
	if a.ctx.Err() != nil {
		return
	}
	a.scheduleThink(msg.GroupID, item.isMentioned, false, msg.ReceivedAt)
}

func (a *Agent) resolveBufferedReplyInfo(msg *onebot.GroupMessage) {
	if msg == nil || msg.Reply == nil || msg.Reply.MessageID == 0 || msg.Reply.SenderID != 0 {
		return
	}
	if reply := findReplyInfoInMessages(a.getBuffer(msg.GroupID), msg.Reply.MessageID); reply != nil {
		msg.Reply = reply
	}
}

// onInteractionMessage 只构造戳一戳的展示内容，返回是否有效；缓冲和思考调度由提交队列统一处理。
func (a *Agent) onInteractionMessage(msg *onebot.GroupMessage) bool {
	if msg.UserID <= 0 || len(msg.AtList) == 0 || msg.AtList[0] <= 0 {
		return false
	}
	targetID := msg.AtList[0]
	actorName := utils.FirstNonEmpty(msg.GroupCard, msg.DisplayName, msg.Nickname, fmt.Sprintf("%d", msg.UserID))
	targetName := fmt.Sprintf("%d", targetID)
	msg.Content = ""
	msg.FinalContent = fmt.Sprintf("[%s] %s(%d) 戳了戳 %s(%d)\n", msg.Time.Format("15:04:05"), actorName, msg.UserID, targetName, targetID)
	return true
}

func botMentionDisplayName(botName string) string {
	if botName = strings.TrimSpace(botName); botName != "" {
		return botName + "(你)"
	}
	return "机器人(你)"
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
	// 提交队列保证消息按到达顺序写入缓冲，直接追加即可。
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
