package views

import (
	"fmt"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/services"
)

type KnowledgeDetailPageData struct {
	ReturnTo, CurrentURL string
	Detail               services.KnowledgeDetail
	SelfID               int64
	Flash                *FlashMessage
}

func knowledgeHref(id uint) string { return fmt.Sprintf("/admin/knowledge/%d", id) }
func knowledgeTitle(item memory.KnowledgeItem) string {
	if item.Label != "" {
		return item.Label
	}
	text := []rune(item.Content)
	if len(text) > 24 {
		return string(text[:24]) + "…"
	}
	if len(text) > 0 {
		return string(text)
	}
	return memoryKindText(item.Kind)
}
func relationText(kind string) string {
	switch kind {
	case "variant_of":
		return "变体来自"
	case "part_of":
		return "属于"
	case "supersedes":
		return "修正替代"
	case "contradicts":
		return "相互矛盾"
	}
	return "关联"
}

func knowledgeNodeStyle(status string) map[string]any {
	color := "#159a8c"
	if status == "archived" {
		color = "#b7b4bc"
	}
	return map[string]any{"color": color, "borderColor": color, "borderWidth": 2, "borderType": "solid", "shadowBlur": 14, "shadowColor": "rgba(21,154,140,.18)"}
}
