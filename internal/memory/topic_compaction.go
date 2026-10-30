package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CommitCompaction 原子写入话题、归属、检查点，并删除被覆盖的工作集记录
func (m *Manager) CommitCompaction(ctx context.Context, input CompactionCommit) error {
	if input.ConversationKind == "" || input.TargetID <= 0 || input.ThroughMessageID == 0 || input.ThroughVisibleSeq == 0 || strings.TrimSpace(input.BatchID) == "" {
		return fmt.Errorf("压缩提交范围无效")
	}
	if input.MaxInputTokens > 0 && input.OutputTokens > input.MaxInputTokens {
		return fmt.Errorf("压缩结果仍超过 55%% 上限：%d > %d", input.OutputTokens, input.MaxInputTokens)
	}
	if input.UnassignedCount > 16 || (input.UnassignedCount > 0 && input.Coverage < 0.80) {
		return fmt.Errorf("话题覆盖率不足：未分配 %d 条", input.UnassignedCount)
	}
	topicIDs := map[string]uint{}
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var checkpoint CompactionCheckpoint
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_kind=? AND target_id=?", input.ConversationKind, input.TargetID).Take(&checkpoint).Error
		currentVersion := uint64(0)
		hasCheckpoint := err == nil
		if hasCheckpoint {
			currentVersion = checkpoint.Version
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		if currentVersion != input.ExpectedVersion {
			return fmt.Errorf("上下文检查点已变化，请重新读取后再压缩")
		}
		nextVersion := currentVersion + 1

		oneBotIDs := make([]int64, 0)
		for _, topicInput := range input.Topics {
			for _, ref := range topicInput.Messages {
				oneBotIDs = append(oneBotIDs, ref.MessageID)
			}
		}
		messageByOneBot := make(map[int64]uint, len(oneBotIDs))
		if len(oneBotIDs) > 0 {
			var messages []MessageLog
			if err := tx.Where("conversation_kind=? AND target_id=? AND one_bot_message_id IN ? AND id<=?",
				input.ConversationKind, input.TargetID, oneBotIDs, input.ThroughMessageID).Find(&messages).Error; err != nil {
				return err
			}
			for _, message := range messages {
				messageByOneBot[message.OneBotMessageID] = message.ID
			}
		}
		if err := validateTopicMessages(input, messageByOneBot); err != nil {
			return err
		}
		var existing []TopicThreadRow
		if err := tx.Where("conversation_kind=? AND target_id=?", input.ConversationKind, input.TargetID).Order("id").Find(&existing).Error; err != nil {
			return err
		}
		existingIDs := map[uint]bool{}
		existingTopicIDs := make([]uint, 0, len(existing))
		for _, topic := range existing {
			existingIDs[topic.ID] = true
			existingTopicIDs = append(existingTopicIDs, topic.ID)
		}
		latestByTopic, err := latestTopicSummaries(tx, existingTopicIDs)
		if err != nil {
			return err
		}
		type openLoopBatch struct {
			topicID uint
			loops   []string
		}
		oldOpenLoops := make([]openLoopBatch, 0)
		for _, topic := range existing {
			if topic.Status != "active" && hasCheckpoint {
				continue
			}
			if latest, ok := latestByTopic[topic.ID]; ok {
				var summary TopicSummary
				if sonic.UnmarshalString(latest.SummaryJSON, &summary) == nil && len(summary.OpenLoops) > 0 {
					oldOpenLoops = append(oldOpenLoops, openLoopBatch{topicID: topic.ID, loops: summary.OpenLoops})
				}
			}
		}

		for i := range input.Topics {
			topicInput := &input.Topics[i]
			if topicInput.TopicID == 0 {
				if id := parseTopicIDRef(topicInput.Ref); id > 0 {
					if !existingIDs[id] {
						return fmt.Errorf("话题 %d 不属于当前会话", id)
					}
					topicInput.TopicID = id
				} else {
					thread := TopicThreadRow{ConversationKind: input.ConversationKind, TargetID: input.TargetID, Status: "dormant"}
					if err := tx.Create(&thread).Error; err != nil {
						return err
					}
					topicInput.TopicID = thread.ID
				}
			} else {
				if !existingIDs[topicInput.TopicID] {
					return fmt.Errorf("话题 %d 不属于当前会话", topicInput.TopicID)
				}
			}
			if _, exists := topicIDs[topicInput.Ref]; exists {
				return fmt.Errorf("话题引用 %s 重复", topicInput.Ref)
			}
			topicIDs[topicInput.Ref] = topicInput.TopicID
			existingIDs[topicInput.TopicID] = true
		}

		updatedTopicIDs := map[uint]bool{}
		for _, id := range topicIDs {
			updatedTopicIDs[id] = true
		}
		activeIDs := make([]uint, 0, len(input.ActiveTopicRefs))
		activeSet := map[uint]bool{}
		for _, ref := range input.ActiveTopicRefs {
			ref = strings.TrimSpace(ref)
			id := topicIDs[ref]
			if id == 0 {
				id = parseTopicIDRef(ref)
			}
			if id == 0 || !existingIDs[id] {
				return fmt.Errorf("激活话题引用 %s 无效", ref)
			}
			if activeSet[id] {
				return fmt.Errorf("激活话题 %d 重复", id)
			}
			activeSet[id] = true
			activeIDs = append(activeIDs, id)
		}

		assigned := map[uint]bool{}
		assignments := make([]TopicAssignment, 0)
		sources := make([]TopicSourceRow, 0)
		for _, topicInput := range input.Topics {
			if err := ValidateTopicSummary(topicInput.Summary); err != nil {
				return err
			}
			latest, hasLatest := latestByTopic[topicInput.TopicID]
			nextSummaryVersion := uint64(1)
			if hasLatest {
				nextSummaryVersion = latest.Version + 1
			}
			body, err := sonic.MarshalString(topicInput.Summary)
			if err != nil {
				return err
			}
			row := TopicSummaryRow{TopicID: topicInput.TopicID, Version: nextSummaryVersion, SummaryJSON: body}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			latestByTopic[topicInput.TopicID] = row
			for _, ref := range topicInput.Messages {
				messageID, ok := messageByOneBot[ref.MessageID]
				if !ok || messageID > input.ThroughMessageID {
					return fmt.Errorf("消息 %d 不属于当前压缩范围", ref.MessageID)
				}
				topicID := topicInput.TopicID
				assignments = append(assignments, TopicAssignment{MessageLogID: messageID, TopicID: &topicID})
				sources = append(sources, TopicSourceRow{SummaryID: row.ID, MessageLogID: messageID, IsAnchor: ref.IsAnchor})
				assigned[messageID] = true
			}
		}
		if len(assignments) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "message_log_id"}},
				DoUpdates: clause.Assignments(map[string]any{"topic_id": gorm.Expr("EXCLUDED.topic_id")}),
			}).CreateInBatches(assignments, 500).Error; err != nil {
				return err
			}
		}
		if len(sources) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(sources, 500).Error; err != nil {
				return err
			}
		}

		if len(input.CandidateMessageIDs) > 0 {
			var found int64
			if err := tx.Model(&MessageLog{}).Where("id IN ? AND conversation_kind=? AND target_id=? AND id<=?",
				input.CandidateMessageIDs, input.ConversationKind, input.TargetID, input.ThroughMessageID).Count(&found).Error; err != nil {
				return err
			}
			if int(found) != len(input.CandidateMessageIDs) {
				return fmt.Errorf("压缩候选消息范围无效")
			}
			unassigned := make([]uint, 0, len(input.CandidateMessageIDs))
			for _, id := range input.CandidateMessageIDs {
				if !assigned[id] {
					unassigned = append(unassigned, id)
				}
			}
			if len(unassigned) > 0 {
				if err := tx.Model(&TopicAssignment{}).Where("message_log_id IN ? AND topic_id IS NOT NULL", unassigned).Update("topic_id", nil).Error; err != nil {
					return err
				}
			}
		}

		overview := strings.TrimSpace(input.Overview)
		for _, batch := range oldOpenLoops {
			if activeSet[batch.topicID] {
				continue
			}
			for _, loop := range batch.loops {
				loop = strings.TrimSpace(loop)
				if loop != "" && !strings.Contains(overview, loop) {
					if overview != "" {
						overview += "\n"
					}
					overview += "未完成事项延续：" + loop
				}
			}
		}
		if overview == "" {
			return fmt.Errorf("压缩概览不能为空")
		}
		if len([]rune(overview)) > 12000 {
			return fmt.Errorf("压缩概览超过 12000 字")
		}

		for _, topic := range existing {
			status := "dormant"
			if activeSet[topic.ID] {
				status = "active"
			}
			if topic.Status == status && !updatedTopicIDs[topic.ID] {
				continue
			}
			if err := tx.Model(&TopicThreadRow{}).Where("id=?", topic.ID).Updates(map[string]any{"status": status, "updated_at": time.Now()}).Error; err != nil {
				return err
			}
		}
		for _, id := range activeIDs {
			if old, ok := topicByID(existing, id); ok && old.Status == "active" && !updatedTopicIDs[id] {
				continue
			}
			if err := tx.Model(&TopicThreadRow{}).Where("id=?", id).Updates(map[string]any{"status": "active", "updated_at": time.Now()}).Error; err != nil {
				return err
			}
		}

		snapshots := make([]TopicSnapshot, 0, len(activeIDs))
		for _, topicID := range activeIDs {
			latest, ok := latestByTopic[topicID]
			if !ok {
				return fmt.Errorf("激活话题 %d 缺少摘要", topicID)
			}
			snapshots = append(snapshots, TopicSnapshot{TopicID: topicID, SummaryID: latest.ID})
		}
		snapshotJSON, err := TopicSnapshotJSON(snapshots)
		if err != nil {
			return err
		}
		throughVisible := input.ThroughVisibleSeq
		throughMessage := input.ThroughMessageID
		checkpoint = CompactionCheckpoint{
			ConversationKind: input.ConversationKind, TargetID: input.TargetID, Version: nextVersion,
			ThroughVisibleSeq: &throughVisible, ThroughMessageLogID: &throughMessage,
			Overview: overview, ActiveTopicVersions: snapshotJSON,
			ReferenceBaseline: input.ReferenceBaseline, ReferenceVersions: mustJSONMap(input.ReferenceVersions),
			BatchID: input.BatchID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		if err := m.SaveCheckpoint(tx, checkpoint); err != nil {
			return err
		}
		if err := m.ResetReferenceSeen(tx, input.ConversationKind, input.TargetID, input.ReferenceVersions); err != nil {
			return err
		}
		// 压缩基线已覆盖当前参考状态，旧的 pending 参考不再进入下一轮上下文
		if err := tx.Where("conversation_kind=? AND target_id=? AND entry_kind='internal_reference' AND visible_seq IS NULL", input.ConversationKind, input.TargetID).Delete(&TranscriptEntry{}).Error; err != nil {
			return err
		}
		if err := m.DeleteTranscriptThrough(tx, input.ConversationKind, input.TargetID, input.ThroughVisibleSeq); err != nil {
			return err
		}
		return nil
	})
	return err
}

