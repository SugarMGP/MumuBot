package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
)

// RenderCheckpointDigest 按固定顺序渲染检查点中的概览、活跃话题和内部参考基线
func (m *Manager) RenderCheckpointDigest(ctx context.Context, kind string, targetID int64) (string, error) {
	checkpoint, err := m.GetCheckpoint(ctx, kind, targetID)
	if err != nil {
		return "", err
	}
	if checkpoint == nil {
		return m.renderLegacyTopicDigest(ctx, kind, targetID)
	}
	var snapshots []TopicSnapshot
	if err := sonic.Unmarshal(checkpoint.ActiveTopicVersions, &snapshots); err != nil {
		return "", err
	}
	summaries := make([]TopicSummary, 0, len(snapshots))
	topicIDs := make([]uint, 0, len(snapshots))
	for _, snapshot := range snapshots {
		var row TopicSummaryRow
		if err := m.db.WithContext(ctx).Where("id=? AND topic_id=?", snapshot.SummaryID, snapshot.TopicID).Take(&row).Error; err != nil {
			return "", fmt.Errorf("读取活跃话题摘要有误: %w", err)
		}
		var summary TopicSummary
		if err := sonic.UnmarshalString(row.SummaryJSON, &summary); err != nil {
			return "", fmt.Errorf("解析活跃话题摘要有误: %w", err)
		}
		summaries = append(summaries, summary)
		topicIDs = append(topicIDs, snapshot.TopicID)
	}
	return renderContextDigest(checkpoint.Overview, summaries, topicIDs, checkpoint.ReferenceBaseline), nil
}

func (m *Manager) renderLegacyTopicDigest(ctx context.Context, kind string, targetID int64) (string, error) {
	var topics []TopicThreadRow
	if err := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=?", kind, targetID).Order("updated_at DESC,id DESC").Limit(8).Find(&topics).Error; err != nil {
		return "", err
	}
	var summaries []TopicSummary
	var topicIDs []uint
	for _, topic := range topics {
		var row TopicSummaryRow
		if err := m.db.WithContext(ctx).Where("topic_id=?", topic.ID).Order("version DESC,id DESC").Take(&row).Error; err != nil {
			return "", fmt.Errorf("读取历史话题摘要有误: %w", err)
		}
		var summary TopicSummary
		if err := sonic.UnmarshalString(row.SummaryJSON, &summary); err != nil {
			return "", fmt.Errorf("解析历史话题摘要有误: %w", err)
		}
		summaries = append(summaries, summary)
		topicIDs = append(topicIDs, topic.ID)
	}
	if len(summaries) == 0 {
		return "", nil
	}
	return renderContextDigest("以下是升级前保留的历史摘要，仅作旧事参考。", summaries, topicIDs, ""), nil
}

// RenderCompactionPreview 渲染尚未提交的压缩摘要视图，用于提交前预算校验
func (m *Manager) RenderCompactionPreview(ctx context.Context, input *CompactionCommit) (string, error) {
	if input == nil {
		return "", fmt.Errorf("压缩预览参数为空")
	}
	topicSummaries := make(map[string]TopicSummary, len(input.Topics))
	for _, topic := range input.Topics {
		topicSummaries[strings.TrimSpace(topic.Ref)] = topic.Summary
		if topic.TopicID > 0 {
			topicSummaries[fmt.Sprintf("t%d", topic.TopicID)] = topic.Summary
		}
	}
	summaries := make([]TopicSummary, 0, len(input.ActiveTopicRefs))
	topicIDs := make([]uint, 0, len(input.ActiveTopicRefs))
	for _, rawRef := range input.ActiveTopicRefs {
		ref := strings.TrimSpace(rawRef)
		if summary, ok := topicSummaries[ref]; ok {
			summaries = append(summaries, summary)
			topicIDs = append(topicIDs, parseTopicIDRef(ref))
			continue
		}
		topicID := parseTopicIDRef(ref)
		if topicID == 0 {
			continue
		}
		detail, err := m.ReadTopic(ctx, topicID)
		if err != nil || detail == nil {
			return "", fmt.Errorf("读取待激活话题失败: %w", err)
		}
		summaries = append(summaries, detail.Summary)
		topicIDs = append(topicIDs, topicID)
	}
	overview := strings.TrimSpace(input.Overview)
	active := map[uint]bool{}
	for _, id := range topicIDs {
		active[id] = true
	}
	var existing []TopicThreadRow
	qExisting := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=?", input.ConversationKind, input.TargetID)
	if input.ExpectedVersion > 0 {
		qExisting = qExisting.Where("status='active'")
	}
	if err := qExisting.Find(&existing).Error; err != nil {
		return "", err
	}
	for _, topic := range existing {
		if active[topic.ID] {
			continue
		}
		var latest TopicSummaryRow
		if err := m.db.WithContext(ctx).Where("topic_id=?", topic.ID).Order("version DESC,id DESC").Take(&latest).Error; err != nil {
			return "", fmt.Errorf("读取待降级话题失败: %w", err)
		}
		var summary TopicSummary
		if err := sonic.UnmarshalString(latest.SummaryJSON, &summary); err != nil {
			return "", fmt.Errorf("解析待降级话题失败: %w", err)
		}
		for _, loop := range summary.OpenLoops {
			loop = strings.TrimSpace(loop)
			if loop != "" && !strings.Contains(overview, loop) {
				if overview != "" {
					overview += "\n"
				}
				overview += "未完成事项延续：" + loop
			}
		}
	}
	return renderContextDigest(overview, summaries, topicIDs, input.ReferenceBaseline), nil
}

func renderContextDigest(overview string, summaries []TopicSummary, topicIDs []uint, baseline string) string {
	var sections []string
	if overview = strings.TrimSpace(overview); overview != "" {
		sections = append(sections, "【会话概览】\n"+overview)
	}
	if len(summaries) > 0 {
		sections = append(sections, "【活跃话题】")
		for i, summary := range summaries {
			id := uint(0)
			if i < len(topicIDs) {
				id = topicIDs[i]
			}
			title := strings.TrimSpace(summary.Title)
			block := "### " + summaryTitle(summary, id)
			if id > 0 {
				if title == "" {
					block = fmt.Sprintf("### 话题 t%d", id)
				} else {
					block = fmt.Sprintf("### 话题 t%d · %s", id, title)
				}
			}
			if gist := strings.TrimSpace(summary.Gist); gist != "" {
				block += "\n概况：" + gist
			}
			if len(summary.RecentTurns) > 0 {
				block += "\n近期脉络：" + strings.Join(summary.RecentTurns, "；")
			}
			if len(summary.OpenLoops) > 0 {
				block += "\n未完事项：" + strings.Join(summary.OpenLoops, "；")
			}
			if len(summary.Participants) > 0 {
				names := make([]string, 0, len(summary.Participants))
				for _, participant := range summary.Participants {
					if participant.Nickname != "" {
						names = append(names, participant.Nickname)
					}
				}
				if len(names) > 0 {
					block += "\n在场：" + strings.Join(names, "、")
				}
			}
			sections = append(sections, block)
		}
	}
	if baseline = strings.TrimSpace(baseline); baseline != "" {
		sections = append(sections, "【内部参考基线】\n"+baseline)
	}
	return strings.Join(sections, "\n\n")
}
