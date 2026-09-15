package views

import (
	"fmt"
	"net/url"
	"strconv"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/services"
)

type KnowledgeWorkspaceData struct {
	Filter     services.KnowledgeFilter
	Groups     []int64
	Note       *memory.GroupAgentState
	CurrentURL string
	View       string
}

// WithQuery 在已有页面地址上更新查询参数，空值移除参数
func WithQuery(raw string, pairs ...string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			q.Del(pairs[i])
		} else {
			q.Set(pairs[i], pairs[i+1])
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func workspaceViewURL(data KnowledgeWorkspaceData, view string) string {
	kind := data.Filter.Kind
	if view == "list" && kind == "topic" {
		kind = ""
	}
	return WithQuery(data.CurrentURL, "view", view, "kind", kind, "focus_kind", "", "focus_id", "", "selected_kind", "", "selected_id", "", "selected_related", "", "flash_kind", "", "flash_title", "", "flash_body", "")
}

func knowledgeDetailURL(id uint, returnTo string) string {
	return WithQuery(knowledgeHref(id), "return_to", returnTo)
}

func knowledgeSubject(item memory.KnowledgeItem, selfID int64, meta services.KnowledgeMetadata) string {
	if item.SubjectUserID == 0 || item.SubjectUserID == selfID {
		return memorySubjectText(item.SubjectUserID, selfID)
	}
	if meta.Name != "" {
		return meta.Name
	}
	return "QQ " + strconv.FormatInt(item.SubjectUserID, 10)
}

func evidenceSummary(meta services.KnowledgeMetadata) string {
	if meta.Valid > 0 {
		return fmt.Sprintf("有可用依据 · %d 组", meta.Valid)
	}
	if meta.Total > 0 {
		return "依据已失效"
	}
	return "暂无依据"
}

func firstEvidenceOpen(sets []memory.KnowledgeEvidence, index int) bool {
	for i, set := range sets {
		if set.Valid {
			return index == i
		}
	}
	return index == 0
}
