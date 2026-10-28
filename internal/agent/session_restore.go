package agent

import (
	"fmt"
	"strings"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
)

// restoreSessions 先恢复持久化工作集，旧库没有工作集时才回退到最近原文
func (a *Agent) restoreSessions() error {
	groupIDs, err := a.memory.ListActiveGroupIDs(a.ctx)
	if err != nil {
		return fmt.Errorf("读取可恢复群聊失败: %w", err)
	}
	for _, groupID := range groupIDs {
		if err := a.restoreTranscriptSession(memory.ConversationKindGroup, groupID); err != nil {
			return fmt.Errorf("恢复群 %d 工作集失败: %w", groupID, err)
		}
	}
	privateTargets, err := a.memory.ListConversationTargets(a.ctx, memory.ConversationKindPrivate, true)
	if err != nil {
		return fmt.Errorf("读取可恢复私聊失败: %w", err)
	}
	for _, target := range privateTargets {
		if err := a.restoreTranscriptSession(memory.ConversationKindPrivate, target.TargetID); err != nil {
			return fmt.Errorf("恢复好友 %d 工作集失败: %w", target.TargetID, err)
		}
	}
	return nil
}

// restoreTranscriptSession 读取检查点、保留尾部和工作集 pending
func (a *Agent) restoreTranscriptSession(kind string, targetID int64) error {
	session := a.sessions.get(kind, targetID)
	checkpoint, err := a.memory.GetCheckpoint(a.ctx, kind, targetID)
	if err != nil {
		return err
	}
	session.SetCheckpoint(checkpoint)
	var after uint64
	if checkpoint != nil && checkpoint.ThroughVisibleSeq != nil {
		after = *checkpoint.ThroughVisibleSeq
	}
	visible, err := a.memory.VisibleTranscript(a.ctx, kind, targetID, after)
	if err != nil {
		return err
	}
	pending, err := a.memory.PendingInputs(a.ctx, kind, targetID)
	if err != nil {
		return err
	}
	for _, entry := range visible {
		if entry.EntryKind != "internal_reference" {
			continue
		}
		if versions := transcriptReferenceVersions(entry.Payload); len(versions) > 0 {
			session.SetAutoReferenceVersions(versions)
		}
	}
	if len(visible) == 0 && len(pending) == 0 {
		if checkpoint == nil {
			logs, getErr := a.memory.GetRecentMessagesScope(a.ctx, kind, targetID, 0, restoreMessageLimit, 0)
			if getErr != nil {
				return getErr
			}
			a.restoreSession(kind, targetID, trimRestoreMessages(logs, a.bot.GetSelfID()))
		}
		return nil
	}

	for _, entry := range pending {
		if entry.InputSeq == nil {
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
		msg := messageLogToSessionMessage(*log)
		if msg == nil {
			continue
		}
		var payload schema.AgenticMessage
		if sonic.Unmarshal(entry.Payload, &payload) != nil {
			payload = *msgToPayload(msg)
		}
		session.RestoreUser(msg, log.ID, log.MediaRefs, log.MessageRef, entry.ID, 0, false, &payload)
	}

	toolCalls := map[string]bool{}
	for _, entry := range visible {
		if entry.EntryKind == "tool" {
			toolCalls[toolCallKey(entry.RunID, entry.ToolCallID)] = true
		}
	}
	missing := map[int][]*memory.TranscriptEntry{}
	for index, entry := range visible {
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
			if toolCalls[toolCallKey(entry.RunID, call.CallID)] {
				continue
			}
			created, createErr := session.AppendNotExecutedTool(a.ctx, entry.RunID, call.CallID, call.Name)
			if createErr != nil {
				return createErr
			}
			missing[index] = append(missing[index], created)
			toolCalls[toolCallKey(entry.RunID, call.CallID)] = true
		}
	}

	for index, entry := range visible {
		if entry.EntryKind == "user" {
			if entry.MessageLogID == nil {
				continue
			}
			log, getErr := a.memory.GetMessageLogByInternalID(*entry.MessageLogID)
			if getErr != nil {
				continue
			}
			msg := messageLogToSessionMessage(*log)
			if msg == nil {
				continue
			}
			var payload schema.AgenticMessage
			if sonic.Unmarshal(entry.Payload, &payload) != nil {
				continue
			}
			visibleSeq := valueOrZero(entry.VisibleSeq)
			session.RestoreUser(msg, log.ID, log.MediaRefs, log.MessageRef, entry.ID, visibleSeq, true, &payload)
			continue
		}
		if entry.EntryKind == "tool" && (entry.ToolStatus == "running" || entry.ToolStatus == "planned") {
			if err := session.RepairToolTranscript(a.ctx, &entry, "unknown", "工具执行因进程退出而中断，没有可复用结果，请勿自动重放。"); err != nil {
				return err
			}
		}
		var message schema.AgenticMessage
		if sonic.Unmarshal(entry.Payload, &message) == nil {
			session.AppendSeenMessage(&message, entry.ID, valueOrZero(entry.VisibleSeq))
		}
		for _, extra := range missing[index] {
			var repaired schema.AgenticMessage
			if sonic.Unmarshal(extra.Payload, &repaired) == nil {
				session.AppendSeenMessage(&repaired, extra.ID, 0)
			}
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

// restoreSession 首次升级时把近期原文接成可见工作集，不伪造旧工具轨迹
func (a *Agent) restoreSession(kind string, targetID int64, logs []memory.MessageLog) {
	if len(logs) == 0 {
		return
	}
	session := a.sessions.get(kind, targetID)
	seeds := make([]memory.TranscriptSeed, 0, len(logs))
	users := map[uint]*sessionUser{}
	selfID := a.bot.GetSelfID()
	for _, log := range logs {
		payloadMessage := msgToPayload(messageLogToSessionMessage(log))
		if payloadMessage == nil {
			continue
		}
		if log.UserID == selfID {
			payloadMessage = assistantHistoryMessage(log)
		} else {
			msg := messageLogToSessionMessage(log)
			session.mu.Lock()
			user := session.newUserLocked(msg, log.ID, log.MediaRefs, log.MessageRef)
			session.mu.Unlock()
			payloadMessage = user.Msg
			users[log.ID] = user
		}
		payload, err := sonic.Marshal(payloadMessage)
		if err != nil {
			continue
		}
		entryKind := "user"
		if log.UserID == selfID {
			entryKind = "assistant"
		}
		seeds = append(seeds, memory.TranscriptSeed{EntryKind: entryKind, MessageLogID: log.ID, Payload: payload, CreatedAt: log.MessageTime})
	}
	entries, created, err := a.memory.SeedTranscriptIfEmpty(a.ctx, kind, targetID, seeds)
	if err != nil || !created {
		return
	}
	for _, entry := range entries {
		if entry.EntryKind == "user" {
			var logID uint
			if entry.MessageLogID != nil {
				logID = *entry.MessageLogID
			}
			user := users[logID]
			if user == nil {
				continue
			}
			user.TranscriptID = entry.ID
			session.mu.Lock()
			session.seen = append(session.seen, sessionEntry{user: user, msg: user.Msg, transcriptID: entry.ID, visibleSeq: valueOrZero(entry.VisibleSeq)})
			session.mu.Unlock()
			continue
		}
		var message schema.AgenticMessage
		if sonic.Unmarshal(entry.Payload, &message) == nil {
			session.AppendSeenMessage(&message, entry.ID, valueOrZero(entry.VisibleSeq))
		}
	}
}

// trimRestoreMessages 首次升级时只接续预算内的近期原文，优先保留最新消息
func trimRestoreMessages(logs []memory.MessageLog, selfID int64) []memory.MessageLog {
	if len(logs) == 0 {
		return logs
	}
	cfg := config.Get()
	if cfg == nil || cfg.ModelTiers.High.ContextWindow <= 0 || cfg.Agent.Compaction.RecentTailRatio <= 0 {
		return logs
	}
	budget := int(float64(cfg.ModelTiers.High.ContextWindow) * cfg.Agent.Compaction.RecentTailRatio)
	if budget <= 0 {
		return logs
	}
	keepFrom := len(logs)
	total := 0
	for i := len(logs) - 1; i >= 0; i-- {
		var payload *schema.AgenticMessage
		if logs[i].UserID == selfID {
			payload = assistantHistoryMessage(logs[i])
		} else {
			payload = msgToPayload(messageLogToSessionMessage(logs[i]))
		}
		cost := messageTokens(payload)
		if keepFrom < len(logs) && total+cost > budget {
			break
		}
		total += cost
		keepFrom = i
	}
	return logs[keepFrom:]
}

func transcriptReferenceVersions(payload []byte) []string {
	var message schema.AgenticMessage
	if sonic.Unmarshal(payload, &message) != nil || message.Extra == nil {
		return nil
	}
	raw, err := sonic.Marshal(message.Extra["reference_versions"])
	if err != nil {
		return nil
	}
	var versions []string
	if sonic.Unmarshal(raw, &versions) != nil {
		return nil
	}
	return versions
}

func assistantHistoryMessage(log memory.MessageLog) *schema.AgenticMessage {
	text := strings.TrimSpace(log.DisplayContent)
	if text == "" {
		text = strings.TrimSpace(log.TextContent)
	}
	return &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{{Type: schema.ContentBlockTypeAssistantGenText, AssistantGenText: &schema.AssistantGenText{Text: text}}}}
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

// messageLogToSessionMessage 把消息库记录转成会话消息
func messageLogToSessionMessage(log memory.MessageLog) *onebot.ConversationMessage {
	return &onebot.ConversationMessage{
		MessageID:        log.OneBotMessageID,
		ConversationKind: log.ConversationKind,
		TargetID:         log.TargetID,
		UserID:           log.UserID,
		Nickname:         log.Nickname,
		Content:          log.TextContent,
		FinalContent:     log.DisplayContent,
		IsMentioned:      log.IsMentioned,
		Time:             log.MessageTime,
	}
}
