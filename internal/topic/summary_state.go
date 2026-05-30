package topic

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"time"
	"unicode"

	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
)

func EmptySummary() memory.TopicSummary {
	return memory.TopicSummary{
		Version:      1,
		Facts:        []string{},
		Participants: []memory.TopicSummaryParticipant{},
		OpenLoops:    []string{},
		RecentTurns:  []string{},
		Keywords:     []string{},
	}
}

func MarshalSummary(summary memory.TopicSummary) (string, error) {
	if summary.Version == 0 {
		summary.Version = 1
	}
	if summary.Facts == nil {
		summary.Facts = []string{}
	}
	if summary.Participants == nil {
		summary.Participants = []memory.TopicSummaryParticipant{}
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
	summary.ItemMeta = normalizeSummaryItemMeta(summary)
	return sonic.MarshalString(summary)
}

func mustMarshalSummary(summary memory.TopicSummary) string {
	raw, err := MarshalSummary(summary)
	if err != nil {
		return `{"version":1,"title":"","gist":"","facts":[],"participants":[],"open_loops":[],"recent_turns":[],"keywords":[]}`
	}
	return raw
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
	if summary.Facts == nil {
		summary.Facts = []string{}
	}
	if summary.Participants == nil {
		summary.Participants = []memory.TopicSummaryParticipant{}
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
	summary.ItemMeta = normalizeExistingSummaryItemMeta(summary)
	return summary
}

func normalizeExistingSummaryItemMeta(summary memory.TopicSummary) []memory.TopicSummaryItemMeta {
	if len(summary.ItemMeta) == 0 {
		return []memory.TopicSummaryItemMeta{}
	}
	return normalizeSummaryItemMeta(summary)
}

func normalizeSummaryItemMeta(summary memory.TopicSummary) []memory.TopicSummaryItemMeta {
	result := make([]memory.TopicSummaryItemMeta, 0, len(summary.Facts)+len(summary.OpenLoops))
	oldByText := make(map[string]memory.TopicSummaryItemMeta, len(summary.ItemMeta))
	for _, meta := range summary.ItemMeta {
		text := strings.TrimSpace(meta.Text)
		if text == "" {
			continue
		}
		meta.Text = text
		meta.Kind = strings.TrimSpace(meta.Kind)
		meta.SlotKind = strings.TrimSpace(meta.SlotKind)
		meta.Signature = strings.TrimSpace(meta.Signature)
		oldByText[normalizeContentForKey(text)] = meta
	}
	push := func(kind string, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		key := normalizeContentForKey(text)
		meta := oldByText[key]
		meta.Text = text
		meta.Kind = kind
		if meta.Signature == "" {
			meta.Signature = buildSummaryFactKey(text)
		}
		result = append(result, meta)
	}
	for _, fact := range summary.Facts {
		push("fact", fact)
	}
	for _, loop := range summary.OpenLoops {
		push("open_loop", loop)
	}
	return result
}

func MergeSummaryItemMeta(oldSummary memory.TopicSummary, nextSummary memory.TopicSummary) []memory.TopicSummaryItemMeta {
	nextSummary.ItemMeta = append([]memory.TopicSummaryItemMeta(nil), oldSummary.ItemMeta...)
	return normalizeSummaryItemMeta(nextSummary)
}

func MarshalSummaryItemMetaForPrompt(summary memory.TopicSummary) (string, error) {
	return sonic.MarshalString(normalizeSummaryItemMeta(summary))
}

func ParseSummaryHistory(raw string) []memory.TopicSummarySnapshot {
	if strings.TrimSpace(raw) == "" {
		return []memory.TopicSummarySnapshot{}
	}
	var snapshots []memory.TopicSummarySnapshot
	if err := sonic.UnmarshalString(raw, &snapshots); err != nil {
		return []memory.TopicSummarySnapshot{}
	}
	return snapshots
}

func DefaultSummaryJSON() string {
	return mustMarshalSummary(EmptySummary())
}

func DefaultSummaryHistoryJSON() string {
	return "[]"
}

func AppendSummaryHistory(raw string, summary memory.TopicSummary, capturedAt time.Time) (string, error) {
	history := ParseSummaryHistory(raw)
	history = append(history, memory.TopicSummarySnapshot{
		CapturedAt: capturedAt.Format(time.RFC3339),
		Summary:    ParseSummary(mustMarshalSummary(summary)),
	})
	if len(history) > SummaryHistoryLimit {
		history = history[len(history)-SummaryHistoryLimit:]
	}
	return sonic.MarshalString(history)
}

func buildSummaryFactKey(content string) string {
	normalized := normalizeContentForKey(content)
	if normalized == "" {
		return ""
	}
	hash := sha1.Sum([]byte(normalized))
	return hex.EncodeToString(hash[:])[:20]
}

func normalizeContentForKey(raw string) string {
	text := strings.TrimSpace(strings.ToLower(raw))
	if text == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range text {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
