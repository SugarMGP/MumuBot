package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LockKnowledgeGroup 在获取行锁前串行化同群的知识写入与消息撤回
func LockKnowledgeGroup(tx *gorm.DB, groupID int64) error {
	if groupID <= 0 {
		return fmt.Errorf("invalid knowledge group")
	}
	return tx.Exec("SELECT pg_advisory_xact_lock(?)", groupID).Error
}

func (m *Manager) CommitKnowledgeBatch(ctx context.Context, batch KnowledgeBatch) (*KnowledgeCommitResult, error) {
	result := &KnowledgeCommitResult{ItemIDs: map[string]uint{}}
	if batch.GroupID <= 0 || batch.SelfID <= 0 || batch.ThroughID == 0 {
		return nil, fmt.Errorf("invalid knowledge scope")
	}
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := LockKnowledgeGroup(tx, batch.GroupID); err != nil {
			return err
		}
		var upperCount int64
		if err := tx.Model(&MessageLog{}).Where("id=? AND group_id=?", batch.ThroughID, batch.GroupID).Count(&upperCount).Error; err != nil {
			return err
		}
		if upperCount != 1 {
			return fmt.Errorf("snapshot upper bound is not a group message")
		}
		if batch.AdvanceCursor {
			if batch.ThroughID <= batch.AfterID {
				return fmt.Errorf("invalid scan bounds")
			}
			if err := tx.Exec("INSERT INTO learning_states(group_id,last_message_log_id) VALUES (?,0) ON CONFLICT DO NOTHING", batch.GroupID).Error; err != nil {
				return err
			}
			var cursor uint
			if err := tx.Raw("SELECT last_message_log_id FROM learning_states WHERE group_id=? FOR UPDATE", batch.GroupID).Scan(&cursor).Error; err != nil {
				return err
			}
			if cursor != batch.AfterID {
				return fmt.Errorf("knowledge scan changed")
			}
			var pending int64
			if err := tx.Raw(`SELECT count(*) FROM message_logs ml WHERE ml.group_id=? AND ml.id>? AND ml.id<=? AND NOT EXISTS(SELECT 1 FROM topic_assignments ta WHERE ta.message_log_id=ml.id)`, batch.GroupID, batch.AfterID, batch.ThroughID).Scan(&pending).Error; err != nil {
				return err
			}
			if pending != 0 {
				return fmt.Errorf("batch contains unassigned messages")
			}
		}
		messages, err := lockKnowledgeEvidence(tx, batch)
		if err != nil {
			return err
		}
		ids := make([]uint, 0, len(batch.ExpectedItems))
		for id := range batch.ExpectedItems {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		existing := map[uint]KnowledgeItem{}
		if len(ids) > 0 {
			var rows []KnowledgeItem
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ? AND group_id=?", ids, batch.GroupID).Order("id").Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) != len(ids) {
				return fmt.Errorf("knowledge references changed")
			}
			for _, row := range rows {
				if !row.UpdatedAt.Equal(batch.ExpectedItems[row.ID]) {
					return fmt.Errorf("knowledge item %d changed", row.ID)
				}
				existing[row.ID] = row
			}
		}
		for _, input := range batch.Items {
			item, err := saveKnowledgeItem(tx, batch, input, messages, existing)
			if err != nil {
				return err
			}
			if input.Key != "" {
				if _, ok := result.ItemIDs[input.Key]; ok {
					return fmt.Errorf("duplicate temporary key")
				}
				result.ItemIDs[input.Key] = item.ID
			}
			existing[item.ID] = item
		}
		for _, input := range batch.Relations {
			relation, err := saveKnowledgeRelation(tx, batch, input, result.ItemIDs, existing, messages)
			if err != nil {
				return err
			}
			result.RelationIDs = append(result.RelationIDs, relation.ID)
		}
		for _, id := range batch.ReviewedIDs {
			if _, ok := batch.ExpectedItems[id]; !ok {
				return fmt.Errorf("unread reviewed item")
			}
			if err := tx.Model(&KnowledgeItem{}).Where("id=? AND group_id=?", id, batch.GroupID).Updates(map[string]any{"reviewed_through_id": gorm.Expr("GREATEST(reviewed_through_id,?)", batch.ThroughID), "updated_at": time.Now()}).Error; err != nil {
				return err
			}
		}
		if batch.AdvanceCursor {
			return tx.Exec("UPDATE learning_states SET last_message_log_id=? WHERE group_id=?", batch.ThroughID, batch.GroupID).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validKnowledgeStatus(status string) bool {
	return status == "candidate" || status == "active" || status == "archived"
}
func validKnowledgeKind(kind string) bool {
	switch kind {
	case "fact", "episode", "preference", "constraint", "goal", "term", "expression", "alias":
		return true
	}
	return false
}

func saveKnowledgeItem(tx *gorm.DB, batch KnowledgeBatch, input KnowledgeItemInput, messages map[uint]MessageLog, existing map[uint]KnowledgeItem) (KnowledgeItem, error) {
	item := KnowledgeItem{}
	input.Label = strings.TrimSpace(input.Label)
	input.Content = strings.TrimSpace(input.Content)
	if input.SubjectUserID < 0 || !validKnowledgeKind(input.Kind) || !validKnowledgeStatus(input.Status) || input.Content == "" || (input.Kind == "term" && input.Label == "") {
		return item, fmt.Errorf("invalid knowledge item")
	}
	if input.ID != 0 {
		var ok bool
		item, ok = existing[input.ID]
		if !ok {
			return item, fmt.Errorf("unread knowledge item")
		}
		if item.SubjectUserID != input.SubjectUserID || item.Kind != input.Kind || item.Label != input.Label || item.Content != input.Content {
			return item, fmt.Errorf("changed meaning requires a new item")
		}
	} else {
		err := tx.Where("group_id=? AND subject_user_id=? AND kind=? AND lower(btrim(label))=lower(btrim(?)) AND btrim(content)=?", batch.GroupID, input.SubjectUserID, input.Kind, input.Label, input.Content).Order("id").First(&item).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if len(input.EvidenceSets) == 0 {
				return item, fmt.Errorf("new knowledge requires source evidence, even while pending")
			}
			item = KnowledgeItem{GroupID: batch.GroupID, SubjectUserID: input.SubjectUserID, Kind: input.Kind, Label: input.Label, Content: input.Content, Status: "candidate"}
			if err = tx.Create(&item).Error; err != nil {
				return item, err
			}
		} else if err != nil {
			return item, err
		}
		// 重放可以补充证据，但不能撤销已有的审核决定
		if _, read := batch.ExpectedItems[item.ID]; !read && item.Status != "candidate" {
			input.Status = item.Status
		}
	}
	for _, set := range input.EvidenceSets {
		if err := validateKnowledgeSubject(input.SubjectUserID, batch.SelfID, input.Kind, set, messages); err != nil {
			return item, err
		}
		if err := saveKnowledgeEvidence(tx, &item.ID, nil, set); err != nil {
			return item, err
		}
	}
	if input.Status == "active" {
		if item.Status != "active" && len(input.EvidenceSets) == 0 {
			return item, fmt.Errorf("activation requires a complete evidence group read in this run")
		}
		ok, err := knowledgeHasEvidence(tx, item.ID, 0)
		if err != nil {
			return item, err
		}
		if !ok {
			return item, fmt.Errorf("active knowledge requires complete evidence")
		}
	}
	item.Status = input.Status
	updates := map[string]any{"status": item.Status, "updated_at": time.Now()}
	if item.Status == "candidate" {
		updates["reviewed_through_id"] = 0
		if batch.AdvanceCursor {
			updates["reviewed_through_id"] = batch.ThroughID
		}
	}
	err := tx.Model(&item).Updates(updates).Error
	if err == nil {
		err = tx.First(&item, item.ID).Error
	}
	return item, err
}

func saveKnowledgeRelation(tx *gorm.DB, batch KnowledgeBatch, input KnowledgeRelationInput, keys map[string]uint, items map[uint]KnowledgeItem, messages map[uint]MessageLog) (KnowledgeRelation, error) {
	relation := KnowledgeRelation{}
	if input.SourceKey != "" {
		input.SourceID = keys[input.SourceKey]
	}
	if input.TargetKey != "" {
		input.TargetID = keys[input.TargetKey]
	}
	source, sok := items[input.SourceID]
	target, tok := items[input.TargetID]
	if !sok || !tok || source.ID == target.ID || source.GroupID != batch.GroupID || target.GroupID != batch.GroupID || !validKnowledgeStatus(input.Status) {
		return relation, fmt.Errorf("invalid relation endpoints")
	}
	switch input.Kind {
	case "variant_of":
		if source.Kind != "term" || target.Kind != "term" {
			return relation, fmt.Errorf("variants require term endpoints")
		}
	case "part_of":
		if target.Kind != "episode" {
			return relation, fmt.Errorf("part_of target must be an episode")
		}
	case "supersedes":
		if source.SubjectUserID != target.SubjectUserID || source.Kind != target.Kind {
			return relation, fmt.Errorf("replacement must share subject and kind")
		}
	case "contradicts":
		if source.ID > target.ID {
			input.SourceID, input.TargetID = input.TargetID, input.SourceID
		}
	default:
		return relation, fmt.Errorf("invalid relation kind")
	}
	err := tx.Where("source_item_id=? AND target_item_id=? AND kind=?", input.SourceID, input.TargetID, input.Kind).First(&relation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		relation = KnowledgeRelation{SourceItemID: input.SourceID, TargetItemID: input.TargetID, Kind: input.Kind, Status: "candidate"}
		if err = tx.Create(&relation).Error; err != nil {
			return relation, err
		}
	} else if err != nil {
		return relation, err
	}
	for _, set := range input.EvidenceSets {
		subject := int64(0)
		if !knowledgeNeedsHuman(source.SubjectUserID, batch.SelfID, source.Kind) && !knowledgeNeedsHuman(target.SubjectUserID, batch.SelfID, target.Kind) {
			subject = batch.SelfID
		}
		if err := validateKnowledgeSubject(subject, batch.SelfID, "relation", set, messages); err != nil {
			return relation, err
		}
		for _, id := range set {
			if _, ok := messages[id]; !ok {
				return relation, fmt.Errorf("unread relation evidence")
			}
		}
		if err := saveKnowledgeEvidence(tx, nil, &relation.ID, set); err != nil {
			return relation, err
		}
	}
	if input.Status == "active" {
		if source.Status != "active" || (target.Status != "active" && !(input.Kind == "supersedes" && target.Status == "archived")) {
			return relation, fmt.Errorf("active relation requires active endpoints")
		}
		for _, id := range []uint{source.ID, target.ID} {
			ok, err := knowledgeHasEvidence(tx, id, 0)
			if err != nil {
				return relation, err
			}
			if !ok {
				return relation, fmt.Errorf("endpoint has no valid evidence")
			}
		}
		ok, err := knowledgeHasEvidence(tx, 0, relation.ID)
		if err != nil {
			return relation, err
		}
		if !ok {
			return relation, fmt.Errorf("relation requires independent evidence")
		}
	}
	relation.Status = input.Status
	if err := tx.Model(&relation).Update("status", input.Status).Error; err != nil {
		return relation, err
	}
	// 更新两个端点的时间，使后续审核能检测到关系状态变化
	if err := tx.Model(&KnowledgeItem{}).Where("id IN ?", []uint{source.ID, target.ID}).Update("updated_at", time.Now()).Error; err != nil {
		return relation, err
	}
	if input.Kind == "supersedes" && input.Status == "active" {
		if err := tx.Model(&KnowledgeItem{}).Where("id=?", target.ID).Updates(map[string]any{"status": "archived", "updated_at": time.Now()}).Error; err != nil {
			return relation, err
		}
		target.Status = "archived"
		items[target.ID] = target
	}
	return relation, nil
}
