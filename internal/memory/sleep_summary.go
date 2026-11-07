package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SleepConversationSummary 是睡眠期间一个活跃会话的真人消息概况
type SleepConversationSummary struct {
	ConversationKind string    `json:"conversation_kind"`
	TargetID         int64     `json:"target_id"`
	Name             string    `json:"name,omitempty"`
	Count            int       `json:"count"`
	LastAt           time.Time `json:"last_at"`
}

// SummarizeSleepMessages 聚合睡眠期间的真人消息数量，不生成未读表
func (m *Manager) SummarizeSleepMessages(ctx context.Context, from, to time.Time, selfID int64) ([]SleepConversationSummary, int, error) {
	if from.IsZero() || !to.After(from) {
		return nil, 0, nil
	}
	var rows []struct {
		ConversationKind string
		TargetID         int64
		Name             string
		Count            int
		LastAt           time.Time
	}
	err := m.db.WithContext(ctx).Raw(`
		SELECT ml.conversation_kind, ml.target_id,
			COALESCE(NULLIF(ct.name,''), NULLIF(ct.remote_remark,''), '') AS name,
			COUNT(*) AS count, MAX(ml.message_time) AS last_at
		FROM message_logs ml
		JOIN conversation_targets ct
			ON ct.conversation_kind=ml.conversation_kind AND ct.target_id=ml.target_id
		WHERE ml.message_time>=? AND ml.message_time<?
			AND ct.active=true AND ct.blocked=false AND ml.user_id<>?
		GROUP BY ml.conversation_kind, ml.target_id, ct.name, ct.remote_remark
		ORDER BY last_at DESC, ml.conversation_kind, ml.target_id`, from, to, selfID).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	items := make([]SleepConversationSummary, 0, len(rows))
	total := 0
	for _, row := range rows {
		items = append(items, SleepConversationSummary{
			ConversationKind: row.ConversationKind, TargetID: row.TargetID,
			Name: row.Name, Count: row.Count, LastAt: row.LastAt,
		})
		total += row.Count
	}
	return items, total, nil
}

// FormatSleepSummary 把概况写成可读的系统事实，不声称精确逐条未读
func FormatSleepSummary(items []SleepConversationSummary, total int, from, to time.Time) string {
	if from.IsZero() || !to.After(from) {
		return ""
	}
	header := fmt.Sprintf("【睡眠期间的新消息概况｜系统事实，不是未读计数】\n从 %s 到 %s，%d 个会话共有 %d 条真人消息。",
		formatSleepTime(from), formatSleepTime(to), len(items), total)
	if len(items) == 0 {
		return header + "\n睡眠期间没有新的真人消息。"
	}
	lines := make([]string, 0, len(items)+1)
	limit := len(items)
	if limit > 20 {
		limit = 20
	}
	for _, item := range items[:limit] {
		label := "会话"
		if item.ConversationKind == ConversationKindGroup {
			label = "群"
		} else if item.ConversationKind == ConversationKindPrivate {
			label = "好友"
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = fmt.Sprint(item.TargetID)
		}
		lines = append(lines, fmt.Sprintf("- %s「%s」%d 条，最后 %s", label, name, item.Count, formatSleepTime(item.LastAt)))
	}
	if len(items) > limit {
		lines = append(lines, fmt.Sprintf("- 另有 %d 个会话有新消息", len(items)-limit))
	}
	lines = append(lines, "这些会话不会被自动唤醒；需要时按时间和会话用历史工具读取，不要把概况当成完整正文。")
	return header + "\n" + strings.Join(lines, "\n")
}

func formatSleepTime(value time.Time) string {
	return value.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02 15:04")
}
