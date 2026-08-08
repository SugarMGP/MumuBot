package views

import (
	"fmt"
	"mumu-bot/internal/memory"
	topicpkg "mumu-bot/internal/topic"
	"mumu-bot/internal/web/services"
	"strings"
	"time"
)

func topicSummary(topic services.TopicThreadView) memory.TopicSummary {
	latest := topic.LatestSummary()
	if latest == nil {
		return memory.TopicSummary{}
	}
	return topicpkg.ParseSummary(latest.SummaryJSON)
}

type topicSummarySnapshot struct {
	CapturedAt string
	Summary    memory.TopicSummary
}

func topicSummaryHistory(topic services.TopicThreadView) []topicSummarySnapshot {
	out := make([]topicSummarySnapshot, 0, len(topic.Summaries))
	for _, record := range topic.Summaries {
		out = append(out, topicSummarySnapshot{CapturedAt: record.CreatedAt.Format(time.RFC3339), Summary: topicpkg.ParseSummary(record.SummaryJSON)})
	}
	return out
}

func TopicSummaryChanges(topic services.TopicThreadView) []TopicSummaryChangeView {
	history := topicSummaryHistory(topic)
	if len(history) == 0 {
		return nil
	}

	changes := make([]TopicSummaryChangeView, 0, len(history))
	for idx, snapshot := range history {
		var prev *topicSummarySnapshot
		if idx > 0 {
			prev = &history[idx-1]
		}
		change := buildTopicSummaryChangeView(prev, snapshot)
		if !change.Changed {
			continue
		}
		changes = append(changes, change)
	}
	for left, right := 0, len(changes)-1; left < right; left, right = left+1, right-1 {
		changes[left], changes[right] = changes[right], changes[left]
	}
	if len(changes) > 0 {
		changes[0].InitiallyOpen = true
	}
	return changes
}

func buildTopicSummaryChangeView(prev *topicSummarySnapshot, current topicSummarySnapshot) TopicSummaryChangeView {
	currentTitle := strings.TrimSpace(current.Summary.Title)
	currentGist := strings.TrimSpace(current.Summary.Gist)
	view := TopicSummaryChangeView{
		CapturedAtLabel: formatRFC3339Time(current.CapturedAt),
		CapturedAtValue: formatRFC3339TimeAttr(current.CapturedAt),
		CurrentTitle:    currentTitle,
		CurrentGist:     currentGist,
	}

	if prev == nil {
		view.InitialSnapshot = true
		view.TitleChanged = currentTitle != ""
		view.GistChanged = currentGist != ""
		view.AddedClaims = topicClaimItems(current.Summary.Claims)
		view.AddedOpenLoops = normalizedTopicSummaryItems(current.Summary.OpenLoops)
		view.Changed = view.TitleChanged || view.GistChanged || len(view.AddedClaims) > 0 || len(view.AddedOpenLoops) > 0
		view.TitleDiff = buildAddedTextDiffView(currentTitle, "之前没有标题", "现在没有标题")
		view.GistDiff = buildAddedTextDiffView(currentGist, "之前没有概括", "现在没有概括")
		view.Headline = topicSummaryChangeHeadline(view)
		view.Badges = topicSummaryChangeBadges(view)
		return view
	}

	prevTitle := strings.TrimSpace(prev.Summary.Title)
	prevGist := strings.TrimSpace(prev.Summary.Gist)
	view.PreviousTitle = prevTitle
	view.PreviousGist = prevGist
	view.TitleChanged = prevTitle != currentTitle
	view.GistChanged = prevGist != currentGist
	view.TitleDiff = buildTopicTextDiffView(prevTitle, currentTitle, "之前没有标题", "现在没有标题")
	view.GistDiff = buildTopicTextDiffView(prevGist, currentGist, "之前没有概括", "现在没有概括")
	view.AddedClaims, view.RemovedClaims = diffStringSet(topicClaimItems(prev.Summary.Claims), topicClaimItems(current.Summary.Claims))
	view.AddedOpenLoops, view.RemovedOpenLoops = diffStringSet(prev.Summary.OpenLoops, current.Summary.OpenLoops)
	view.Changed = view.TitleChanged || view.GistChanged || len(view.AddedClaims) > 0 || len(view.RemovedClaims) > 0 || len(view.AddedOpenLoops) > 0 || len(view.RemovedOpenLoops) > 0
	view.Headline = topicSummaryChangeHeadline(view)
	view.Badges = topicSummaryChangeBadges(view)
	return view
}

