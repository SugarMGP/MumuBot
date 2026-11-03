package app

import (
	"strings"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/services"

	"github.com/bytedance/sonic"
)

type topicSummarySnapshot struct {
	CapturedAt time.Time
	Summary    memory.TopicSummary
}

func topicSummaryOf(thread services.TopicThreadView) memory.TopicSummary {
	latest := thread.LatestSummary()
	if latest == nil {
		return memory.TopicSummary{}
	}
	var summary memory.TopicSummary
	if sonic.UnmarshalString(latest.SummaryJSON, &summary) != nil {
		return memory.TopicSummary{}
	}
	return summary
}

func topicSummaryHistory(thread services.TopicThreadView) []topicSummarySnapshot {
	out := make([]topicSummarySnapshot, 0, len(thread.Summaries))
	for _, record := range thread.Summaries {
		var summary memory.TopicSummary
		if sonic.UnmarshalString(record.SummaryJSON, &summary) != nil {
			continue
		}
		out = append(out, topicSummarySnapshot{CapturedAt: record.CreatedAt, Summary: summary})
	}
	return out
}

func topicListItem(thread services.TopicThreadView) topicListItemDTO {
	summary := topicSummaryOf(thread)
	keywords := summary.Keywords
	if keywords == nil {
		keywords = []string{}
	}
	return topicListItemDTO{
		ID:               thread.ID,
		Title:            strings.TrimSpace(summary.Title),
		Gist:             strings.TrimSpace(summary.Gist),
		Keywords:         keywords,
		Status:           thread.Status,
		ConversationKind: thread.ConversationKind,
		TargetID:         thread.TargetID,
		MessageCount:     thread.MessageCount,
		AnchorCount:      len(thread.AnchorMessageIDs),
		SourceCount:      len(thread.SourceMessageIDs),
		OpenLoopCount:    len(summary.OpenLoops),
		UpdatedAt:        thread.UpdatedAt,
	}
}

func topicSummaryChanges(thread services.TopicThreadView) []topicSummaryChangeDTO {
	history := topicSummaryHistory(thread)
	changes := make([]topicSummaryChangeDTO, 0, len(history))
	for index, snapshot := range history {
		var previous *topicSummarySnapshot
		if index > 0 {
			previous = &history[index-1]
		}
		change, changed := buildTopicSummaryChange(previous, snapshot)
		if !changed {
			continue
		}
		changes = append(changes, change)
	}
	for left, right := 0, len(changes)-1; left < right; left, right = left+1, right-1 {
		changes[left], changes[right] = changes[right], changes[left]
	}
	return changes
}

func buildTopicSummaryChange(previous *topicSummarySnapshot, current topicSummarySnapshot) (topicSummaryChangeDTO, bool) {
	currentTitle := strings.TrimSpace(current.Summary.Title)
	currentGist := strings.TrimSpace(current.Summary.Gist)
	change := topicSummaryChangeDTO{
		CapturedAt:       current.CapturedAt,
		AddedOpenLoops:   []string{},
		RemovedOpenLoops: []string{},
	}
	if previous == nil {
		change.Initial = true
		change.Title = addedTextDiff(currentTitle)
		change.Gist = addedTextDiff(currentGist)
		change.AddedOpenLoops = normalizedTopicItems(current.Summary.OpenLoops)
		changed := change.Title.Changed || change.Gist.Changed || len(change.AddedOpenLoops) > 0
		return change, changed
	}
	previousTitle := strings.TrimSpace(previous.Summary.Title)
	previousGist := strings.TrimSpace(previous.Summary.Gist)
	change.Title = textDiff(previousTitle, currentTitle)
	change.Gist = textDiff(previousGist, currentGist)
	change.AddedOpenLoops, change.RemovedOpenLoops = diffTopicItems(previous.Summary.OpenLoops, current.Summary.OpenLoops)
	changed := change.Title.Changed || change.Gist.Changed ||
		len(change.AddedOpenLoops) > 0 || len(change.RemovedOpenLoops) > 0
	return change, changed
}

