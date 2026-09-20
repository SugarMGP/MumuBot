package views

import (
	"fmt"
	"strings"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/services"

	"github.com/bytedance/sonic"
)

type GraphPanelData struct {
	Title, Content, KindLabel, Status, ActionKind       string
	NodeKind                                            string
	ID, Related                                         uint
	Evidence                                            []memory.KnowledgeEvidence
	DetailURL, FocusURL, ReturnTo, PreviousURL, NextURL string
}

func graphPanelURL(groupID int64, kind string, id uint, offset int) string {
	return fmt.Sprintf("/admin/knowledge/graph-panel?group_id=%d&kind=%s&id=%d&offset=%d", groupID, kind, id, offset)
}

func graphTopicTitle(topic services.GraphTopic) string {
	var summary memory.TopicSummary
	if sonic.UnmarshalString(topic.SummaryJSON, &summary) == nil && summary.Title != "" {
		return summary.Title
	}
	return fmt.Sprintf("话题 %d", topic.TopicID)
}

func groupKnowledgeGraphJSON(data KnowledgeGraphPageData) string {
	nodes := []map[string]any{}
	edges := []map[string]any{}
	seen := map[string]bool{}
	for _, item := range data.Graph.Items {
		id := fmt.Sprintf("k:%d", item.ID)
		seen[id] = true
		itemStyle := knowledgeNodeStyle(item.Status)
		size := 34

		nodes = append(nodes, map[string]any{"id": id, "name": knowledgeTitle(item), "status": memoryStatusText(item.Status), "value": item.Content, "url": graphPanelURL(item.GroupID, "knowledge", item.ID, 0), "symbol": "circle", "symbolSize": size, "itemStyle": itemStyle})
	}
	for _, topic := range data.Graph.Topics {
		id := fmt.Sprintf("t:%d", topic.TopicID)
		seen[id] = true
		status := "话题"
		if !topic.SourcesValid {
			status = "话题 · 依据已失效"
		}
		itemStyle := map[string]any{"color": "#159a8c"}
		if !topic.SourcesValid {
			itemStyle["color"] = "#9ca3af"
		}
		size := 34

		nodes = append(nodes, map[string]any{"id": id, "name": graphTopicTitle(topic), "status": status, "url": graphPanelURL(data.Workspace.Filter.GroupID, "topic", topic.TopicID, 0), "symbol": "diamond", "symbolSize": size, "itemStyle": itemStyle})
	}
	for _, rel := range data.Graph.Relations {
		edges = append(edges, map[string]any{"source": fmt.Sprintf("k:%d", rel.SourceItemID), "target": fmt.Sprintf("k:%d", rel.TargetItemID), "name": relationText(rel.Kind), "status": memoryStatusText(rel.Status), "symbol": []string{"none", "arrow"}, "url": graphPanelURL(data.Workspace.Filter.GroupID, "relation", rel.ID, 0), "lineStyle": map[string]any{"color": "#ef8ca8", "type": ternaryString(rel.Status == "archived", "dashed", "solid")}})
	}
	for _, topic := range data.Graph.Topics {
		if !topic.SourcesValid {
			continue
		}
		var summary memory.TopicSummary
		if sonic.UnmarshalString(topic.SummaryJSON, &summary) != nil {
			continue
		}
		for _, related := range summary.RelatedTopics {
			target := fmt.Sprintf("t:%d", related.TopicID)
			if !seen[target] {
				continue
			}
			edges = append(edges, map[string]any{"source": fmt.Sprintf("t:%d", topic.TopicID), "target": target, "name": "话题关联", "value": related.Reason, "url": graphPanelURL(data.Workspace.Filter.GroupID, "topic", topic.TopicID, 0) + fmt.Sprintf("&related=%d", related.TopicID), "lineStyle": map[string]any{"color": "#159a8c"}})
		}
	}
	for _, source := range data.Graph.Sources {
		edges = append(edges, map[string]any{"source": fmt.Sprintf("k:%d", source.ItemID), "target": fmt.Sprintf("t:%d", source.TopicID), "name": "原文归属", "value": "依据原文参与了该话题，不代表两个知识结论相互证明", "url": graphPanelURL(data.Workspace.Filter.GroupID, "knowledge", source.ItemID, 0) + fmt.Sprintf("&related=%d", source.TopicID), "lineStyle": map[string]any{"color": "#adb5bd", "type": "dashed"}})
	}
	raw, _ := sonic.MarshalString(map[string]any{"nodes": nodes, "edges": edges})
	return raw
}

func GraphPanel(selection services.GraphSelection, groupID, selfID int64, kind string, id, related uint, offset int) GraphPanelData {
	d := GraphPanelData{NodeKind: kind, ID: id, Related: related, Evidence: selection.Evidence, ReturnTo: fmt.Sprintf("/admin/knowledge?group_id=%d&selected_kind=%s&selected_id=%d", groupID, kind, id)}
	if selection.Item != nil {
		item := selection.Item
		d.Title = knowledgeTitle(*item)
		d.Content = item.Content
		d.Status = item.Status
		d.KindLabel = memoryKindText(item.Kind) + " · " + memorySubjectText(item.SubjectUserID, selfID)
		d.ActionKind = "knowledge-status"
		d.DetailURL = knowledgeHref(id)
		if related > 0 {
			d.KindLabel = "原文归属"
			d.Content = "这些完整依据组包含所选话题的原文。这条连线不表示知识结论相互证明。\n\n" + item.Content
		}
	}
	if selection.Relation != nil {
		rel := selection.Relation
		d.Title = relationText(rel.Kind)
		d.KindLabel = "知识关系"
		d.Content = selection.Description
		d.Status = rel.Status
		d.ActionKind = "relation-status"
	}
	if selection.Topic != nil {
		topic := selection.Topic
		d.Title = graphTopicTitle(*topic)
		d.KindLabel = "话题"
		d.DetailURL = fmt.Sprintf("/admin/topics/%d", id)
		var summary memory.TopicSummary
		if topic.SourcesValid && sonic.UnmarshalString(topic.SummaryJSON, &summary) == nil {
			d.Content = summary.Gist
			if len(summary.OpenLoops) > 0 {
				d.Content += "\n\n未完事项：" + strings.Join(summary.OpenLoops, "；")
			}
		} else {
			d.Content = "原文依据已失效，等待后续讨论重新整理。"
		}
		if related > 0 {
			d.KindLabel = "话题关联"
			d.Content = selection.Description
		}
	}
	if kind == "knowledge" || kind == "topic" {
		d.FocusURL = fmt.Sprintf("/admin/knowledge?group_id=%d&focus_kind=%s&focus_id=%d&status=all", groupID, kind, id)
	}
	base := graphPanelURL(groupID, kind, id, 0) + fmt.Sprintf("&related=%d", related)
	if offset > 0 {
		d.PreviousURL = strings.Replace(base, "offset=0", fmt.Sprintf("offset=%d", max(0, offset-5)), 1)
	}
	if selection.HasMore {
		d.NextURL = strings.Replace(base, "offset=0", fmt.Sprintf("offset=%d", offset+5), 1)
	}
	return d
}