func topicClaimItems(claims []memory.MemoryClaim) []string {
	items := make([]string, 0, len(claims))
	for _, claim := range claims {
		content := strings.TrimSpace(claim.Content)
		if content != "" {
			items = append(items, fmt.Sprintf("%d|%s|%s", claim.SubjectUserID, claim.Kind, content))
		}
	}
	return normalizedTopicSummaryItems(items)
}

func topicClaimDisplay(item string) string {
	parts := strings.SplitN(item, "|", 3)
	if len(parts) == 3 {
		return parts[2]
	}
	return item
}

func buildTopicTextDiffView(previous string, current string, previousPlaceholder string, currentPlaceholder string) TopicTextDiffView {
	previous = strings.TrimSpace(previous)
	current = strings.TrimSpace(current)
	if previous == current {
		segments := diffSegmentsForText(previous, "equal")
		return TopicTextDiffView{
			PreviousSegments:    diffSegmentsForText(previous, "equal"),
			CurrentSegments:     diffSegmentsForText(current, "equal"),
			InlineSegments:      append([]TopicTextDiffSegmentView(nil), segments...),
			PreviousPlaceholder: previousPlaceholder,
			CurrentPlaceholder:  currentPlaceholder,
			PreviousEmpty:       previous == "",
			CurrentEmpty:        current == "",
		}
	}

	diffView := TopicTextDiffView{
		PreviousPlaceholder: previousPlaceholder,
		CurrentPlaceholder:  currentPlaceholder,
		PreviousEmpty:       previous == "",
		CurrentEmpty:        current == "",
	}
	if previous == "" {
		diffView.CurrentSegments = diffSegmentsForText(current, "add")
		diffView.InlineSegments = append(diffView.InlineSegments, TopicTextDiffSegmentView{Text: current, Kind: "add"})
		return diffView
	}
	if current == "" {
		diffView.PreviousSegments = diffSegmentsForText(previous, "remove")
		diffView.InlineSegments = append(diffView.InlineSegments, TopicTextDiffSegmentView{Text: previous, Kind: "remove"})
		return diffView
	}

	prevPrefix, prevMiddle, prevSuffix, currPrefix, currMiddle, currSuffix := topicCommonTextDiff(previous, current)
	if prevPrefix != "" {
		appendDiffSegment(&diffView.PreviousSegments, "equal", prevPrefix)
		appendDiffSegment(&diffView.CurrentSegments, "equal", currPrefix)
		appendDiffSegment(&diffView.InlineSegments, "equal", prevPrefix)
	}
	if prevMiddle != "" {
		appendDiffSegment(&diffView.PreviousSegments, "remove", prevMiddle)
		appendDiffSegment(&diffView.InlineSegments, "remove", prevMiddle)
	}
	if currMiddle != "" {
		appendDiffSegment(&diffView.CurrentSegments, "add", currMiddle)
		appendDiffSegment(&diffView.InlineSegments, "add", currMiddle)
	}
	if prevSuffix != "" {
		appendDiffSegment(&diffView.PreviousSegments, "equal", prevSuffix)
		appendDiffSegment(&diffView.CurrentSegments, "equal", currSuffix)
		appendDiffSegment(&diffView.InlineSegments, "equal", prevSuffix)
	}
	return diffView
}

