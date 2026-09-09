package views

import (
	"fmt"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelstats"
	"mumu-bot/internal/web/services"
	neturl "net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/bytedance/sonic"
)

func FaviconSVG() string {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="18" fill="#fff3f5"/><path d="M32 48c-5-8-15-10-15-20 0-5 4-9 9-9 3 0 5 1 6 4 1-3 3-4 6-4 5 0 9 4 9 9 0 10-10 12-15 20Z" fill="#e85d75"/><circle cx="32" cy="29" r="4" fill="#ffffff"/><path d="M32 15v6M21 20l4 4M43 20l-4 4" stroke="#159a8c" stroke-width="3" stroke-linecap="round"/></svg>`
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

func stickerCardTitle(item memory.Sticker) string {
	cleaned := stickerDescriptionText(item.Description)
	if cleaned == "暂无描述" {
		return fmt.Sprintf("表情包 #%d", item.ID)
	}
	return cleaned
}

func stickerPreviewAlt(description string, id uint) string {
	cleaned := stickerDescriptionText(description)
	runes := []rune(cleaned)
	if len(runes) > 20 {
		cleaned = string(runes[:20]) + "…"
	}
	if cleaned == "暂无描述" {
		return fmt.Sprintf("预览表情包 #%d", id)
	}
	return "预览表情包：" + cleaned
}

func stickerFileURL(fileName string) string {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return ""
	}
	return "/admin/stickers/files/" + neturl.PathEscape(fileName)
}

func memorySubjectText(subjectUserID, selfID int64) string {
	switch {
	case subjectUserID == 0:
		return "群组"
	case subjectUserID == selfID && selfID > 0:
		return "自身"
	default:
		return "成员"
	}
}

func memoryKindText(kind string) string {
	switch kind {
	case "fact":
		return "事实"
	case "episode":
		return "经历"
	case "preference":
		return "偏好"
	case "constraint":
		return "约束"
	case "goal":
		return "目标"
	case "term":
		return "群术语"
	case "expression":
		return "表达方式"
	case "alias":
		return "别名"
	default:
		return "未归类"
	}
}

func memoryStatusText(status string) string {
	switch status {
	case "archived":
		return "已归档"
	case "candidate":
		return "待确认"
	default:
		return "生效中"
	}
}

func memoryStatusClass(status string) string {
	switch status {
	case "archived":
		return "badge badge-ghost badge-sm"
	case "candidate":
		return "badge badge-warning badge-soft badge-sm"
	default:
		return "badge badge-success badge-soft badge-sm"
	}
}

func memberDisplayName(value string) string {
	return displayText(value, "未填写昵称")
}

func memberPrimaryName(profile services.MemberProfileView) string {
	return profile.Nickname
}

func memberGroupCards(profile services.MemberProfileView, limit int) []string {
	if len(profile.Names) == 0 {
		return nil
	}
	items := make([]string, 0, len(profile.Names))
	for _, record := range profile.Names {
		label := record.Value
		if record.GroupID > 0 {
			label = fmt.Sprintf("%s · 群 %d", label, record.GroupID)
		}
		items = append(items, label)
	}
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func rowActionClass(action RowAction) string {
	switch action.Kind {
	case "danger":
		return "btn btn-error btn-soft btn-sm"
	case "ghost":
		return "btn btn-ghost btn-sm border-base-300"
	default:
		return "btn btn-success btn-sm"
	}
}

func stickerDeleteDialogHref(id uint) string {
	return adminActionDialogHref("sticker-delete", id, nil)
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
		return "btn btn-error"
	case "ghost":
		return "btn btn-ghost border-base-300"
	default:
		return "btn btn-primary"
	}
}

func sortToolbarLinkClass(active bool) string {
	base := "btn btn-sm"
	if active {
		return joinClasses(base, "btn-primary")
	}
	return joinClasses(base, "btn-ghost border-base-300")
}

func filterChoiceClass(active bool) string {
	base := "btn btn-sm"
	if active {
		return joinClasses(base, "btn-primary")
	}
	return joinClasses(base, "btn-ghost border-transparent")
}

func dialogChipClass(kind string) string {
	switch strings.TrimSpace(kind) {
	case "cyan":
		return "badge badge-info badge-soft badge-sm"
	case "teal":
		return "badge badge-success badge-soft badge-sm"
	default:
		return "badge badge-ghost badge-sm"
	}
}

func ternaryString(condition bool, whenTrue string, whenFalse string) string {
	if condition {
		return whenTrue
	}
	return whenFalse
}

func systemViewLabel(view string) string {
	switch view {
	case "logs":
		return "运行日志"
	case "models":
		return "模型调用"
	default:
		return "运行信息"
	}
}

func systemLogFragmentURL(data SystemPageData) string {
	values := neturl.Values{"view": {"logs"}, "fragment": {"logs"}, "level": {data.LogLevel}}
	if data.LogKeyword != "" {
		values.Set("keyword", data.LogKeyword)
	}
	return "/admin/system?" + values.Encode()
}

func systemLogDownloadURL(data SystemPageData) string {
	values := neturl.Values{"level": {data.LogLevel}}
	if data.LogKeyword != "" {
		values.Set("keyword", data.LogKeyword)
	}
	return "/admin/system/logs/download?" + values.Encode()
}

func modelFailureRate(row modelstats.AggregateRow) string {
	if row.RequestCount == 0 {
		return "暂无数据"
	}
	return fmt.Sprintf("%.1f%%", float64(row.FailureCount)*100/float64(row.RequestCount))
}

func modelLatency(row modelstats.AggregateRow) string {
	if row.RequestCount == 0 {
		return "暂无数据"
	}
	return fmt.Sprintf("%.0f ms", row.AverageLatencyMS)
}

func modelUsageCoverage(row modelstats.AggregateRow) string {
	success := row.RequestCount - row.FailureCount
	if success <= 0 {
		return "暂无数据"
	}
	return fmt.Sprintf("%.1f%%", float64(row.UsageReportedCount)*100/float64(success))
}

func modelAverageTokens(row modelstats.AggregateRow) string {
	if row.UsageReportedCount == 0 {
		return "暂无数据"
	}
	return fmt.Sprintf("%.0f", float64(row.TotalTokens)/float64(row.UsageReportedCount))
}

func modelStatsJSON(snapshot modelstats.Snapshot) string {
	raw, _ := sonic.MarshalString(snapshot)
	return raw
}

func equalTrimmed(left string, right string) bool {
	return strings.TrimSpace(left) == strings.TrimSpace(right)
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
