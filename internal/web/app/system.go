package app

import (
	"context"
	"errors"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/views"
	neturl "net/url"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
)

func (a *App) systemSections() []views.SystemSection {
	cfg := a.cfg
	snapshot := a.runtimeSnapshot()

	groupIDs := make([]string, 0, len(cfg.Groups))
	for _, group := range cfg.Groups {
		if group.Enabled {
			groupIDs = append(groupIDs, fmt.Sprintf("%d", group.GroupID))
		}
	}
	groupSummary := "暂未启用群聊"
	if len(groupIDs) > 0 {
		groupSummary = strings.Join(groupIDs, "、")
	}

	appendField := func(fields []views.SystemField, label string, value string) []views.SystemField {
		value = strings.TrimSpace(value)
		if value == "" || value == "-" {
			return fields
		}
		return append(fields, views.SystemField{Label: label, Value: value})
	}

	personaFields := make([]views.SystemField, 0, 5)
	personaFields = appendField(personaFields, "名称", views.EmptyDash(cfg.Persona.Name))
	personaFields = appendField(personaFields, "别名", joinOrDash(cfg.Persona.AliasNames))

	groupFields := []views.SystemField{
		{Label: "启用群数", Value: fmt.Sprintf("%d / %d", countEnabledGroups(cfg.Groups), len(cfg.Groups))},
		{Label: "已启用群聊", Value: groupSummary},
		{Label: "观察窗口", Value: fmt.Sprintf("%d 秒", cfg.Agent.ObserveWindow)},
		{Label: "思考间隔", Value: fmt.Sprintf("%d 秒", cfg.Agent.ThinkInterval)},
	}
	if cfg.Agent.ThinkDebounceMS > 0 {
		groupFields = append(groupFields, views.SystemField{Label: "聚合窗口", Value: fmt.Sprintf("%d 毫秒", cfg.Agent.ThinkDebounceMS)})
	}
	if cfg.Learning.Enabled {
		groupFields = append(groupFields, views.SystemField{Label: "自动学习", Value: fmt.Sprintf("每 %d 分钟整理 %d 条消息", cfg.Learning.IntervalMinutes, cfg.Learning.BatchSize)})
		if cfg.Learning.ReviewIntervalMinutes > 0 {
			groupFields = append(groupFields, views.SystemField{Label: "审核节奏", Value: fmt.Sprintf("每 %d 分钟整理一次待审内容", cfg.Learning.ReviewIntervalMinutes)})
		}
	}

	modelFields := make([]views.SystemField, 0, 8)
	modelFields = appendField(modelFields, llm.TierDisplayName(llm.TierHigh), cfg.ModelTiers.High.Model)
	modelFields = appendField(modelFields, llm.TierDisplayName(llm.TierLow), cfg.ModelTiers.Low.Model)
	modelFields = appendField(modelFields, "记忆检索", cfg.Embedding.Model)
	if cfg.VisionLLM.Enabled {
		modelFields = appendField(modelFields, "图片理解", cfg.VisionLLM.Model)
	}
	if snapshot.MCPToolCount > 0 {
		modelFields = append(modelFields, views.SystemField{Label: "扩展工具", Value: fmt.Sprintf("%d 个", snapshot.MCPToolCount)})
	}

	runtimeFields := []views.SystemField{
		{Label: "当前连接", Value: views.ConnectionText(snapshot.Connected)},
		{Label: "重连间隔", Value: fmt.Sprintf("%d 秒", cfg.OneBot.ReconnectInterval)},
	}
	if currentVersion, err := a.memMgr.SchemaVersion(context.Background()); err == nil {
		runtimeFields = append(runtimeFields, views.SystemField{Label: "数据结构版本", Value: fmt.Sprintf("v%d / v%d", currentVersion, memory.LatestSchemaVersion())})
	}
	if snapshot.SelfID > 0 {
		runtimeFields = append(runtimeFields, views.SystemField{Label: "机器人 QQ", Value: fmt.Sprintf("%d", snapshot.SelfID)})
	}
	if cfg.Sticker.MaxSizeMB > 0 {
		runtimeFields = append(runtimeFields, views.SystemField{Label: "表情包大小上限", Value: fmt.Sprintf("%d MB", cfg.Sticker.MaxSizeMB)})
	}
	if cfg.Sticker.AutoSave {
		runtimeFields = append(runtimeFields, views.SystemField{Label: "表情包收集", Value: "自动保存"})
	}

	sections := make([]views.SystemSection, 0, 4)
	if len(personaFields) > 0 {
		sections = append(sections, views.SystemSection{Title: "人格设定", Fields: personaFields})
	}
	if len(groupFields) > 0 {
		sections = append(sections, views.SystemSection{Title: "群聊与学习", Fields: groupFields})
	}
	if len(modelFields) > 0 {
		sections = append(sections, views.SystemSection{Title: "模型能力", Fields: modelFields})
	}
	if len(runtimeFields) > 0 {
		sections = append(sections, views.SystemSection{Title: "连接与数据", Fields: runtimeFields})
	}
	return sections
}

func isSafeAdminTarget(target *neturl.URL) bool {
	if target == nil {
		return false
	}
	if target.IsAbs() || strings.TrimSpace(target.Host) != "" {
		return false
	}
	cleanPath := strings.TrimSpace(target.Path)
	if cleanPath == "" {
		return false
	}
	return strings.HasPrefix(cleanPath, "/admin")
}

