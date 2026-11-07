package agent

import (
	"fmt"
	"sort"
	"strings"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
)

// restoreSessions 先恢复持久化工作集，没有工作集时才回退到最近原文
func (a *Agent) restoreSessions() error {
	groupIDs, err := a.memory.ListActiveGroupIDs(a.ctx)
	if err != nil {
		return fmt.Errorf("读取可恢复群聊失败: %w", err)
	}
	for _, groupID := range groupIDs {
		if err := a.restoreTranscriptSession(memory.ConversationKindGroup, groupID, 0); err != nil {
			return fmt.Errorf("恢复群 %d 工作集失败: %w", groupID, err)
		}
	}
	privateTargets, err := a.memory.ListConversationTargets(a.ctx, memory.ConversationKindPrivate, true)
	if err != nil {
		return fmt.Errorf("读取可恢复私聊失败: %w", err)
	}
	for _, target := range privateTargets {
		if err := a.restoreTranscriptSession(memory.ConversationKindPrivate, target.TargetID, 0); err != nil {
			return fmt.Errorf("恢复好友 %d 工作集失败: %w", target.TargetID, err)
		}
	}
	workers, err := a.memory.ListWorkerSessions(a.ctx)
	if err != nil {
		return fmt.Errorf("读取可恢复 worker 失败: %w", err)
	}
	for _, worker := range workers {
		afterID := worker.ContextThroughID
		if worker.ContextStartID > 0 {
			afterID = max(afterID, worker.ContextStartID-1)
		}
		if err := a.restoreTranscriptSession(memory.ConversationKindWorker, int64(worker.ID), afterID); err != nil {
			return fmt.Errorf("恢复 worker %d 工作集失败: %w", worker.ID, err)
		}
	}
	lifeState, stateErr := a.memory.GetLifeState(a.ctx)
	if stateErr != nil {
		return fmt.Errorf("读取 life 状态失败: %w", stateErr)
	}
	if err := a.restoreTranscriptSession(memory.ConversationKindLife, memory.LifeTargetID, lifeState.ContextThroughID); err != nil {
		return fmt.Errorf("恢复 life 工作集失败: %w", err)
	}
	return nil
}

