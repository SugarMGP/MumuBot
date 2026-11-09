package agent

import (
	"fmt"
	"strings"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
)

func renderReplyHeader(reply *onebot.ReplyInfo) string {
	var parts []string
	name := strings.Join(strings.Fields(resolveMessageDisplayName(reply.GroupCard, reply.Nickname)), " ")
	if reply.SenderID > 0 {
		name = fmt.Sprintf("%s(%d)", name, reply.SenderID)
	}
	if name != "" {
		parts = append(parts, name)
	}
	if reply.MessageID != 0 {
		parts = append(parts, fmt.Sprintf("消息 %d", reply.MessageID))
	}
	content := strings.Join(strings.Fields(reply.Content), " ")
	if content == memory.RecalledMessageDisplayContent {
		content = "已撤回"
	}
	if content == "" {
		content = "原文不可用"
	}
	runes := []rune(content)
	if len(runes) > 30 {
		content = string(runes[:29]) + "…"
	}
	prefix := "[回复"
	if len(parts) > 0 {
		prefix += " " + strings.Join(parts, " ")
	}
	return prefix + ": “" + content + "”] "
}
