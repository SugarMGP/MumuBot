package views

import (
	"fmt"
	"mumu-bot/internal/memory"
	neturl "net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bytedance/sonic"
)

func NavItems() []NavItem {
	return []NavItem{
		{Label: "总览", Href: "/admin"},
		{Label: "风格卡片", Href: "/admin/style-cards"},
		{Label: "黑话", Href: "/admin/jargons"},
		{Label: "表情包", Href: "/admin/stickers"},
		{Label: "话题", Href: "/admin/topics"},
		{Label: "记忆", Href: "/admin/memories"},
		{Label: "成员", Href: "/admin/members"},
		{Label: "系统", Href: "/admin/system"},
	}
}

func adminCSSHref() string {
	return "/assets/admin.css"
}

func adminJSHref() string {
	return "/assets/admin.js"
}

func joinClasses(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, " ")
}

func navClass(currentPath string, href string) string {
	base := "group inline-flex w-full items-center gap-3 rounded-2xl px-4 py-3 text-sm font-semibold transition duration-300 ease-out focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 focus-visible:ring-offset-2 focus-visible:ring-offset-white"
	active := currentPath == href
	if !active && href != "" && href != "/admin" && strings.HasPrefix(currentPath, href+"/") {
		active = true
	}
	if active {
		return joinClasses(base, "bg-[linear-gradient(135deg,#101a32_0%,#1e2c4f_58%,#0b7285_120%)] text-white shadow-[0_20px_36px_rgba(15,23,42,0.18)]")
	}
	return joinClasses(base, "text-slate-600 hover:bg-white/90 hover:text-slate-900")
}

func boolText(v bool) string {
	if v {
		return "已启用"
	}
	return "未启用"
}

func connectionText(v bool) string {
	if v {
		return "已连接"
	}
	return "未连接"
}

func flashJSON(flash *FlashMessage) string {
	if flash == nil {
		return ""
	}

	data, err := sonic.MarshalString(flash)
	if err != nil {
		return ""
	}
	return data
}

func navIconName(href string) string {
	switch strings.TrimSpace(href) {
	case "/admin":
		return "overview"
	case "/admin/style-cards":
		return "style-cards"
	case "/admin/jargons":
		return "jargons"
	case "/admin/stickers":
		return "stickers"
	case "/admin/topics":
		return "memories"
	case "/admin/memories":
		return "memories"
	case "/admin/members":
		return "members"
	case "/admin/system":
		return "system"
	default:
		return "overview"
	}
}

func systemSectionIconName(title string) string {
	switch strings.TrimSpace(title) {
	case "人格设定":
		return "persona"
	case "群配置", "启用群聊", "群聊与学习":
		return "group-config"
	case "行为与学习":
		return "behavior"
	case "模型接入", "智能能力", "能力概览", "模型能力":
		return "model"
	case "OneBot 连接", "连接服务", "消息连接", "连接状态", "连接与数据":
		return "connection"
	case "存储", "数据存储", "数据与检索", "数据状态":
		return "storage"
	case "后台服务", "后台访问", "后台安全", "登录与扩展":
		return "backend"
	default:
		return "system"
	}
}

func systemFieldCardClass(field SystemField) string {
	base := "rounded-[1.15rem] bg-slate-50/90 p-4 ring-1 ring-slate-200/80"
	if systemFieldNeedsWide(field) {
		return joinClasses(base, "sm:col-span-2")
	}
	return base
}

func systemFieldValueClass(field SystemField) string {
	base := "mt-2 break-words whitespace-pre-line text-sm leading-6 text-slate-700"
	if systemFieldNeedsWide(field) {
		return joinClasses(base, "font-normal")
	}
	return joinClasses(base, "font-medium")
}

func systemFieldNeedsWide(field SystemField) bool {
	switch strings.TrimSpace(field.Label) {
	case "已启用群聊", "自动学习", "审核节奏":
		return true
	}
	return len([]rune(strings.TrimSpace(field.Value))) > 32
}

func sortOrderIconName(label string) string {
	switch strings.TrimSpace(label) {
	case "正序":
		return "sort-asc"
	case "倒序":
		return "sort-desc"
	default:
		return "sort"
	}
}

func sortOrderAriaLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "切换排序顺序"
	}
	return "切换为" + label
}

func styleCardStatusText(status memory.StyleCardStatus) string {
	switch status {
	case memory.StyleCardStatusActive:
		return "已启用"
	case memory.StyleCardStatusRejected:
		return "已拒绝"
	default:
		return "候选"
	}
}