func buildAddedTextDiffView(current string, previousPlaceholder string, currentPlaceholder string) TopicTextDiffView {
	current = strings.TrimSpace(current)
	return TopicTextDiffView{
		PreviousPlaceholder: previousPlaceholder,
		CurrentPlaceholder:  currentPlaceholder,
		PreviousEmpty:       true,
		CurrentEmpty:        current == "",
		CurrentSegments:     diffSegmentsForText(current, "add"),
		InlineSegments:      diffSegmentsForText(current, "add"),
	}
}

func diffSegmentsForText(text string, kind string) []TopicTextDiffSegmentView {
	if text == "" {
		return nil
	}
	return []TopicTextDiffSegmentView{{Text: text, Kind: kind}}
}

func appendDiffSegment(target *[]TopicTextDiffSegmentView, kind string, text string) {
	if text == "" {
		return
	}
	if len(*target) > 0 {
		last := &(*target)[len(*target)-1]
		if last.Kind == kind {
			last.Text += text
			return
		}
	}
	*target = append(*target, TopicTextDiffSegmentView{Text: text, Kind: kind})
}

func topicCommonTextDiff(previous string, current string) (string, string, string, string, string, string) {
	prevRunes := []rune(previous)
	currRunes := []rune(current)

	prefix := 0
	for prefix < len(prevRunes) && prefix < len(currRunes) && prevRunes[prefix] == currRunes[prefix] {
		prefix++
	}

	suffix := 0
	for suffix < len(prevRunes)-prefix && suffix < len(currRunes)-prefix && prevRunes[len(prevRunes)-1-suffix] == currRunes[len(currRunes)-1-suffix] {
		suffix++
	}

	return string(prevRunes[:prefix]), string(prevRunes[prefix : len(prevRunes)-suffix]), string(prevRunes[len(prevRunes)-suffix:]), string(currRunes[:prefix]), string(currRunes[prefix : len(currRunes)-suffix]), string(currRunes[len(currRunes)-suffix:])
}

func topicSummaryChangeHeadline(change TopicSummaryChangeView) string {
	if change.InitialSnapshot {
		return "首次生成摘要快照"
	}
	parts := make([]string, 0, 4)
	if change.TitleChanged {
		parts = append(parts, "标题改了")
	}
	if change.GistChanged {
		parts = append(parts, "概括改了")
	}
	if count := len(change.AddedOpenLoops) + len(change.RemovedOpenLoops); count > 0 {
		parts = append(parts, fmt.Sprintf("未完事项调整 %d 项", count))
	}
	if count := len(change.AddedClaims) + len(change.RemovedClaims); count > 0 {
		parts = append(parts, fmt.Sprintf("已确认事项调整 %d 项", count))
	}
	if len(parts) == 0 {
		return "摘要内容有更新"
	}
	return strings.Join(parts, " · ")
}

func topicSummaryChangeBadges(change TopicSummaryChangeView) []TopicSummaryChangeBadgeView {
	badges := make([]TopicSummaryChangeBadgeView, 0, 5)
	if change.TitleChanged {
		badges = append(badges, TopicSummaryChangeBadgeView{Label: "标题变化", Tone: "cyan"})
	}
	if change.GistChanged {
		badges = append(badges, TopicSummaryChangeBadgeView{Label: "概括变化", Tone: "teal"})
	}
	if len(change.AddedOpenLoops) > 0 || len(change.RemovedOpenLoops) > 0 {
		badges = append(badges, TopicSummaryChangeBadgeView{Label: "未完事项", Tone: "amber"})
	}
	if len(change.AddedClaims) > 0 || len(change.RemovedClaims) > 0 {
		badges = append(badges, TopicSummaryChangeBadgeView{Label: "已确认事项", Tone: "emerald"})
	}
	return badges
}

