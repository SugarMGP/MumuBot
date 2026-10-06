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
