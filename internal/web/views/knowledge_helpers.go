package views

import (
	"fmt"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/services"

	"github.com/bytedance/sonic"
)

type KnowledgeDetailPageData struct {
	Detail services.KnowledgeDetail
	SelfID int64
	Flash  *FlashMessage
}

func knowledgeHref(id uint) string { return fmt.Sprintf("/admin/knowledge/%d", id) }
func knowledgeTitle(item memory.KnowledgeItem) string {
	if item.Label != "" {
		return item.Label
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
func knowledgeGraphJSON(graph memory.KnowledgeGraph) string {
	nodes := []map[string]any{}
	edges := []map[string]any{}
	seen := map[string]bool{}
	for _, item := range graph.Items {
		nodes = append(nodes, map[string]any{"id": fmt.Sprint(item.ID), "name": knowledgeTitle(item), "value": item.Content, "status": memoryStatusText(item.Status), "url": knowledgeHref(item.ID)})
		key := fmt.Sprintf("group-%d", item.GroupID)
		name := fmt.Sprintf("群 %d", item.GroupID)
		url := fmt.Sprintf("/admin/knowledge?group_id=%d", item.GroupID)
		label := "本群知识"
		if item.SubjectUserID > 0 {
			key = fmt.Sprintf("member-%d", item.SubjectUserID)
			name = fmt.Sprintf("成员 %d", item.SubjectUserID)
			url += fmt.Sprintf("&user_id=%d", item.SubjectUserID)
			label = "关于"
		}
		if !seen[key] {
			nodes = append(nodes, map[string]any{"id": key, "name": name, "status": "主体", "url": url, "itemStyle": map[string]any{"color": "#159a8c"}})
			seen[key] = true
		}
		edges = append(edges, map[string]any{"source": key, "target": fmt.Sprint(item.ID), "label": map[string]any{"show": true, "formatter": label}, "lineStyle": map[string]any{"type": "dashed"}})
	}
	for _, rel := range graph.Relations {
		edges = append(edges, map[string]any{"source": fmt.Sprint(rel.SourceItemID), "target": fmt.Sprint(rel.TargetItemID), "label": map[string]any{"show": true, "formatter": relationText(rel.Kind)}})
	}
	raw, _ := sonic.MarshalString(map[string]any{"nodes": nodes, "edges": edges})
	return raw
}
