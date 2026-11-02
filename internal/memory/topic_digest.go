package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
)

type ContextTopic struct {
	ID      uint
	Summary TopicSummary
}

type ContextDigest struct {
	Overview string
	Topics   []ContextTopic
}

// LoadContextDigest 读取当前检查点的概览和活跃话题摘要
func (m *Manager) LoadContextDigest(ctx context.Context, kind string, targetID int64) (*ContextDigest, error) {
	checkpoint, err := m.GetCheckpoint(ctx, kind, targetID)
	if err != nil {
		return nil, err
	}
	if checkpoint == nil {
		return &ContextDigest{}, nil
	}
	var snapshots []TopicSnapshot
	if err := sonic.Unmarshal(checkpoint.ActiveTopicVersions, &snapshots); err != nil {
		return nil, err
	}
	topics := make([]ContextTopic, 0, len(snapshots))
	for _, snapshot := range snapshots {
		var row TopicSummaryRow
		if err := m.db.WithContext(ctx).Where("id=? AND topic_id=?", snapshot.SummaryID, snapshot.TopicID).Take(&row).Error; err != nil {
			return nil, fmt.Errorf("读取活跃话题摘要有误: %w", err)
		}
		var summary TopicSummary
		if err := sonic.UnmarshalString(row.SummaryJSON, &summary); err != nil {
			return nil, fmt.Errorf("解析活跃话题摘要有误: %w", err)
		}
		topics = append(topics, ContextTopic{ID: snapshot.TopicID, Summary: summary})
	}
	return &ContextDigest{Overview: checkpoint.Overview, Topics: topics}, nil
}

// RenderCheckpointDigest 渲染检查点中的概览和全部活跃话题
func (m *Manager) RenderCheckpointDigest(ctx context.Context, kind string, targetID int64) (string, error) {
	digest, err := m.LoadContextDigest(ctx, kind, targetID)
	if err != nil {
		return "", err
	}
	return RenderContextDigest(digest.Overview, digest.Topics, 0), nil
}

// RenderContextDigest 渲染概览、保留的话题和未展开数量
func RenderContextDigest(overview string, topics []ContextTopic, omitted int) string {
	summaries := make([]TopicSummary, 0, len(topics))
	topicIDs := make([]uint, 0, len(topics))
	for _, topic := range topics {
		summaries = append(summaries, topic.Summary)
		topicIDs = append(topicIDs, topic.ID)
	}
	return renderContextDigest(overview, summaries, topicIDs, omitted)
}

func renderContextDigest(overview string, summaries []TopicSummary, topicIDs []uint, omitted int) string {
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
	return strings.Join(sections, "\n\n")
}
