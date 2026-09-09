package topic

import (
	"strings"

	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
)

const TailKeepMessages = 8

func EmptySummary() memory.TopicSummary {
	return memory.TopicSummary{Version: 1, Participants: []memory.TopicParticipant{}, OpenLoops: []string{}, RecentTurns: []string{}, Keywords: []string{}}
}

func ParseSummary(raw string) memory.TopicSummary {
	summary := EmptySummary()
	if strings.TrimSpace(raw) == "" {
		return summary
	}
	if err := sonic.UnmarshalString(raw, &summary); err != nil {
		return EmptySummary()
	}
	if summary.Version == 0 {
		summary.Version = 1
	}
	return summary
}
