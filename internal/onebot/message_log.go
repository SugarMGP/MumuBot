package onebot

import (
	"mumu-bot/internal/memory"
	"strconv"
)

func MessageLogToGroupMessage(log memory.MessageLog) *GroupMessage {
	messageID, _ := strconv.ParseInt(log.MessageID, 10, 64)
	return &GroupMessage{
		MessageID:   messageID,
		GroupID:     log.GroupID,
		UserID:      log.UserID,
		Nickname:    log.Nickname,
		Time:        log.CreatedAt,
		MessageType: log.MsgType,
	}
}
