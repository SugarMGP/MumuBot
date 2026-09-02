package memory

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

var (
	legacySenderPrefix = regexp.MustCompile(`^.*?\((-?\d+|你)\):\s*`)
	legacyReplyPrefix  = regexp.MustCompile(`^\[回复 #(-?\d+)(?: [^\]]*)?\]\s*`)
)

func migrateV3(db *gorm.DB) error {
	var rows []MessageLog
	if err := db.Where("recalled_at IS NULL").Find(&rows).Error; err != nil {
		return fmt.Errorf("读取待整理消息失败: %w", err)
	}
	for _, row := range rows {
		content, replyID, ok := normalizeLegacyDisplayContent(row)
		if !ok {
			continue
		}
		updates := map[string]interface{}{"display_content": content}
		if row.ReplyToMessageID == nil && replyID != nil {
			updates["reply_to_message_id"] = *replyID
		}
		if err := db.Model(&MessageLog{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
			return fmt.Errorf("整理消息 %d 失败: %w", row.ID, err)
		}
	}
	return nil
}

func normalizeLegacyDisplayContent(row MessageLog) (string, *int64, bool) {
	content := strings.TrimSpace(row.DisplayContent)
	if !strings.HasPrefix(content, "[") {
		return content, nil, false
	}
	headerEnd := strings.Index(content, "] #")
	if headerEnd < 0 {
		return content, nil, false
	}
	idStart := headerEnd + 3
	idEnd := strings.IndexByte(content[idStart:], ' ')
	if idEnd < 0 {
		return content, nil, false
	}
	id, err := strconv.ParseInt(content[idStart:idStart+idEnd], 10, 64)
	if err != nil || id != row.OneBotMessageID {
		return content, nil, false
	}
	content = content[idStart+idEnd+1:]
	match := legacySenderPrefix.FindStringIndex(content)
	if match == nil {
		return content, nil, false
	}
	content = content[match[1]:]
	var replyID *int64
	if match := legacyReplyPrefix.FindStringSubmatch(content); match != nil {
		id, err := strconv.ParseInt(match[1], 10, 64)
		if err == nil {
			replyID = &id
		}
		content = content[len(match[0]):]
	}
	return strings.TrimSpace(content), replyID, true
}
