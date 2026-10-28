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
func (m *Manager) CommitCompaction(ctx context.Context, input CompactionCommit) (*CompactionCommitResult, error) {
	if input.ConversationKind == "" || input.TargetID <= 0 || input.ThroughMessageID == 0 || input.ThroughVisibleSeq == 0 || strings.TrimSpace(input.BatchID) == "" {
		return nil, fmt.Errorf("压缩提交范围无效")
	}
	if input.MaxInputTokens > 0 && input.OutputTokens > input.MaxInputTokens {
		return nil, fmt.Errorf("压缩结果仍超过 55%% 上限：%d > %d", input.OutputTokens, input.MaxInputTokens)
	}
	result := &CompactionCommitResult{TopicIDs: map[string]uint{}}
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

		if err := validateCompactionCoverage(tx, input); err != nil {
			return err
		}
		var existing []TopicThreadRow
		if err := tx.Where("conversation_kind=? AND target_id=?", input.ConversationKind, input.TargetID).Find(&existing).Error; err != nil {
			return err
		}
		existingIDs := map[uint]bool{}
		for _, topic := range existing {
			existingIDs[topic.ID] = true
		}
		oldOpenLoops := map[uint][]string{}
		for _, topic := range existing {
			if topic.Status != "active" && hasCheckpoint {
				continue
			}
			var latest TopicSummaryRow
			if err := tx.Where("topic_id=?", topic.ID).Order("version DESC,id DESC").Take(&latest).Error; err == nil {
				var summary TopicSummary
				if sonic.UnmarshalString(latest.SummaryJSON, &summary) == nil {
					oldOpenLoops[topic.ID] = summary.OpenLoops
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
			if _, exists := result.TopicIDs[topicInput.Ref]; exists {
				return fmt.Errorf("话题引用 %s 重复", topicInput.Ref)
			}
			result.TopicIDs[topicInput.Ref] = topicInput.TopicID
			existingIDs[topicInput.TopicID] = true
		}

		updatedTopicIDs := map[uint]bool{}
		for _, id := range result.TopicIDs {
			updatedTopicIDs[id] = true
		}
		activeIDs := make([]uint, 0, len(input.ActiveTopicRefs))
		activeSet := map[uint]bool{}
		for _, ref := range input.ActiveTopicRefs {
			ref = strings.TrimSpace(ref)
			id := result.TopicIDs[ref]
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

		for _, topicInput := range input.Topics {
			if err := validateTopicSummary(topicInput.Summary); err != nil {
				return err
			}
			if err := assignTopicMessages(tx, input, topicInput); err != nil {
				return err
			}
			var latest TopicSummaryRow
			err := tx.Where("topic_id=?", topicInput.TopicID).Order("version DESC,id DESC").Take(&latest).Error
			if err != nil && err != gorm.ErrRecordNotFound {
				return err
			}
			nextSummaryVersion := uint64(1)
			if err == nil {
				nextSummaryVersion = latest.Version + 1
			}
			topicInput.Summary.Version = int(nextSummaryVersion)
			body, err := sonic.MarshalString(topicInput.Summary)
			if err != nil {
				return err
			}
			row := TopicSummaryRow{TopicID: topicInput.TopicID, Version: nextSummaryVersion, SummaryJSON: body}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			for _, id := range topicInput.SourceIDs {
				if err := createTopicSource(tx, input, row.ID, id, "source"); err != nil {
					return err
				}
			}
			for _, id := range topicInput.AnchorIDs {
				if err := createTopicSource(tx, input, row.ID, id, "anchor"); err != nil {
					return err
				}
			}
		}
		for _, id := range input.NoTopicIDs {
			if err := assignNoTopic(tx, input, id); err != nil {
				return err
			}
		}

		overview := strings.TrimSpace(input.Overview)
		for topicID, loops := range oldOpenLoops {
			if activeSet[topicID] {
				continue
			}
			for _, loop := range loops {
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
			var latest TopicSummaryRow
			if err := tx.Where("topic_id=?", topicID).Order("version DESC,id DESC").Take(&latest).Error; err != nil {
				return err
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
			ReferenceBaseline: input.ReferenceBaseline, ReferenceVersions: mustJSONStrings(input.ReferenceVersions),
			BatchID: input.BatchID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		if err := m.SaveCheckpoint(tx, checkpoint); err != nil {
			return err
		}
		if err := m.DeleteTranscriptThrough(tx, input.ConversationKind, input.TargetID, input.ThroughVisibleSeq); err != nil {
			return err
		}
		result.CheckpointVersion = nextVersion
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validateTopicSummary(summary TopicSummary) error {
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

func validateCompactionCoverage(tx *gorm.DB, input CompactionCommit) error {
	expected := map[uint]bool{}
	for _, id := range input.CoveredMessageIDs {
		if id == 0 || id > input.ThroughMessageID {
			return fmt.Errorf("压缩候选消息 %d 越界", id)
		}
		var message MessageLog
		if err := tx.Where("id=? AND conversation_kind=? AND target_id=?", id, input.ConversationKind, input.TargetID).Take(&message).Error; err != nil {
			return fmt.Errorf("压缩候选消息 %d 不属于当前会话", id)
		}
		expected[id] = true
	}
	covered := map[uint]bool{}
	mark := func(id uint) error {
		if !expected[id] {
			return fmt.Errorf("压缩结果包含候选范围外消息 %d", id)
		}
		if covered[id] {
			return fmt.Errorf("压缩结果重复覆盖消息 %d", id)
		}
		covered[id] = true
		return nil
	}
	for _, topic := range input.Topics {
		for _, id := range topic.MessageIDs {
			if err := mark(id); err != nil {
				return err
			}
		}
	}
	for _, id := range input.NoTopicIDs {
		if err := mark(id); err != nil {
			return err
		}
	}
	for id := range expected {
		if !covered[id] {
			return fmt.Errorf("压缩结果遗漏消息 %d", id)
		}
	}
	return nil
}

func mustJSONStrings(values []string) []byte {
	if values == nil {
		values = []string{}
	}
	raw, _ := sonic.Marshal(values)
	return raw
}

func assignTopicMessages(tx *gorm.DB, input CompactionCommit, topic CompactionTopicInput) error {
	for _, messageID := range topic.MessageIDs {
		var message MessageLog
		if err := tx.Where("id=? AND conversation_kind=? AND target_id=? AND id<=?", messageID, input.ConversationKind, input.TargetID, input.ThroughMessageID).Take(&message).Error; err != nil {
			return fmt.Errorf("消息 %d 不属于当前压缩范围", messageID)
		}
		var old TopicAssignment
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("message_log_id=?", messageID).Take(&old).Error
		if err == nil {
			if old.TopicID == nil || *old.TopicID != topic.TopicID {
				if err := tx.Model(&TopicAssignment{}).Where("id=?", old.ID).Update("topic_id", topic.TopicID).Error; err != nil {
					return err
				}
			}
			continue
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		if err := tx.Create(&TopicAssignment{MessageLogID: messageID, TopicID: &topic.TopicID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func assignNoTopic(tx *gorm.DB, input CompactionCommit, messageID uint) error {
	var message MessageLog
	if err := tx.Where("id=? AND conversation_kind=? AND target_id=? AND id<=?", messageID, input.ConversationKind, input.TargetID, input.ThroughMessageID).Take(&message).Error; err != nil {
		return fmt.Errorf("消息 %d 不属于当前压缩范围", messageID)
	}
	var old TopicAssignment
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("message_log_id=?", messageID).Take(&old).Error
	if err == nil {
		if old.TopicID != nil {
			return tx.Model(&TopicAssignment{}).Where("id=?", old.ID).Update("topic_id", nil).Error
		}
		return nil
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}
	return tx.Create(&TopicAssignment{MessageLogID: messageID}).Error
}

func createTopicSource(tx *gorm.DB, input CompactionCommit, summaryID, messageID uint, role string) error {
	if messageID == 0 || messageID > input.ThroughMessageID {
		return fmt.Errorf("话题来源消息 %d 超出压缩范围", messageID)
	}
	var message MessageLog
	if err := tx.Where("id=? AND conversation_kind=? AND target_id=?", messageID, input.ConversationKind, input.TargetID).Take(&message).Error; err != nil {
		return fmt.Errorf("话题来源消息 %d 不属于当前会话", messageID)
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&TopicSourceRow{SummaryID: summaryID, MessageLogID: messageID, Role: role}).Error
}