func styleCardStatusClass(status memory.StyleCardStatus) string {
	switch status {
	case memory.StyleCardStatusActive:
		return "inline-flex items-center rounded-full bg-emerald-100 px-3 py-1 text-xs font-semibold text-emerald-700 ring-1 ring-emerald-200"
	case memory.StyleCardStatusRejected:
		return "inline-flex items-center rounded-full bg-rose-100 px-3 py-1 text-xs font-semibold text-rose-700 ring-1 ring-rose-200"
	default:
		return "inline-flex items-center rounded-full bg-amber-100 px-3 py-1 text-xs font-semibold text-amber-700 ring-1 ring-amber-200"
	}
}

func jargonStatusText(item memory.Jargon) string {
	switch {
	case !item.Checked:
		return "待审核"
	case item.Rejected:
		return "已拒绝"
	default:
		return "已通过"
	}
}

func jargonStatusValue(item memory.Jargon) string {
	switch {
	case !item.Checked:
		return "pending"
	case item.Rejected:
		return "rejected"
	default:
		return "approved"
	}
}

func jargonStatusClass(item memory.Jargon) string {
	switch jargonStatusValue(item) {
	case "approved":
		return "inline-flex items-center rounded-full bg-emerald-100 px-3 py-1 text-xs font-semibold text-emerald-700 ring-1 ring-emerald-200"
	case "rejected":
		return "inline-flex items-center rounded-full bg-rose-100 px-3 py-1 text-xs font-semibold text-rose-700 ring-1 ring-rose-200"
	default:
		return "inline-flex items-center rounded-full bg-amber-100 px-3 py-1 text-xs font-semibold text-amber-700 ring-1 ring-amber-200"
	}
}

func formatTime(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.Format("2006-01-02 15:04")
}

func formatTimeAttr(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.Format(time.RFC3339)
}

func formatRFC3339Time(raw string) string {
	ts, ok := parseRFC3339Time(raw)
	if !ok {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return "-"
		}
		return trimmed
	}
	return formatTime(ts)
}

func formatRFC3339TimeAttr(raw string) string {
	ts, ok := parseRFC3339Time(raw)
	if !ok {
		return strings.TrimSpace(raw)
	}
	return formatTimeAttr(ts)
}

func parseRFC3339Time(raw string) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

func topicStatusText(status memory.TopicThreadStatus) string {
	switch status {
	case memory.TopicThreadStatusArchived:
		return "已归档"
	default:
		return "进行中"
	}
}

func topicStatusClass(status memory.TopicThreadStatus) string {
	switch status {
	case memory.TopicThreadStatusArchived:
		return "inline-flex items-center rounded-full bg-slate-100 px-3 py-1 text-xs font-semibold text-slate-600 ring-1 ring-slate-200"
	default:
		return "inline-flex items-center rounded-full bg-cyan-100 px-3 py-1 text-xs font-semibold text-cyan-700 ring-1 ring-cyan-200"
	}
}

func topicSummary(topic memory.TopicThread) memory.TopicSummaryV1 {
	return memory.ParseTopicSummary(topic.SummaryJSON)
}

func topicSummaryHistory(topic memory.TopicThread) []memory.TopicSummarySnapshot {
	return memory.ParseTopicSummaryHistory(topic.SummaryHistoryJSON)
}