// restoreTranscriptSession 读取检查点、保留尾部和工作集 pending
func (a *Agent) restoreTranscriptSession(kind string, targetID int64, afterID uint) error {
	session := a.sessions.get(kind, targetID)
	session.ClearTokenBase()
	if latest, timeErr := a.memory.LatestMessageTime(a.ctx, kind, targetID, a.bot.GetSelfID()); timeErr == nil && !latest.IsZero() {
		session.SetLastActivity(latest)
	}
	checkpoint, err := a.memory.GetCheckpoint(a.ctx, kind, targetID)
	if err != nil {
		return err
	}
	var after uint64
	if checkpoint != nil && checkpoint.ThroughVisibleSeq != nil {
		after = *checkpoint.ThroughVisibleSeq
	}
	visible, err := a.memory.VisibleTranscript(a.ctx, kind, targetID, after, afterID)
	if err != nil {
		return err
	}
	pending, err := a.memory.PendingInputs(a.ctx, kind, targetID)
	if err != nil {
		return err
	}
	if len(visible) == 0 && len(pending) == 0 {
		// 工作集只从 transcript/checkpoint 恢复；原文通过历史工具按需读取
		return nil
	}

	visibleRows, deferred := splitRestoredEntries(visible)
	toolCalls := map[string]bool{}
	for _, entry := range visible {
		if entry.EntryKind == "tool" {
			toolCalls[toolCallKey(entry.RunID, entry.ToolCallID)] = true
		}
	}
	assistantEntries := make([]memory.TranscriptEntry, 0, len(visibleRows)+len(deferred))
	assistantEntries = append(assistantEntries, visibleRows...)
	assistantEntries = append(assistantEntries, deferred...)
	missing, err := a.collectMissingToolResults(session, assistantEntries, toolCalls)
	if err != nil {
		return err
	}

	for _, entry := range visibleRows {
		if entry.EntryKind != "user" {
			if err := a.restoreSeenEntry(session, &entry, missing); err != nil {
				return err
			}
			continue
		}
		if entry.MessageLogID == nil {
			continue
		}
		log, getErr := a.memory.GetMessageLogByInternalID(*entry.MessageLogID)
		if getErr != nil {
			continue
		}
		msg := a.conversationMessageFromLog(*log)
		if msg == nil {
			continue
		}
		var payload schema.AgenticMessage
		if sonic.Unmarshal(entry.Payload, &payload) != nil {
			payload = *msgToPayload(msg)
		}
		session.RestoreUser(msg, log.ID, log.MediaRefs, entry.ID, valueOrZero(entry.VisibleSeq), true, &payload)
	}

	unvisible := append([]memory.TranscriptEntry(nil), deferred...)
	sort.SliceStable(unvisible, func(i, j int) bool { return unvisible[i].ID < unvisible[j].ID })
	for _, entry := range unvisible {
		if err := a.restoreSeenEntry(session, &entry, missing); err != nil {
			return err
		}
	}

	for _, entry := range pending {
		if entry.InputSeq == nil {
			continue
		}
		if entry.EntryKind == "agent_message" {
			var payload schema.AgenticMessage
			if sonic.Unmarshal(entry.Payload, &payload) == nil {
				session.RestorePendingAgentMessage(&payload, entry.ID, payloadWake(&payload), payloadAllowSleep(&payload))
			}
			continue
		}
		if entry.EntryKind == "observation" {
			var payload schema.AgenticMessage
			if sonic.Unmarshal(entry.Payload, &payload) == nil {
				session.RestorePendingObservation(&payload, entry.ID)
			}
			continue
		}
		if entry.MessageLogID == nil {
			continue
		}
		log, getErr := a.memory.GetMessageLogByInternalID(*entry.MessageLogID)
		if getErr != nil {
			continue
		}
		msg := a.conversationMessageFromLog(*log)
		if msg == nil {
			continue
		}
		var payload schema.AgenticMessage
		if sonic.Unmarshal(entry.Payload, &payload) != nil {
			payload = *msgToPayload(msg)
		}
		session.RestoreUser(msg, log.ID, log.MediaRefs, entry.ID, 0, false, &payload)
	}
	session.RestoreTokenBaseFromSeen()
	return nil
}

func splitRestoredEntries(entries []memory.TranscriptEntry) (visible, deferred []memory.TranscriptEntry) {
	for _, entry := range entries {
		if entry.VisibleSeq == nil {
			deferred = append(deferred, entry)
			continue
		}
		visible = append(visible, entry)
	}
	return visible, deferred
}

// collectMissingToolResults 为缺少回执的 assistant 工具调用补 not_executed 行
func (a *Agent) collectMissingToolResults(session *Session, entries []memory.TranscriptEntry, toolCalls map[string]bool) (map[uint][]*memory.TranscriptEntry, error) {
	missing := map[uint][]*memory.TranscriptEntry{}
	for _, entry := range entries {
		if entry.EntryKind != "assistant" {
			continue
		}
		var message schema.AgenticMessage
		if sonic.Unmarshal(entry.Payload, &message) != nil {
			continue
		}
		for _, block := range message.ContentBlocks {
			if block == nil || block.FunctionToolCall == nil {
				continue
			}
			call := block.FunctionToolCall
			key := toolCallKey(entry.RunID, call.CallID)
			if toolCalls[key] {
				continue
			}
			assistantID := entry.ID
			toolEntry, created, err := session.AppendNotExecutedTool(a.ctx, entry.RunID, call.CallID, call.Name)
			if err != nil {
				return nil, err
			}
			toolCalls[key] = true
			if created && toolEntry != nil {
				missing[assistantID] = append(missing[assistantID], toolEntry)
			}
		}
	}
	return missing, nil
}

