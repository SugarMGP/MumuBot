package agent

import (
	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"strconv"
	"strings"
)

func contextClassificationMessageWindowSize(bufferSize int) int {
	if bufferSize <= 0 {
		cfg := config.Get()
		if cfg != nil {
			bufferSize = cfg.Agent.MessageBufferSize
		}
	}

	window := bufferSize / 2
	if window < 10 {
		return 10
	}
	if window > 30 {
		return 30
	}
	return window
}

func trimContextClassificationMessages(msgs []*onebot.GroupMessage, bufferSize int) []*onebot.GroupMessage {
	window := contextClassificationMessageWindowSize(bufferSize)
	if len(msgs) <= window {
		return msgs
	}
	return msgs[len(msgs)-window:]
}

func replyTargetsSelf(reply *onebot.ReplyInfo, selfID int64) bool {
	return reply != nil && reply.SenderID != 0 && selfID != 0 && reply.SenderID == selfID
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

func messageLogBaseGroupMessage(log memory.MessageLog) *onebot.GroupMessage {
	messageID, _ := strconv.ParseInt(log.MessageID, 10, 64)
	return &onebot.GroupMessage{
		MessageID:   messageID,
		GroupID:     log.GroupID,
		UserID:      log.UserID,
		Nickname:    log.Nickname,
		Time:        log.CreatedAt,
		MessageType: log.MsgType,
	}
}