func topicSummaryChanges(topic memory.TopicThread) []TopicSummaryChangeView {
	history := topicSummaryHistory(topic)
	if len(history) == 0 {
		return nil
	}

	changes := make([]TopicSummaryChangeView, 0, len(history))
	for idx, snapshot := range history {
		var prev *memory.TopicSummarySnapshot
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

func TopicSummaryChanges(topic memory.TopicThread) []TopicSummaryChangeView {
	return topicSummaryChanges(topic)
}

func buildTopicSummaryChangeView(prev *memory.TopicSummarySnapshot, current memory.TopicSummarySnapshot) TopicSummaryChangeView {
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
		view.AddedFacts = normalizedTopicSummaryItems(current.Summary.Facts)
		view.AddedOpenLoops = normalizedTopicSummaryItems(current.Summary.OpenLoops)
		view.Changed = view.TitleChanged || view.GistChanged || len(view.AddedFacts) > 0 || len(view.AddedOpenLoops) > 0
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
	view.AddedFacts, view.RemovedFacts = diffStringSet(prev.Summary.Facts, current.Summary.Facts)
	view.AddedOpenLoops, view.RemovedOpenLoops = diffStringSet(prev.Summary.OpenLoops, current.Summary.OpenLoops)
	view.Changed = view.TitleChanged || view.GistChanged || len(view.AddedFacts) > 0 || len(view.RemovedFacts) > 0 || len(view.AddedOpenLoops) > 0 || len(view.RemovedOpenLoops) > 0
	view.Headline = topicSummaryChangeHeadline(view)
	view.Badges = topicSummaryChangeBadges(view)
	return view
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
	if count := len(change.AddedFacts) + len(change.RemovedFacts); count > 0 {
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
	if len(change.AddedFacts) > 0 || len(change.RemovedFacts) > 0 {
		badges = append(badges, TopicSummaryChangeBadgeView{Label: "已确认事项", Tone: "emerald"})
	}
	return badges
}

func topicChangeBadgeClass(tone string) string {
	base := "inline-flex items-center rounded-full px-2.5 py-1 text-[11px] font-semibold ring-1"
	switch strings.TrimSpace(tone) {
	case "cyan":
		return joinClasses(base, "bg-cyan-50 text-cyan-700 ring-cyan-200/80")
	case "teal":
		return joinClasses(base, "bg-teal-50 text-teal-700 ring-teal-200/80")
	case "amber":
		return joinClasses(base, "bg-amber-50 text-amber-700 ring-amber-200/80")
	case "emerald":
		return joinClasses(base, "bg-emerald-50 text-emerald-700 ring-emerald-200/80")
	default:
		return joinClasses(base, "bg-slate-100 text-slate-600 ring-slate-200/80")
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

func topicSummaryChangeCountLabel(topic memory.TopicThread, changes []TopicSummaryChangeView) string {
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

func topicSummaryTimelineClass(changes []TopicSummaryChangeView) string {
	if len(changes) >= 5 {
		return "admin-topic-summary-timeline admin-topic-summary-timeline--compact"
	}
	return "admin-topic-summary-timeline"
}

func topicSummaryColumnClass(changes []TopicSummaryChangeView) string {
	_ = changes
	return "space-y-4"
}

func topicSummaryShellClass(changes []TopicSummaryChangeView) string {
	_ = changes
	return "grid gap-4 xl:grid-cols-[minmax(0,1.2fr)_minmax(22rem,0.8fr)] xl:items-start"
}

func topicDiffList(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	cloned := append([]string(nil), items...)
	sort.Strings(cloned)
	return cloned
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

func topicTitle(topic memory.TopicThread) string {
	title := strings.TrimSpace(topicSummary(topic).Title)
	if title != "" {
		return title
	}
	return fmt.Sprintf("话题 #%d", topic.ID)
}

func topicGist(topic memory.TopicThread) string {
	gist := strings.TrimSpace(topicSummary(topic).Gist)
	if gist != "" {
		return gist
	}
	if topic.Status == memory.TopicThreadStatusArchived {
		return "这条话题已经归档，目前没有更详细的概括。"
	}
	return "这条话题还在进行中，摘要会继续补齐。"
}

func topicKeywords(topic memory.TopicThread) []string {
	return topicSummary(topic).Keywords
}

func topicParticipantSummary(topic memory.TopicThread) string {
	parts := make([]string, 0, len(topicSummary(topic).Participants))
	for _, participant := range topicSummary(topic).Participants {
		if strings.TrimSpace(participant.Nickname) == "" || strings.TrimSpace(participant.Position) == "" {
			continue
		}
		parts = append(parts, participant.Nickname+"："+participant.Position)
	}
	return strings.Join(parts, "；")
}

func topicSummaryProgressText(topic memory.TopicThread) string {
	if topic.SummaryUntilMessageLogID >= topic.LastMessageLogID {
		return "摘要已覆盖最新消息"
	}
	return "还有新消息待补进摘要"
}

func topicSummaryProgressClass(topic memory.TopicThread) string {
	if topic.SummaryUntilMessageLogID >= topic.LastMessageLogID {
		return "inline-flex items-center rounded-full bg-emerald-100 px-3 py-1 text-xs font-semibold text-emerald-700 ring-1 ring-emerald-200"
	}
	return "inline-flex items-center rounded-full bg-amber-100 px-3 py-1 text-xs font-semibold text-amber-700 ring-1 ring-amber-200"
}

func topicMessageText(log memory.MessageLog) string {
	text := strings.TrimSpace(log.OriginalContent)
	if text != "" {
		return text
	}
	text = strings.TrimSpace(log.Content)
	if text != "" {
		return text
	}
	return "暂无内容"
}

func formatOptionalTime(ts *time.Time) string {
	if ts == nil || ts.IsZero() {
		return "-"
	}
	return ts.Format("2006-01-02 15:04")
}

func formatOptionalTimeAttr(ts *time.Time) string {
	if ts == nil || ts.IsZero() {
		return ""
	}
	return ts.Format(time.RFC3339)
}

func emptyDash(value string) string {
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

func faviconHref() string {
	return "/favicon.ico"
}

func FaviconSVG() string {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><defs><linearGradient id="g" x1="0%" y1="0%" x2="100%" y2="100%"><stop offset="0%" stop-color="#0f766e"/><stop offset="100%" stop-color="#0f172a"/></linearGradient></defs><rect width="64" height="64" rx="18" fill="url(#g)"/><path d="M18 46V18h8l6 10 6-10h8v28h-7V30l-5 8h-4l-5-8v16z" fill="white"/></svg>`
}

func metaSummary(meta ListMeta) string {
	if meta.Total == 0 {
		return "暂无数据"
	}
	start := (meta.Page-1)*meta.PageSize + 1
	end := meta.Page * meta.PageSize
	if int64(end) > meta.Total {
		end = int(meta.Total)
	}
	return fmt.Sprintf("第 %d 页，显示 %d-%d / %d", meta.Page, start, end, meta.Total)
}

func stickerPreviewText(description string) string {
	cleaned := stickerDescriptionText(description)
	preview := firstReadableRunes(cleaned, 2)
	if preview == "" {
		return "贴图"
	}
	return preview
}

func stickerFileURL(fileName string) string {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return ""
	}
	return "/admin/stickers/files/" + neturl.PathEscape(fileName)
}

func memoryTypeText(kind memory.MemoryType) string {
	switch kind {
	case memory.MemoryTypeGroupFact:
		return "群长期事实"
	case memory.MemoryTypeSelfExperience:
		return "自身经历"
	case memory.MemoryTypeConversation:
		return "重要对话"
	default:
		if strings.TrimSpace(string(kind)) == "" {
			return "未分类"
		}
		return "未分类"
	}
}

func memoryCanonicalTypeText(kind memory.CanonicalMemoryType) string {
	switch kind {
	case memory.CanonicalMemoryTypeFact:
		return "事实"
	case memory.CanonicalMemoryTypeEpisode:
		return "经历"
	case memory.CanonicalMemoryTypePreference:
		return "偏好"
	case memory.CanonicalMemoryTypeConstraint:
		return "约束"
	case memory.CanonicalMemoryTypeGoal:
		return "目标"
	default:
		return "未归类"
	}
}

func memoryStatusText(status memory.MemoryStatus) string {
	switch status {
	case memory.MemoryStatusCandidate:
		return "待收敛"
	case memory.MemoryStatusArchived:
		return "已归档"
	case memory.MemoryStatusLegacy:
		return "旧版记录"
	default:
		return "生效中"
	}
}

func memoryStatusClass(status memory.MemoryStatus) string {
	switch status {
	case memory.MemoryStatusCandidate:
		return "inline-flex items-center rounded-full bg-amber-100 px-3 py-1 text-xs font-semibold text-amber-700 ring-1 ring-amber-200"
	case memory.MemoryStatusArchived:
		return "inline-flex items-center rounded-full bg-slate-100 px-3 py-1 text-xs font-semibold text-slate-600 ring-1 ring-slate-200"
	case memory.MemoryStatusLegacy:
		return "inline-flex items-center rounded-full bg-violet-100 px-3 py-1 text-xs font-semibold text-violet-700 ring-1 ring-violet-200"
	default:
		return "inline-flex items-center rounded-full bg-emerald-100 px-3 py-1 text-xs font-semibold text-emerald-700 ring-1 ring-emerald-200"
	}
}

func memorySourceKindText(item memory.Memory) string {
	switch kind := item.SourceKind; kind {
	case memory.MemorySourceKindTopic:
		return "话题沉淀"
	case memory.MemorySourceKindMessage:
		return "主动记住"
	}
	sourceRef := strings.TrimSpace(item.SourceRef)
	switch {
	case strings.HasPrefix(sourceRef, "topic:"):
		return "话题沉淀"
	case strings.HasPrefix(sourceRef, "message:"):
		return "主动记住"
	case item.EffectiveStatus() == memory.MemoryStatusLegacy:
		return "旧版兼容"
	default:
		return "未标注"
	}
}

func avatarText(value string) string {
	if isPlaceholderText(value) {
		return "友"
	}
	preview := firstReadableRunes(value, 1)
	if preview == "" {
		return "友"
	}
	return preview
}

func memberDisplayName(value string) string {
	return displayText(value, "未填写昵称")
}

func memberPrimaryName(profile memory.MemberProfile) string {
	return profile.Nickname
}

func memberGroupCards(profile memory.MemberProfile, limit int) []string {
	records := profile.MemberNameRecords()
	if len(records) == 0 {
		return nil
	}
	items := make([]string, 0, len(records))
	for _, record := range records {
		if record.Source == memory.MemberNameSourceGroupCard {
			items = append(items, memberGroupCardLabel(record))
		}
	}
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func memberGroupCardLabel(record memory.MemberNameRecord) string {
	name := strings.TrimSpace(record.Content)
	if name == "" {
		return ""
	}
	if record.GroupID > 0 {
		return fmt.Sprintf("%s · 群 %d", name, record.GroupID)
	}
	return name
}

func memberLearnedAliases(profile memory.MemberProfile, limit int) []string {
	items := memory.MemberLearnedAliases(profile.MemberNameRecords())
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func memberTags(raw string, limit int) []string {
	items := normalizedListItems(raw)
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func memberTagOverflow(raw string, limit int) int {
	items := normalizedListItems(raw)
	if limit <= 0 || len(items) <= limit {
		return 0
	}
	return len(items) - limit
}

func rowActionClass(action RowAction) string {
	switch action.Kind {
	case "danger":
		return "inline-flex w-full items-center justify-center gap-2 rounded-2xl bg-rose-50 px-3.5 py-2.5 text-[13px] font-semibold text-rose-700 ring-1 ring-rose-200/80 transition duration-200 ease-out hover:bg-rose-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-rose-300 focus-visible:ring-offset-2 focus-visible:ring-offset-white disabled:cursor-wait disabled:opacity-70"
	case "ghost":
		return "inline-flex w-full items-center justify-center gap-2 rounded-2xl border border-slate-200/80 bg-white/82 px-3.5 py-2.5 text-[13px] font-semibold text-slate-700 shadow-[inset_0_1px_0_rgba(255,255,255,0.85)] transition duration-200 ease-out hover:bg-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 focus-visible:ring-offset-2 focus-visible:ring-offset-white disabled:cursor-wait disabled:opacity-70"
	default:
		return "inline-flex w-full items-center justify-center gap-2 rounded-2xl bg-teal-50 px-3.5 py-2.5 text-[13px] font-semibold text-teal-700 ring-1 ring-teal-200/80 transition duration-200 ease-out hover:bg-teal-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 focus-visible:ring-offset-2 focus-visible:ring-offset-white disabled:cursor-wait disabled:opacity-70"
	}
}

func styleCardActionDialogHref(id uint, status string) string {
	return adminActionDialogHref("style-card-status", id, map[string]string{"status": status})
}

func jargonActionDialogHref(id uint, status string) string {
	return adminActionDialogHref("jargon-status", id, map[string]string{"status": status})
}

func stickerDeleteDialogHref(id uint) string {
	return adminActionDialogHref("sticker-delete", id, nil)
}

func memoryDeleteDialogHref(id uint) string {
	return adminActionDialogHref("memory-delete", id, nil)
}

func memoryArchiveDialogHref(id uint) string {
	return adminActionDialogHref("memory-archive", id, nil)
}

func memoryRestoreDialogHref(id uint) string {
	return adminActionDialogHref("memory-restore", id, nil)
}

func adminActionDialogHref(kind string, id uint, extra map[string]string) string {
	values := neturl.Values{}
	values.Set("action_kind", kind)
	values.Set("action_id", strconv.FormatUint(uint64(id), 10))
	for key, value := range extra {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		values.Set(key, value)
	}
	return "/admin/dialogs/actions?" + values.Encode()
}

func stickerPreviewDialogHref(id uint) string {
	return "/admin/dialogs/stickers/" + strconv.FormatUint(uint64(id), 10)
}

func modalActionClass(action RowAction) string {
	switch action.Kind {
	case "danger":
		return "inline-flex items-center justify-center gap-2 rounded-2xl bg-rose-50 px-4 py-2.5 text-[13px] font-semibold text-rose-700 ring-1 ring-rose-200/80 transition duration-200 ease-out hover:bg-rose-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-rose-300 focus-visible:ring-offset-2 focus-visible:ring-offset-white disabled:cursor-wait disabled:opacity-70"
	case "ghost":
		return "inline-flex items-center justify-center gap-2 rounded-2xl border border-slate-200/80 bg-white/82 px-4 py-2.5 text-[13px] font-semibold text-slate-700 shadow-[inset_0_1px_0_rgba(255,255,255,0.85)] transition duration-200 ease-out hover:bg-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 focus-visible:ring-offset-2 focus-visible:ring-offset-white disabled:cursor-wait disabled:opacity-70"
	default:
		return "inline-flex items-center justify-center gap-2 rounded-2xl bg-[linear-gradient(135deg,#101a32_0%,#1e2c4f_58%,#0b7285_120%)] px-4 py-2.5 text-[13px] font-semibold text-white shadow-[0_18px_32px_rgba(15,23,42,0.16)] transition duration-200 ease-out hover:brightness-105 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 focus-visible:ring-offset-2 focus-visible:ring-offset-white disabled:cursor-wait disabled:opacity-70"
	}
}

func sortToolbarLinkClass(active bool) string {
	base := "inline-flex items-center justify-center rounded-full px-3 py-1.5 text-sm font-semibold transition duration-200 ease-out focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 focus-visible:ring-offset-2 focus-visible:ring-offset-white"
	if active {
		return joinClasses(base, "bg-[linear-gradient(135deg,#101a32_0%,#1e2c4f_58%,#0b7285_120%)] text-white shadow-[0_12px_24px_rgba(15,23,42,0.14)]")
	}
	return joinClasses(base, "border border-slate-200/80 bg-white/88 text-slate-600 hover:bg-white hover:text-slate-900")
}

func filterChoiceClass(active bool) string {
	base := "inline-flex cursor-pointer items-center justify-center rounded-full px-3 py-1.5 text-sm font-semibold transition duration-200 ease-out focus-within:outline-none focus-within:ring-2 focus-within:ring-cyan-300 focus-within:ring-offset-2 focus-within:ring-offset-white"
	if active {
		return joinClasses(base, "bg-[linear-gradient(135deg,#101a32_0%,#1e2c4f_58%,#0b7285_120%)] text-white shadow-[0_12px_24px_rgba(15,23,42,0.14)]")
	}
	return joinClasses(base, "border border-slate-200/80 bg-white/88 text-slate-600 hover:bg-white hover:text-slate-900")
}

func dialogChipClass(kind string) string {
	switch strings.TrimSpace(kind) {
	case "cyan":
		return "inline-flex items-center rounded-full bg-cyan-50 px-3 py-1 text-[11px] font-semibold text-cyan-700 ring-1 ring-cyan-200/80"
	case "teal":
		return "inline-flex items-center rounded-full bg-teal-50 px-3 py-1 text-[11px] font-semibold text-teal-700 ring-1 ring-teal-200/80"
	default:
		return "inline-flex items-center rounded-full bg-slate-100 px-3 py-1 text-[11px] font-semibold text-slate-600 ring-1 ring-slate-200/80"
	}
}

func ternaryString(condition bool, whenTrue string, whenFalse string) string {
	if condition {
		return whenTrue
	}
	return whenFalse
}

func equalTrimmed(left string, right string) bool {
	return strings.TrimSpace(left) == strings.TrimSpace(right)
}

func styleCardActions(status memory.StyleCardStatus) []RowAction {
	switch status {
	case memory.StyleCardStatusActive:
		return []RowAction{
			{Label: "设为拒绝", Value: string(memory.StyleCardStatusRejected), Kind: "danger", BusyLabel: "处理中", ConfirmText: "确认将这张风格卡片设为拒绝状态？"},
		}
	case memory.StyleCardStatusRejected:
		return []RowAction{
			{Label: "重新启用", Value: string(memory.StyleCardStatusActive), Kind: "approve", BusyLabel: "启用中", ConfirmText: "确认重新启用这张风格卡片？"},
		}
	default:
		return []RowAction{
			{Label: "通过", Value: string(memory.StyleCardStatusActive), Kind: "approve", BusyLabel: "通过中", ConfirmText: "确认通过这张候选风格卡片？"},
			{Label: "拒绝", Value: string(memory.StyleCardStatusRejected), Kind: "danger", BusyLabel: "拒绝中", ConfirmText: "确认拒绝这张候选风格卡片？"},
		}
	}
}

func jargonActions(status string) []RowAction {
	switch strings.TrimSpace(status) {
	case "approved":
		return []RowAction{
			{Label: "设为拒绝", Value: "rejected", Kind: "danger", BusyLabel: "处理中", ConfirmText: "确认将这条黑话改为拒绝状态？"},
		}
	case "rejected":
		return []RowAction{
			{Label: "重新通过", Value: "approved", Kind: "approve", BusyLabel: "处理中", ConfirmText: "确认重新通过这条黑话？"},
		}
	default:
		return []RowAction{
			{Label: "通过", Value: "approved", Kind: "approve", BusyLabel: "通过中", ConfirmText: "确认通过这条黑话？"},
			{Label: "拒绝", Value: "rejected", Kind: "danger", BusyLabel: "拒绝中", ConfirmText: "确认拒绝这条黑话？"},
		}
	}
}

func StyleCardActionDialogData(item memory.StyleCard, targetStatus string, returnTo string) (AdminActionDialogContentData, bool) {
	for _, action := range styleCardActions(item.Status) {
		if strings.TrimSpace(action.Value) != strings.TrimSpace(targetStatus) {
			continue
		}
		return AdminActionDialogContentData{
			Title:       action.Label,
			Body:        action.ConfirmText,
			SubmitLabel: action.Label,
			SubmitClass: modalActionClass(action),
			BusyLabel:   action.BusyLabel,
			Spotlight:   "“" + item.Example + "”",
			Chips: []AdminActionChip{
				{Label: "意图：" + item.Intent, Kind: "cyan"},
				{Label: "语气：" + item.Tone, Kind: "teal"},
			},
			Hidden: []AdminActionHiddenField{
				{Name: "action_kind", Value: "style-card-status"},
				{Name: "action_id", Value: strconv.FormatUint(uint64(item.ID), 10)},
				{Name: "status", Value: action.Value},
			},
			ReturnTo: returnTo,
		}, true
	}
	return AdminActionDialogContentData{}, false
}

func JargonActionDialogData(item memory.Jargon, targetStatus string, returnTo string) (AdminActionDialogContentData, bool) {
	for _, action := range jargonActions(jargonStatusValue(item)) {
		if strings.TrimSpace(action.Value) != strings.TrimSpace(targetStatus) {
			continue
		}
		return AdminActionDialogContentData{
			Title:       action.Label,
			Body:        action.ConfirmText,
			SubmitLabel: action.Label,
			SubmitClass: modalActionClass(action),
			BusyLabel:   action.BusyLabel,
			Fields: []AdminActionField{
				{Label: "术语", Value: item.Content},
				{Label: "释义", Value: item.Meaning},
			},
			Hidden: []AdminActionHiddenField{
				{Name: "action_kind", Value: "jargon-status"},
				{Name: "action_id", Value: strconv.FormatUint(uint64(item.ID), 10)},
				{Name: "status", Value: action.Value},
			},
			ReturnTo: returnTo,
		}, true
	}
	return AdminActionDialogContentData{}, false
}

func StickerDeleteDialogData(item memory.Sticker, returnTo string) AdminActionDialogContentData {
	action := RowAction{Kind: "danger", BusyLabel: "删除中"}
	return AdminActionDialogContentData{
		Title:       "删除表情包",
		Body:        "删除后会一并移除这张图片，请确认它已经不再需要。",
		SubmitLabel: "确认删除",
		SubmitClass: modalActionClass(action),
		BusyLabel:   action.BusyLabel,
		Fields: []AdminActionField{
			{Label: "待删除内容", Value: stickerDescriptionText(item.Description)},
		},
		Hidden: []AdminActionHiddenField{
			{Name: "action_kind", Value: "sticker-delete"},
			{Name: "action_id", Value: strconv.FormatUint(uint64(item.ID), 10)},
		},
		ReturnTo: returnTo,
	}
}

func MemoryDeleteDialogData(item memory.Memory, returnTo string) AdminActionDialogContentData {
	action := RowAction{Kind: "danger", BusyLabel: "删除中"}
	return AdminActionDialogContentData{
		Title:       "删除记忆",
		Body:        "删除后将无法再查看这条记忆。",
		SubmitLabel: "确认删除",
		SubmitClass: modalActionClass(action),
		BusyLabel:   action.BusyLabel,
		Fields: []AdminActionField{
			{Label: "记忆内容", Value: item.Content},
			{Label: "记忆类型", Value: memoryTypeText(item.Type)},
		},
		Hidden: []AdminActionHiddenField{
			{Name: "action_kind", Value: "memory-delete"},
			{Name: "action_id", Value: strconv.FormatUint(uint64(item.ID), 10)},
		},
		ReturnTo: returnTo,
	}
}

func MemoryArchiveDialogData(item memory.Memory, returnTo string) AdminActionDialogContentData {
	action := RowAction{Kind: "ghost", BusyLabel: "归档中"}
	return AdminActionDialogContentData{
		Title:       "归档记忆",
		Body:        "归档后默认不再参与召回，但仍保留在历史里，之后可以重新放回待收敛区。",
		SubmitLabel: "确认归档",
		SubmitClass: modalActionClass(action),
		BusyLabel:   action.BusyLabel,
		Fields: []AdminActionField{
			{Label: "记忆内容", Value: item.Content},
			{Label: "当前状态", Value: memoryStatusText(item.EffectiveStatus())},
		},
		Hidden: []AdminActionHiddenField{
			{Name: "action_kind", Value: "memory-archive"},
			{Name: "action_id", Value: strconv.FormatUint(uint64(item.ID), 10)},
		},
		ReturnTo: returnTo,
	}
}

func MemoryRestoreDialogData(item memory.Memory, returnTo string) AdminActionDialogContentData {
	action := RowAction{Kind: "approve", BusyLabel: "恢复中"}
	return AdminActionDialogContentData{
		Title:       "恢复到待收敛",
		Body:        "恢复后会重新进入待收敛区，后续仍需新的证据继续确认，不会直接恢复为生效中。",
		SubmitLabel: "确认恢复",
		SubmitClass: modalActionClass(action),
		BusyLabel:   action.BusyLabel,
		Fields: []AdminActionField{
			{Label: "记忆内容", Value: item.Content},
			{Label: "当前状态", Value: memoryStatusText(item.EffectiveStatus())},
		},
		Hidden: []AdminActionHiddenField{
			{Name: "action_kind", Value: "memory-restore"},
			{Name: "action_id", Value: strconv.FormatUint(uint64(item.ID), 10)},
		},
		ReturnTo: returnTo,
	}
}

func StickerPreviewDialogDataForItem(item memory.Sticker) StickerPreviewDialogData {
	createdAtText := formatTime(item.CreatedAt)
	meta := fmt.Sprintf("使用 %d 次", item.UseCount)
	if strings.TrimSpace(createdAtText) != "" {
		meta = fmt.Sprintf("使用 %d 次 · 创建于 %s", item.UseCount, createdAtText)
	}
	return StickerPreviewDialogData{
		FileURL:     stickerFileURL(item.FileName),
		Description: stickerDescriptionText(item.Description),
		FileName:    item.FileName,
		FileHash:    item.FileHash,
		Meta:        meta,
	}
}

func stickerDescriptionText(description string) string {
	cleaned := strings.TrimSpace(description)
	for _, marker := range []string{"<|begin_of_box|>", "<|end_of_box|>", "<|box_start|>", "<|box_end|>"} {
		cleaned = strings.ReplaceAll(cleaned, marker, " ")
	}
	cleaned = strings.Trim(cleaned, "[]【】()（）<>《》「」『』\"'`")
	prefixes := []string{
		"图片:", "图片：", "image:", "Image:",
		"这是一张", "这是一幅", "这是一只", "这是一个", "这是", "一张", "一个", "关于",
	}
	changed := true
	for changed {
		changed = false
		for _, prefix := range prefixes {
			trimmed := strings.TrimSpace(strings.TrimPrefix(cleaned, prefix))
			if trimmed != cleaned {
				cleaned = trimmed
				changed = true
			}
		}
	}
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if cleaned == "" {
		return "暂无描述"
	}
	return cleaned
}

func firstReadableRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	var builder []rune
	for _, r := range text {
		if unicode.IsSpace(r) || strings.ContainsRune("[]【】()（）<>《》「」『』:：;；,.，。!！?？'\"`~·-_/\\|", r) {
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			continue
		}
		builder = append(builder, unicode.ToUpper(r))
		if len(builder) == limit {
			break
		}
	}
	return string(builder)
}

func normalizedListItems(raw string) []string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}

	var items []string
	if strings.HasPrefix(text, "[") {
		var parsed []string
		if err := sonic.UnmarshalString(text, &parsed); err == nil {
			items = parsed
		}
	}
	if len(items) == 0 {
		items = strings.FieldsFunc(text, func(r rune) bool {
			switch r {
			case '\n', '\r', '\t', ',', '，', '、':
				return true
			default:
				return false
			}
		})
	}

	seen := make(map[string]struct{}, len(items))
	normalized := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(strings.Trim(item, "\""))
		if isPlaceholderText(item) {
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