func ValidateTopicSummary(summary TopicSummary) error {
	if strings.TrimSpace(summary.Title) == "" || strings.TrimSpace(summary.Gist) == "" {
		return fmt.Errorf("话题摘要必须包含标题和概况")
	}
	if len([]rune(summary.Title)) > 120 || len([]rune(summary.Gist)) > 4000 {
		return fmt.Errorf("话题标题或概况过长")
	}
	if len(summary.RecentTurns) > 8 || len(summary.OpenLoops) > 12 || len(summary.Keywords) > 20 || len(summary.Participants) > 20 {
		return fmt.Errorf("话题摘要列表过长")
	}
	for _, item := range append(append([]string{}, summary.RecentTurns...), summary.OpenLoops...) {
		if len([]rune(item)) > 300 {
			return fmt.Errorf("话题脉络或未完事项单条超过 300 字")
		}
	}
	for _, keyword := range summary.Keywords {
		if len([]rune(keyword)) > 50 {
			return fmt.Errorf("话题关键词过长")
		}
	}
	return nil
}

func topicByID(topics []TopicThreadRow, id uint) (TopicThreadRow, bool) {
	for _, topic := range topics {
		if topic.ID == id {
			return topic, true
		}
	}
	return TopicThreadRow{}, false
}

func parseTopicIDRef(ref string) uint {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "t") {
		return 0
	}
	var id uint
	if _, err := fmt.Sscanf(ref, "t%d", &id); err != nil {
		return 0
	}
	return id
}

func validateTopicMessages(input CompactionCommit, messageByOneBot map[int64]uint) error {
	seen := map[int64]bool{}
	for _, topic := range input.Topics {
		for _, ref := range topic.Messages {
			if ref.MessageID == 0 {
				return fmt.Errorf("话题 message_id 不能为空")
			}
			if seen[ref.MessageID] {
				return fmt.Errorf("消息 %d 重复归属多个话题", ref.MessageID)
			}
			seen[ref.MessageID] = true
			if _, ok := messageByOneBot[ref.MessageID]; !ok {
				return fmt.Errorf("消息 %d 不属于当前压缩范围", ref.MessageID)
			}
		}
	}
	return nil
}

func mustJSONMap(values map[string]string) []byte {
	if values == nil {
		values = map[string]string{}
	}
	raw, _ := sonic.Marshal(values)
	return raw
}
