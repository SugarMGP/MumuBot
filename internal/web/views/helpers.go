package views

import (
	"math"
	"strings"
	"time"

	"github.com/bytedance/sonic"
)

func NavItems() []NavItem {
	return []NavItem{
		{Label: "首页", Href: "/admin"},
		{Label: "统一记忆", Href: "/admin/knowledge"},
		{Label: "话题管理", Href: "/admin/topics"},
		{Label: "成员管理", Href: "/admin/members"},
		{Label: "表情包", Href: "/admin/stickers"},
		{Label: "系统状态", Href: "/admin/system"},
	}
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
	base := "group relative flex h-11 w-full items-center gap-3 rounded-xl px-3 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/35"
	active := currentPath == href
	if !active && href != "" && href != "/admin" && strings.HasPrefix(currentPath, href+"/") {
		active = true
	}
	if active {
		return joinClasses(base, "bg-primary/12 text-primary before:absolute before:-left-3 before:h-7 before:w-1 before:rounded-r-full before:bg-primary")
	}
	return joinClasses(base, "text-base-content/68 hover:bg-primary/10 hover:text-primary")
}

func boolText(v bool) string {
	if v {
		return "已启用"
	}
	return "未启用"
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
		return "topics"
	case "/admin/knowledge":
		return "memories"
	case "/admin/members":
		return "members"
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
	base := "rounded-xl bg-base-200/50 p-4"
	if systemFieldNeedsWide(field) {
		return joinClasses(base, "sm:col-span-2")
	}
	return base
}

func systemFieldValueClass(field SystemField) string {
	base := "mt-2 break-words whitespace-pre-line text-sm leading-6 text-base-content/75"
	if systemFieldNeedsWide(field) {
		return joinClasses(base, "font-normal")
	}
	return joinClasses(base, "font-medium")
}

func systemFieldNeedsWide(field SystemField) bool {
	switch strings.TrimSpace(field.Label) {
	case "已启用群聊", "群聊整理", "整理间隔":
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
