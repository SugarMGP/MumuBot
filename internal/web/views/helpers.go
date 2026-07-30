package views

import (
	"mumu-bot/internal/memory"
	"strings"
	"time"

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

func styleCardStatusText(status memory.StylePatternStatus) string {
	switch status {
	case memory.StylePatternStatusActive:
		return "已启用"
	case memory.StylePatternStatusRejected:
		return "已拒绝"
	default:
		return "候选"
	}
}

func styleCardStatusClass(status memory.StylePatternStatus) string {
	switch status {
	case memory.StylePatternStatusActive:
		return "inline-flex items-center rounded-full bg-emerald-100 px-3 py-1 text-xs font-semibold text-emerald-700 ring-1 ring-emerald-200"
	case memory.StylePatternStatusRejected:
		return "inline-flex items-center rounded-full bg-rose-100 px-3 py-1 text-xs font-semibold text-rose-700 ring-1 ring-rose-200"
	default:
		return "inline-flex items-center rounded-full bg-amber-100 px-3 py-1 text-xs font-semibold text-amber-700 ring-1 ring-amber-200"
	}
}

func jargonStatusText(item memory.Jargon) string {
	switch item.Status {
	case memory.CultureStatusRejected:
		return "已拒绝"
	case memory.CultureStatusActive:
		return "已通过"
	default:
		return "待审核"
	}
}

func jargonStatusValue(item memory.Jargon) string {
	return string(item.Status)
}

func jargonStatusClass(item memory.Jargon) string {
	switch jargonStatusValue(item) {
	case string(memory.CultureStatusActive):
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
