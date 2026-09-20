package memory

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LockKnowledgeGroup 在获取行锁前串行化同群的知识写入与消息撤回
func LockKnowledgeGroup(tx *gorm.DB, groupID int64) error {
	if groupID <= 0 {
		return invalidKnowledge("知识所属群无效，请使用当前群号")
	}
	return tx.Exec("SELECT pg_advisory_xact_lock(?)", groupID).Error
}

func (m *Manager) CommitKnowledgeBatch(ctx context.Context, batch KnowledgeBatch) (*KnowledgeCommitResult, error) {
	result := &KnowledgeCommitResult{ItemIDs: map[string]uint{}, ItemStatuses: map[string]string{}}
	if batch.GroupID <= 0 || batch.SelfID <= 0 || batch.ThroughID == 0 {
		return nil, invalidKnowledge("知识批次范围无效，请使用本轮固定消息范围")
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
			return invalidKnowledge("固定范围上界不是当前群消息，请在后续批次重新调查")
		}
		if batch.AdvanceCursor {
			if batch.ThroughID <= batch.AfterID {
				return invalidKnowledge("整理范围起止位置无效，请在后续批次重新调查")
			}
			if err := tx.Exec("INSERT INTO learning_states(group_id,last_message_log_id) VALUES (?,0) ON CONFLICT DO NOTHING", batch.GroupID).Error; err != nil {
				return err
			}
			var cursor uint
			if err := tx.Raw("SELECT last_message_log_id FROM learning_states WHERE group_id=? FOR UPDATE", batch.GroupID).Scan(&cursor).Error; err != nil {
				return err
			}
			if cursor != batch.AfterID {
				return ErrSnapshotChanged
			}
			var pending int64
			if err := tx.Raw(`SELECT count(*) FROM message_logs ml WHERE ml.group_id=? AND ml.id>? AND ml.id<=? AND NOT EXISTS(SELECT 1 FROM topic_assignments ta WHERE ta.message_log_id=ml.id)`, batch.GroupID, batch.AfterID, batch.ThroughID).Scan(&pending).Error; err != nil {
				return err
			}
			if pending != 0 {
				return invalidKnowledge("本批仍有未归属消息，请补全话题归属后重试")
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
				return ErrSnapshotChanged
			}
			for _, row := range rows {
				if !row.UpdatedAt.Equal(batch.ExpectedItems[row.ID]) {
					return ErrSnapshotChanged
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
					return invalidKnowledge("知识临时编号重复，请为每条知识使用不同编号")
				}
				result.ItemIDs[input.Key] = item.ID
			}
			existing[item.ID] = item
		}
		if err := archiveInvalidKnowledgeRelations(tx, batch.GroupID); err != nil {
			return err
		}
		for _, input := range batch.Relations {
			relation, err := saveKnowledgeRelation(tx, batch, input, result.ItemIDs, existing, messages)
			if err != nil {
				return err
			}
			result.RelationIDs = append(result.RelationIDs, relation.ID)
		}
		if err := archiveInvalidKnowledgeRelations(tx, batch.GroupID); err != nil {
			return err
		}
		for key, id := range result.ItemIDs {
			result.ItemStatuses[key] = existing[id].Status
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
	return status == "active" || status == "archived"
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
	if input.SubjectUserID < 0 || !validKnowledgeKind(input.Kind) || (input.Status != "" && !validKnowledgeStatus(input.Status)) || input.Content == "" || (input.Kind == "term" && input.Label == "") {
		return item, invalidKnowledge("知识主体、类型或正文无效，请补全必要字段后重试")
	}
	if input.ID != 0 {
		var ok bool
		item, ok = existing[input.ID]
		_, read := batch.ExpectedItems[input.ID]
		if !ok || !read {
			return item, invalidKnowledge("知识 %d 尚未在本轮读取，请先用 searchKnowledge 获取后重试", input.ID)
		}
		if item.SubjectUserID != input.SubjectUserID || item.Kind != input.Kind || item.Label != input.Label || item.Content != input.Content {
			return item, invalidKnowledge("知识正文语义已变化，请新建知识并用关系表达修正")
		}
	} else {
		err := tx.Where("group_id=? AND subject_user_id=? AND kind=? AND lower(btrim(label))=lower(btrim(?)) AND btrim(content)=?", batch.GroupID, input.SubjectUserID, input.Kind, input.Label, input.Content).Order("id").First(&item).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if len(input.EvidenceSets) == 0 {
				return item, invalidKnowledge("新知识必须提供完整原文依据，请补充后重试")
			}
			if input.Status == "" {
				input.Status = "active"
			}
			item = KnowledgeItem{GroupID: batch.GroupID, SubjectUserID: input.SubjectUserID, Kind: input.Kind, Label: input.Label, Content: input.Content, Status: input.Status, ReviewedThroughID: batch.ThroughID}
			if err = tx.Create(&item).Error; err != nil {
				return item, err
			}
		} else if err != nil {
			return item, err
		} else if _, read := batch.ExpectedItems[item.ID]; !read {
			// 重放可以补充证据，但不能撤销未在本轮读取的状态决定
			input.Status = item.Status
		}
	}
	if input.Status == "" {
		input.Status = item.Status
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
		if err := checkKnowledgeActivation(tx, item.ID); err != nil {
			return item, err
		}
		if item.Status != "active" && len(input.EvidenceSets) == 0 {
			return item, invalidKnowledge("知识启用前必须在本轮完整读取一组有效依据，请先读取后重试")
		}
		ok, err := knowledgeHasEvidence(tx, item.ID, 0)
		if err != nil {
			return item, err
		}
		if !ok {
			return item, invalidKnowledge("启用知识必须具有完整有效依据，请补充证据或放弃提交")
		}
	}
	item.Status = input.Status
	updates := map[string]any{"status": item.Status, "updated_at": time.Now(), "reviewed_through_id": gorm.Expr("GREATEST(reviewed_through_id,?)", batch.ThroughID)}
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
	} else if _, read := batch.ExpectedItems[input.SourceID]; !read {
		return relation, invalidKnowledge("关系来源尚未读取，请先查询知识或使用本批临时编号")
	}
	if input.TargetKey != "" {
		input.TargetID = keys[input.TargetKey]
	} else if _, read := batch.ExpectedItems[input.TargetID]; !read {
		return relation, invalidKnowledge("关系目标尚未读取，请先查询知识或使用本批临时编号")
	}
	source, sok := items[input.SourceID]
	target, tok := items[input.TargetID]
	if !sok || !tok || source.ID == target.ID || source.GroupID != batch.GroupID || target.GroupID != batch.GroupID || (input.Status != "" && !validKnowledgeStatus(input.Status)) {
		return relation, invalidKnowledge("关系两端必须是当前群中两个不同的知识，请修正后重试")
	}
	switch input.Kind {
	case "variant_of":
		if source.Kind != "term" || target.Kind != "term" {
			return relation, invalidKnowledge("变体关系两端都必须是术语义项，请修正知识类型或关系")
		}
	case "part_of":
		if target.Kind != "episode" {
			return relation, invalidKnowledge("属于关系的目标必须是经历，请修正目标或关系类型")
		}
	case "supersedes":
		if source.SubjectUserID != target.SubjectUserID || source.Kind != target.Kind {
			return relation, invalidKnowledge("修正替代关系两端必须具有相同主体和类型，请修正后重试")
		}
	case "contradicts":
		if source.ID > target.ID {
			input.SourceID, input.TargetID = input.TargetID, input.SourceID
		}
	default:
		return relation, invalidKnowledge("关系类型无效，请使用允许的关系类型后重试")
	}
	err := tx.Where("source_item_id=? AND target_item_id=? AND kind=?", input.SourceID, input.TargetID, input.Kind).First(&relation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if len(input.EvidenceSets) == 0 {
			return relation, invalidKnowledge("新关联必须提供独立的原文证据")
		}
		if input.Status == "" {
			input.Status = "active"
		}
		relation = KnowledgeRelation{SourceItemID: input.SourceID, TargetItemID: input.TargetID, Kind: input.Kind, Status: input.Status}
		if err = tx.Create(&relation).Error; err != nil {
			return relation, err
		}
	} else if err != nil {
		return relation, err
	} else {
		_, sourceRead := batch.ExpectedItems[source.ID]
		_, targetRead := batch.ExpectedItems[target.ID]
		if !sourceRead || !targetRead {
			input.Status = relation.Status
		}
	}
	if input.Status == "" {
		input.Status = relation.Status
	}
	if input.Status == "active" && relation.Status != "active" && len(input.EvidenceSets) == 0 {
		return relation, invalidKnowledge("关系重新启用前必须完整读取并提交一组独立依据")
	}
	for _, set := range input.EvidenceSets {
		subject := int64(0)
		if !knowledgeNeedsHuman(source.SubjectUserID, batch.SelfID, source.Kind) && !knowledgeNeedsHuman(target.SubjectUserID, batch.SelfID, target.Kind) {
			subject = batch.SelfID
		}
		if err := validateKnowledgeSubject(subject, batch.SelfID, "relation", set, messages); err != nil {
			return relation, err
		}
		if err := saveKnowledgeEvidence(tx, nil, &relation.ID, set); err != nil {
			return relation, err
		}
	}
	if input.Status == "active" {
		if err := checkRelationActivation(tx, relation); err != nil {
			return relation, err
		}
		if source.Status != "active" || (target.Status != "active" && !(input.Kind == "supersedes" && target.Status == "archived")) {
			return relation, invalidKnowledge("关系启用前两端知识都必须启用，请先处理知识状态")
		}
		for _, id := range []uint{source.ID, target.ID} {
			ok, err := knowledgeHasEvidence(tx, id, 0)
			if err != nil {
				return relation, err
			}
			if !ok {
				return relation, invalidKnowledge("关系端点缺少有效依据，请先补充端点知识的证据")
			}
		}
		ok, err := knowledgeHasEvidence(tx, 0, relation.ID)
		if err != nil {
			return relation, err
		}
		if !ok {
			return relation, invalidKnowledge("关系需要自己的独立原文依据，请补充后重试")
		}
	}
	relation.Status = input.Status
	if err := tx.Model(&relation).Update("status", input.Status).Error; err != nil {
		return relation, err
	}
	// 关系维护同样受固定快照和端点的乐观并发校验约束
	if err := tx.Model(&KnowledgeItem{}).Where("id IN ?", []uint{source.ID, target.ID}).Updates(map[string]any{"updated_at": time.Now(), "reviewed_through_id": gorm.Expr("GREATEST(reviewed_through_id,?)", batch.ThroughID)}).Error; err != nil {
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
