package views

import (
	"fmt"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/services"
	neturl "net/url"
	"strconv"
	"strings"
	"unicode"
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

func stickerFileURL(fileName string) string {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return ""
	}
	return "/admin/stickers/files/" + neturl.PathEscape(fileName)
}

func memoryScopeText(kind memory.MemoryScope) string {
	switch kind {
	case memory.MemoryScopeGroup:
		return "群组"
	case memory.MemoryScopeSelf:
		return "自身"
	case memory.MemoryScopeMember:
		return "成员"
	default:
		return "未分类"
	}
}

func memoryKindText(kind memory.MemoryKind) string {
	switch kind {
	case memory.MemoryKindFact:
		return "事实"
	case memory.MemoryKindEpisode:
		return "经历"
	case memory.MemoryKindPreference:
		return "偏好"
	case memory.MemoryKindConstraint:
		return "约束"
	case memory.MemoryKindGoal:
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
	default:
		return "生效中"
	}
}

func memoryStatusClass(status memory.MemoryStatus) string {
	switch status {
	case memory.MemoryStatusCandidate:
		return "badge badge-warning badge-soft badge-sm"
	case memory.MemoryStatusArchived:
		return "badge badge-ghost badge-sm"
	default:
		return "badge badge-success badge-soft badge-sm"
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

func memberTraits(profile services.MemberProfileView, kind string, limit int) []string {
	items := make([]string, 0, len(profile.Traits))
	for _, trait := range profile.Traits {
		if trait.Kind == kind {
			items = append(items, trait.Value)
		}
	}
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func memberLearnedAliases(profile services.MemberProfileView, limit int) []string {
	return memberTraits(profile, "alias", limit)
}

func memberTraitText(profile services.MemberProfileView, kind string) string {
	return strings.Join(memberTraits(profile, kind, 0), "；")
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

func equalTrimmed(left string, right string) bool {
	return strings.TrimSpace(left) == strings.TrimSpace(right)
}

func styleCardActions(status memory.StylePatternStatus) []RowAction {
	switch status {
	case memory.StylePatternStatusActive:
		return []RowAction{
			{Label: "设为拒绝", Value: string(memory.StylePatternStatusRejected), Kind: "danger", BusyLabel: "处理中", ConfirmText: "确认将这张风格卡片设为拒绝状态？"},
		}
	case memory.StylePatternStatusRejected:
		return []RowAction{
			{Label: "重新启用", Value: string(memory.StylePatternStatusActive), Kind: "approve", BusyLabel: "启用中", ConfirmText: "确认重新启用这张风格卡片？"},
		}
	default:
		return []RowAction{
			{Label: "通过", Value: string(memory.StylePatternStatusActive), Kind: "approve", BusyLabel: "通过中", ConfirmText: "确认通过这张候选风格卡片？"},
			{Label: "拒绝", Value: string(memory.StylePatternStatusRejected), Kind: "danger", BusyLabel: "拒绝中", ConfirmText: "确认拒绝这张候选风格卡片？"},
		}
	}
}

func jargonActions(status string) []RowAction {
	switch strings.TrimSpace(status) {
	case string(memory.CultureStatusActive):
		return []RowAction{
			{Label: "设为拒绝", Value: string(memory.CultureStatusRejected), Kind: "danger", BusyLabel: "处理中", ConfirmText: "确认将这条黑话改为拒绝状态？"},
		}
	case "rejected":
		return []RowAction{
			{Label: "重新通过", Value: string(memory.CultureStatusActive), Kind: "approve", BusyLabel: "处理中", ConfirmText: "确认重新通过这条黑话？"},
		}
	default:
		return []RowAction{
			{Label: "通过", Value: string(memory.CultureStatusActive), Kind: "approve", BusyLabel: "通过中", ConfirmText: "确认通过这条黑话？"},
			{Label: "拒绝", Value: string(memory.CultureStatusRejected), Kind: "danger", BusyLabel: "拒绝中", ConfirmText: "确认拒绝这条黑话？"},
		}
	}
}

func StyleCardActionDialogData(item memory.StylePattern, targetStatus string, returnTo string) (AdminActionDialogContentData, bool) {
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
			Spotlight:   "“" + item.Expression + "”",
			Chips: []AdminActionChip{
				{Label: "场景：" + item.Situation, Kind: "cyan"},
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
				{Label: "术语", Value: item.Term},
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
			{Label: "范围", Value: memoryScopeText(item.Scope)},
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
			{Label: "当前状态", Value: memoryStatusText(item.Status)},
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
			{Label: "当前状态", Value: memoryStatusText(item.Status)},
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