func textDiff(previous string, current string) topicTextDiff {
	previous = strings.TrimSpace(previous)
	current = strings.TrimSpace(current)
	if previous == current {
		return topicTextDiff{Segments: segmentsForText(previous, "equal")}
	}
	if previous == "" {
		return topicTextDiff{Changed: true, Segments: segmentsForText(current, "add")}
	}
	if current == "" {
		return topicTextDiff{Changed: true, Segments: segmentsForText(previous, "remove")}
	}
	segments := make([]topicDiffSegment, 0, 4)
	prefix, previousMiddle, currentMiddle, suffix := commonTextDiff(previous, current)
	segments = appendSegment(segments, "equal", prefix)
	segments = appendSegment(segments, "remove", previousMiddle)
	segments = appendSegment(segments, "add", currentMiddle)
	segments = appendSegment(segments, "equal", suffix)
	return topicTextDiff{Changed: true, Segments: segments}
}

func addedTextDiff(current string) topicTextDiff {
	current = strings.TrimSpace(current)
	return topicTextDiff{Changed: current != "", Segments: segmentsForText(current, "add")}
}

func segmentsForText(text string, kind string) []topicDiffSegment {
	if text == "" {
		return nil
	}
	return []topicDiffSegment{{Kind: kind, Text: text}}
}

func appendSegment(target []topicDiffSegment, kind string, text string) []topicDiffSegment {
	if text == "" {
		return target
	}
	if len(target) > 0 && target[len(target)-1].Kind == kind {
		target[len(target)-1].Text += text
		return target
	}
	return append(target, topicDiffSegment{Kind: kind, Text: text})
}

func commonTextDiff(previous string, current string) (string, string, string, string) {
	previousRunes := []rune(previous)
	currentRunes := []rune(current)
	prefix := 0
	for prefix < len(previousRunes) && prefix < len(currentRunes) && previousRunes[prefix] == currentRunes[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(previousRunes)-prefix && suffix < len(currentRunes)-prefix &&
		previousRunes[len(previousRunes)-1-suffix] == currentRunes[len(currentRunes)-1-suffix] {
		suffix++
	}
	return string(previousRunes[:prefix]),
		string(previousRunes[prefix : len(previousRunes)-suffix]),
		string(currentRunes[prefix : len(currentRunes)-suffix]),
		string(previousRunes[len(previousRunes)-suffix:])
}

func diffTopicItems(before []string, after []string) ([]string, []string) {
	beforeItems := normalizedTopicItems(before)
	afterItems := normalizedTopicItems(after)
	beforeSet := make(map[string]struct{}, len(beforeItems))
	afterSet := make(map[string]struct{}, len(afterItems))
	for _, item := range beforeItems {
		beforeSet[item] = struct{}{}
	}
	for _, item := range afterItems {
		afterSet[item] = struct{}{}
	}
	added := make([]string, 0)
	removed := make([]string, 0)
	for _, item := range afterItems {
		if _, ok := beforeSet[item]; !ok {
			added = append(added, item)
		}
	}
	for _, item := range beforeItems {
		if _, ok := afterSet[item]; !ok {
			removed = append(removed, item)
		}
	}
	return added, removed
}

func normalizedTopicItems(items []string) []string {
	if len(items) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(items))
	normalized := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	return normalized
}

func topicMessages(thread services.TopicThreadView, logs []memory.MessageLog) []topicMessageDTO {
	items := make([]topicMessageDTO, 0, len(logs))
	for _, log := range logs {
		items = append(items, topicMessageDTO{
			ID:          log.ID,
			Sender:      strings.TrimSpace(log.Nickname),
			UserID:      log.UserID,
			Text:        topicMessageText(log),
			MessageTime: log.MessageTime,
			Recalled:    log.RecalledAt != nil,
			Anchor:      containsUint(thread.AnchorMessageIDs, log.ID),
			Source:      containsUint(thread.SourceMessageIDs, log.ID),
		})
	}
	return items
}

func topicMessageText(log memory.MessageLog) string {
	if text := strings.TrimSpace(log.TextContent); text != "" {
		return text
	}
	return strings.TrimSpace(log.DisplayContent)
}

func containsUint(items []uint, target uint) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