func normalizeAdminTarget(raw string, requestHost string) (*neturl.URL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}

	target, err := neturl.Parse(raw)
	if err != nil || target.Path == "" {
		return nil, false
	}

	if target.IsAbs() || strings.TrimSpace(target.Host) != "" {
		if !strings.EqualFold(strings.TrimSpace(target.Host), strings.TrimSpace(requestHost)) {
			return nil, false
		}
		if target.Scheme != "" && target.Scheme != "http" && target.Scheme != "https" {
			return nil, false
		}
	}

	normalized := &neturl.URL{Path: target.Path, RawQuery: target.RawQuery}
	if !isSafeAdminTarget(normalized) {
		return nil, false
	}
	return normalized, true
}

func withPage(current *neturl.URL, page int) string {
	cloned := *current
	query := cloned.Query()
	query.Set("page", strconv.Itoa(page))
	cloned.RawQuery = query.Encode()
	if cloned.RawPath != "" {
		return cloned.RawPath + "?" + cloned.RawQuery
	}
	if cloned.RawQuery == "" {
		return cloned.Path
	}
	return cloned.Path + "?" + cloned.RawQuery
}

func withFlash(target string, kind string, title string, body string) string {
	parsed, err := neturl.Parse(target)
	if err != nil {
		return target
	}
	query := parsed.Query()
	query.Set("flash_kind", kind)
	query.Set("flash_title", title)
	if strings.TrimSpace(body) != "" {
		query.Set("flash_body", body)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func actionTriggerHeader(flash *views.FlashMessage, closeDialog bool) (string, error) {
	if flash == nil && !closeDialog {
		return "", nil
	}

	payload := make(map[string]any)
	if flash != nil {
		payload["admin:toast"] = map[string]string{
			"kind":  strings.TrimSpace(flash.Kind),
			"title": strings.TrimSpace(flash.Title),
			"body":  strings.TrimSpace(flash.Body),
		}
	}
	if closeDialog {
		payload["admin:action-dialog-close"] = true
	}

	encoded, err := sonic.MarshalString(payload)
	if err != nil {
		return "", err
	}
	return asciiHeaderJSON(encoded), nil
}

func asciiHeaderJSON(raw string) string {
	var builder strings.Builder
	builder.Grow(len(raw))

	for _, r := range raw {
		if r <= 127 {
			builder.WriteRune(r)
			continue
		}
		if r <= 0xFFFF {
			fmt.Fprintf(&builder, "\\u%04x", r)
			continue
		}
		for _, unit := range utf16.Encode([]rune{r}) {
			fmt.Fprintf(&builder, "\\u%04x", unit)
		}
	}

	return builder.String()
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func listPageSize(raw string) int {
	return listPageSizeWithDefault(raw, defaultListPageSize)
}

func listPageSizeWithDefault(raw string, fallback int) int {
	return parsePositiveInt(raw, fallback)
}

func parseInt64Query(raw string) int64 {
	value, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return value
}

func parseUintParam(raw string) (uint, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	return uint(value), err
}

func countEnabledGroups(groups []config.GroupConfig) int {
	total := 0
	for _, group := range groups {
		if group.Enabled {
			total++
		}
	}
	return total
}

func joinOrDash(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, "、")
}

func styleCardActionErrorText(err error) string {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "这张风格卡片不存在或已经被处理。"
	}
	if strings.Contains(strings.ToLower(err.Error()), "invalid") {
		return "这次状态变更无效，请刷新列表后重试。"
	}
	return "更新失败，请稍后再试。"
}

func jargonActionErrorText(err error) string {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "这条黑话不存在或已经被处理。"
	}
	if strings.Contains(strings.ToLower(err.Error()), "invalid") {
		return "这次状态变更无效，请刷新列表后重试。"
	}
	return "更新失败，请稍后再试。"
}

func deleteActionErrorText(err error) string {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "这条记录不存在或已经被删除。"
	}
	return "删除失败，请稍后再试。"
}

func buildSortToolbar(current *neturl.URL, currentSort string, currentOrder string, options []sortOption) views.SortToolbarData {
	data := views.SortToolbarData{
		CurrentSort:  currentSort,
		CurrentOrder: currentOrder,
		Options:      make([]views.SortToolbarLink, 0, len(options)),
		OrderOptions: make([]views.SortToolbarLink, 0, 2),
	}

	activeSortLabel := ""
	for _, option := range options {
		active := option.Key == currentSort
		if active {
			activeSortLabel = option.Label
		}
		data.Options = append(data.Options, views.SortToolbarLink{
			Label:  option.Label,
			Href:   withQueryValues(current, true, map[string]string{"sort": option.Key}),
			Active: active,
		})
	}
	if activeSortLabel == "" && len(options) > 0 {
		activeSortLabel = options[0].Label
	}

	orderLabel := "倒序"
	for _, option := range []struct {
		value string
		label string
	}{
		{value: "desc", label: "倒序"},
		{value: "asc", label: "正序"},
	} {
		active := option.value == currentOrder
		if active {
			orderLabel = option.label
		}
		data.OrderOptions = append(data.OrderOptions, views.SortToolbarLink{
			Label:  option.label,
			Href:   withQueryValues(current, true, map[string]string{"order": option.value}),
			Active: active,
		})
	}

	data.Summary = fmt.Sprintf("当前按%s%s查看", activeSortLabel, orderLabel)
	return data
}

func withQueryValues(current *neturl.URL, resetPage bool, updates map[string]string) string {
	cloned := *current
	query := cloned.Query()
	if resetPage {
		query.Set("page", "1")
	}
	for key, value := range updates {
		if strings.TrimSpace(value) == "" {
			query.Del(key)
			continue
		}
		query.Set(key, value)
	}
	cloned.RawQuery = query.Encode()
	if cloned.RawPath != "" {
		if cloned.RawQuery == "" {
			return cloned.RawPath
		}
		return cloned.RawPath + "?" + cloned.RawQuery
	}
	if cloned.RawQuery == "" {
		return cloned.Path
	}
	return cloned.Path + "?" + cloned.RawQuery
}
