package views

import (
	"fmt"
	"math"
	"strings"
	"time"

	"mumu-bot/internal/version"

	"github.com/bytedance/sonic"
)

func NavItems() []NavItem {
	return []NavItem{
		{Label: "首页", Href: "/admin"},
		{Label: "统一记忆", Href: "/admin/knowledge"},
		{Label: "话题管理", Href: "/admin/topics"},
		{Label: "成员管理", Href: "/admin/members"},
		{Label: "会话管理", Href: "/admin/contacts"},
		{Label: "表情包", Href: "/admin/stickers"},
		{Label: "系统状态", Href: "/admin/system"},
	}
}

func BuildVersion() string { return version.String() }

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
	base := "group relative flex h-11 w-full items-center gap-3 rounded-xl px-3 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/35"
	active := currentPath == href
	if !active && href != "" && href != "/admin" && strings.HasPrefix(currentPath, href+"/") {
		active = true
	}
	if active {
		return joinClasses(base, "bg-primary/12 text-primary before:absolute before:-left-3 before:h-7 before:w-1 before:rounded-r-full before:bg-primary")
	}
	return joinClasses(base, "text-base-content/60 hover:bg-primary/10 hover:text-primary")
}

func moodPercent(kind string, raw float64) int {
	if kind == "valence" {
		raw = (raw + 1) / 2
	}
	clamped := math.Min(1, math.Max(0, raw))
	return int(math.Round(clamped * 100))
}

func ConnectionText(v bool) string {
	if v {
		return "已连接"
	}
	return "未连接"
}

func intimacyDeltaText(delta float64) string {
	if delta > 0 {
		return fmt.Sprintf("+%.2f", delta)
	}
	return fmt.Sprintf("%.2f", delta)
}

func intimacyDeltaClass(delta float64) string {
	if delta > 0 {
		return "text-secondary"
	}
	if delta < 0 {
		return "text-error"
	}
	return "text-base-content/60"
}

func intimacyReasonText(reason string) string {
	if text := strings.TrimSpace(reason); text != "" {
		return text
	}
	return "未记录原因"
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
	case "/admin/stickers":
		return "stickers"
	case "/admin/topics":
		return "topics"
	case "/admin/knowledge":
		return "memories"
	case "/admin/members":
		return "members"
	case "/admin/contacts":
		return "sessions"
	case "/admin/system":
		return "system"
	default:
		return "overview"
	}
}

func systemSectionsLeft(sections []SystemSection) []SystemSection {
	mid := (len(sections) + 1) / 2
	if mid >= len(sections) {
		return sections
	}
	return sections[:mid]
}

func systemSectionsRight(sections []SystemSection) []SystemSection {
	mid := (len(sections) + 1) / 2
	if mid >= len(sections) {
		return nil
	}
	return sections[mid:]
}

func systemSectionIconName(title string) string {
	switch strings.TrimSpace(title) {
	case "人格设定":
		return "persona"
	case "群聊与学习":
		return "group-config"
	case "模型能力":
		return "model"
	case "连接与数据":
		return "connection"
	default:
		return "system"
	}
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