func topicChangeBadgeClass(tone string) string {
	base := "badge badge-sm badge-soft"
	switch strings.TrimSpace(tone) {
	case "cyan":
		return joinClasses(base, "badge-info")
	case "teal":
		return joinClasses(base, "badge-success")
	case "amber":
		return joinClasses(base, "badge-warning")
	case "emerald":
		return joinClasses(base, "badge-success")
	default:
		return joinClasses(base, "badge-ghost")
	}
}

func topicInlineDiffClass(kind string) string {
	switch strings.TrimSpace(kind) {
	case "add":
		return "admin-topic-inline-diff__segment admin-topic-inline-diff__segment--add"
	case "remove":
		return "admin-topic-inline-diff__segment admin-topic-inline-diff__segment--remove"
	default:
		return "admin-topic-inline-diff__segment admin-topic-inline-diff__segment--equal"
	}
}

func topicSummaryChangeCountLabel(topic services.TopicThreadView, changes []TopicSummaryChangeView) string {
	historyCount := len(topicSummaryHistory(topic))
	if historyCount == 0 {
		return "还没有摘要快照"
	}
	changed := 0
	for _, change := range changes {
		if change.Changed {
			changed++
		}
	}
	if changed == 0 {
		return "有摘要快照，但还没有内容变化"
	}
	return fmt.Sprintf("共 %d 次内容变化", changed)
}

func diffStringSet(before []string, after []string) ([]string, []string) {
	beforeItems := normalizedTopicSummaryItems(before)
	afterItems := normalizedTopicSummaryItems(after)
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

func normalizedTopicSummaryItems(items []string) []string {
	if len(items) == 0 {
		return nil
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
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func topicTitle(topic services.TopicThreadView) string {
	title := strings.TrimSpace(topicSummary(topic).Title)
	if title != "" {
		return title
	}
	return fmt.Sprintf("话题 #%d", topic.ID)
}

func topicGist(topic services.TopicThreadView) string {
	gist := strings.TrimSpace(topicSummary(topic).Gist)
	if gist != "" {
		return gist
	}
	return "这条话题还没有生成摘要。"
}

func topicKeywords(topic services.TopicThreadView) []string {
	return topicSummary(topic).Keywords
}

func topicSummaryProgressText(topic services.TopicThreadView) string {
	latest := topic.LatestSummary()
	if latest != nil && latest.ThroughTopicAssignmentID >= topic.LastAssignmentID {
		return "摘要已覆盖最新消息"
	}
	return "还有新消息待补进摘要"
}

func topicSummaryProgressClass(topic services.TopicThreadView) string {
	latest := topic.LatestSummary()
	if latest != nil && latest.ThroughTopicAssignmentID >= topic.LastAssignmentID {
		return "badge badge-success badge-soft badge-sm"
	}
	return "badge badge-warning badge-soft badge-sm"
}

func topicTone(topic services.TopicThreadView) string {
	latest := topic.LatestSummary()
	if latest != nil && latest.ThroughTopicAssignmentID >= topic.LastAssignmentID {
		return "success"
	}
	return "primary"
}

func topicSummaryThroughID(topic services.TopicThreadView) uint {
	latest := topic.LatestSummary()
	if latest == nil {
		return 0
	}
	return latest.ThroughTopicAssignmentID
}

func topicMessageText(log memory.MessageLog) string {
	text := strings.TrimSpace(log.TextContent)
	if text != "" {
		return text
	}
	text = strings.TrimSpace(log.DisplayContent)
	if text != "" {
		return text
	}
	return "暂无内容"
}

func EmptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func isPlaceholderText(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(strings.Trim(value, "\"'`")))
	switch normalized {
	case "", "-", "--", "null", "nil", "none", "n/a", "na", "undefined", "unknown", "[]", "{}":
		return true
	default:
		return false
	}
}

func displayText(value string, fallback string) string {
	trimmed := strings.TrimSpace(strings.Trim(value, "\"'`"))
	if isPlaceholderText(trimmed) {
		return fallback
	}
	return trimmed
}
