package agent

import (
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"strconv"
	"strings"
)

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
	return onebot.MessageLogToGroupMessage(log)
}
