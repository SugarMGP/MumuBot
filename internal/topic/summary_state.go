package topic

import (
	"strings"

	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
)

func EmptySummary() memory.TopicSummary {
	return memory.TopicSummary{Version: 1, Claims: []memory.MemoryClaim{}, Participants: []memory.TopicParticipant{}, OpenLoops: []string{}, RecentTurns: []string{}, Keywords: []string{}}
}

func MarshalSummary(summary memory.TopicSummary) (string, error) {
	if summary.Version == 0 {
		summary.Version = 1
	}
	if summary.Claims == nil {
		summary.Claims = []memory.MemoryClaim{}
	}
	if summary.Participants == nil {
		summary.Participants = []memory.TopicParticipant{}
	}
	if summary.OpenLoops == nil {
		summary.OpenLoops = []string{}
	}
	if summary.RecentTurns == nil {
		summary.RecentTurns = []string{}
	}
	if summary.Keywords == nil {
		summary.Keywords = []string{}
	}
	return sonic.MarshalString(summary)
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
