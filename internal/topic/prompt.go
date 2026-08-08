package topic

import (
	"fmt"
	"strings"

	"mumu-bot/internal/memory"
)

func renderTopicPromptSection(topic memory.TopicThread, summary memory.TopicSummary, tail []memory.MessageLog) string {
	title := strings.TrimSpace(summary.Title)
	if title == "" {
		title = fmt.Sprintf("话题 %d", topic.ID)
	}
	lines := []string{"### " + title}
	if summary.Gist != "" {
		lines = append(lines, "概况："+summary.Gist)
	}
	if len(summary.Claims) > 0 {
		claims := make([]string, 0, len(summary.Claims))
		for _, claim := range summary.Claims {
			claims = append(claims, fmt.Sprintf("[%s] %s", claim.Kind, claim.Content))
		}
		lines = append(lines, "已确认："+strings.Join(claims, "；"))
	}
	if len(summary.OpenLoops) > 0 {
		lines = append(lines, "未完事项："+strings.Join(summary.OpenLoops, "；"))
	}
	if tailText := renderMessageTail(tail, 4); tailText != "" {
		lines = append(lines, "关键原文：\n"+tailText)
	}
	return strings.Join(lines, "\n")
}

func renderMessageTail(messages []memory.MessageLog, limit int) string {
	if len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	lines := make([]string, 0, len(messages))
	for _, item := range messages {
		text := strings.TrimSpace(item.TextContent)
		if text != "" {
			lines = append(lines, item.Nickname+"："+text)
		}
	}
	return strings.Join(lines, "\n")
}

func renderTopicSummaryForAssignment(summary memory.TopicSummary) string {
	parts := []string{strings.TrimSpace(summary.Title), strings.TrimSpace(summary.Gist)}
	if len(summary.Claims) > 0 {
		claims := make([]string, 0, len(summary.Claims))
		for _, claim := range summary.Claims {
			claims = append(claims, claim.Content)
		}
		parts = append(parts, strings.Join(claims, "；"))
	}
	if len(summary.OpenLoops) > 0 {
		parts = append(parts, "未完事项："+strings.Join(summary.OpenLoops, "；"))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