func (a *Agent) restoreSeenEntry(session *Session, entry *memory.TranscriptEntry, missing map[uint][]*memory.TranscriptEntry) error {
	if entry.EntryKind == "tool" && (entry.ToolStatus == "running" || entry.ToolStatus == "planned") {
		if err := session.RepairToolTranscript(a.ctx, entry, "unknown", "工具执行因进程退出而中断，没有可复用结果，请勿自动重放。"); err != nil {
			return err
		}
	}
	var message schema.AgenticMessage
	if sonic.Unmarshal(entry.Payload, &message) == nil {
		if entry.EntryKind == "system_note" && messageKind(&message) == "maintenance_hint" && entry.VisibleSeq == nil {
			session.RestorePendingMaintenanceHint(&message, entry.ID)
		} else if entry.EntryKind == "observation" {
			session.AppendSeenObservation(&message, entry.ID, valueOrZero(entry.VisibleSeq))
		} else {
			session.AppendSeenMessage(&message, entry.ID, valueOrZero(entry.VisibleSeq))
		}
	}
	for _, extra := range missing[entry.ID] {
		var repaired schema.AgenticMessage
		if sonic.Unmarshal(extra.Payload, &repaired) == nil {
			session.AppendSeenMessage(&repaired, extra.ID, 0)
		}
	}
	return nil
}

func toolCallKey(runID, callID string) string {
	return runID + "\x00" + callID
}

func valueOrZero(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}

// payloadAllowSleep 读取“允许在睡眠中启动”的持久标记，缺省不允许
func payloadAllowSleep(payload *schema.AgenticMessage) bool {
	if payload == nil || payload.Extra == nil {
		return false
	}
	value, ok := payload.Extra["allow_asleep"].(bool)
	return ok && value
}

// payloadWake 读取内部消息持久化的唤醒标记，缺省按需要处理
func payloadWake(payload *schema.AgenticMessage) bool {
	if payload == nil || payload.Extra == nil {
		return true
	}
	value, ok := payload.Extra["wake"].(bool)
	if !ok {
		return true
	}
	return value
}

func msgToPayload(msg *onebot.ConversationMessage) *schema.AgenticMessage {
	if msg == nil {
		return nil
	}
	body := strings.TrimSpace(msg.FinalContent)
	if body == "" {
		body = strings.TrimSpace(msg.Content)
	}
	return schema.UserAgenticMessage(fmt.Sprintf("[%d] %s %s(%d): %s", msg.MessageID, msg.Time.Format("2006-01-02 15:04:05"), msg.Nickname, msg.UserID, body))
}

// conversationMessageFromLog 恢复消息时补齐引用目标的发送者，供自动参考选择
func (a *Agent) conversationMessageFromLog(log memory.MessageLog) *onebot.ConversationMessage {
	msg := messageLogToSessionMessage(log)
	if msg == nil || log.ReplyToMessageID == nil {
		return msg
	}
	reply, err := a.memory.WithContext(a.ctx).GetMessageLogByScope(log.ConversationKind, log.TargetID, *log.ReplyToMessageID)
	if err == nil && reply != nil {
		msg.Reply.SenderID = reply.UserID
	}
	return msg
}

// messageLogToSessionMessage 把消息库记录转成会话消息
func messageLogToSessionMessage(log memory.MessageLog) *onebot.ConversationMessage {
	var reply *onebot.ReplyInfo
	if log.ReplyToMessageID != nil {
		reply = &onebot.ReplyInfo{MessageID: *log.ReplyToMessageID}
	}
	return &onebot.ConversationMessage{
		MessageID:        log.OneBotMessageID,
		ConversationKind: log.ConversationKind,
		TargetID:         log.TargetID,
		UserID:           log.UserID,
		Nickname:         log.Nickname,
		Content:          log.TextContent,
		FinalContent:     log.DisplayContent,
		Files:            log.FileRefs,
		IsMentioned:      log.IsMentioned,
		Time:             log.MessageTime,
		Reply:            reply,
	}
}
