package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ClaimValidationError struct {
	Code string
	Err  error
}

func (e *ClaimValidationError) Error() string {
	if e.Err == nil {
		return e.Code
	}
	return e.Code + ": " + e.Err.Error()
}

func claimError(code, message string) error {
	return &ClaimValidationError{Code: code, Err: errors.New(message)}
}

type StoreClaimResult struct {
	Memory Memory
	Action string
}

type preparedMemory struct {
	incoming Memory
	decision memoryMergeDecision
	evidence []uint
}

func NormalizeMemoryKind(raw string) MemoryKind {
	switch kind := MemoryKind(strings.ToLower(strings.TrimSpace(raw))); kind {
	case MemoryKindFact, MemoryKindEpisode, MemoryKindPreference, MemoryKindConstraint, MemoryKindGoal:
		return kind
	default:
		return ""
	}
}

func NormalizeMemoryClaim(raw RawMemoryClaim, selfID int64) (MemoryClaim, error) {
	if raw.SubjectUserID == nil {
		return MemoryClaim{}, claimError("invalid_subject", "subject_user_id 必填")
	}
	subjectID := *raw.SubjectUserID
	switch {
	case subjectID == SubjectSelfInputID:
		if selfID <= 0 {
			return MemoryClaim{}, claimError("self_id_unavailable", "机器人账号尚未就绪")
		}
		subjectID = selfID
	case subjectID < SubjectSelfInputID:
		return MemoryClaim{}, claimError("invalid_subject", "subject_user_id 不能小于 -1")
	}
	kind := NormalizeMemoryKind(raw.Kind)
	if kind == "" {
		return MemoryClaim{}, claimError("invalid_kind", "kind 必须是 fact、episode、preference、constraint 或 goal")
	}
	content := strings.TrimSpace(raw.Content)
	if content == "" || utf8.RuneCountInString(content) > 500 {
		return MemoryClaim{}, claimError("invalid_content", "content 必须为 1 到 500 个字符")
	}
	evidence := uniqueNonzeroInt64(raw.EvidenceMessageIDs)
	if len(evidence) == 0 {
		return MemoryClaim{}, claimError("missing_evidence", "evidence_message_ids 必须包含至少一条消息")
	}
	if len(evidence) > 8 {
		return MemoryClaim{}, claimError("invalid_evidence", "evidence_message_ids 最多 8 条")
	}
	return MemoryClaim{SubjectUserID: subjectID, Kind: kind, Content: content, EvidenceMessageIDs: evidence}, nil
}

func uniqueNonzeroInt64(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (m *Manager) NormalizeAndValidateClaims(ctx context.Context, storeCtx StoreClaimsContext, raw []RawMemoryClaim) ([]MemoryClaim, error) {
	if len(raw) == 0 {
		return []MemoryClaim{}, nil
	}
	claims := make([]MemoryClaim, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		claim, err := NormalizeMemoryClaim(item, storeCtx.SelfID)
		if err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%d|%s|%s", claim.SubjectUserID, claim.Kind, NormalizeContent(claim.Content))
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		claims = append(claims, claim)
	}
	if err := m.validateClaimEvidence(ctx, storeCtx, claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func (m *Manager) validateClaimEvidence(ctx context.Context, storeCtx StoreClaimsContext, claims []MemoryClaim) error {
	if storeCtx.GroupID <= 0 {
		return claimError("invalid_evidence", "当前群无效")
	}
	allIDs := make([]int64, 0)
	for _, claim := range claims {
		allIDs = append(allIDs, claim.EvidenceMessageIDs...)
	}
	allIDs = uniqueNonzeroInt64(allIDs)
	if len(allIDs) == 0 && len(claims) > 0 {
		return claimError("missing_evidence", "缺少消息证据")
	}

	var snapshotID uint
	if storeCtx.SnapshotOneBotMessageID != 0 {
		if err := m.db.WithContext(ctx).Model(&MessageLog{}).
			Where("group_id = ? AND one_bot_message_id = ?", storeCtx.GroupID, storeCtx.SnapshotOneBotMessageID).
			Pluck("id", &snapshotID).Error; err != nil {
			return err
		}
		if snapshotID == 0 {
			return claimError("outside_snapshot", "找不到固定消息快照上界")
		}
	}

	var evidence []MessageLog
	query := m.db.WithContext(ctx).Where("group_id = ? AND one_bot_message_id IN ? AND recalled_at IS NULL", storeCtx.GroupID, allIDs)
	if snapshotID > 0 {
		query = query.Where("id <= ?", snapshotID)
	}
	if err := query.Find(&evidence).Error; err != nil {
		return err
	}
	if len(evidence) != len(allIDs) {
		return claimError("invalid_evidence", "证据消息不存在、已撤回、跨群或晚于固定快照")
	}

	if storeCtx.TopicID > 0 {
		var count int64
		if err := m.db.WithContext(ctx).Table("topic_assignments ta").
			Joins("JOIN message_logs ml ON ml.id = ta.message_log_id").
			Where("ta.topic_id = ? AND ta.id <= ? AND ml.id IN ?", storeCtx.TopicID, storeCtx.ThroughAssignmentID, messageLogIDs(evidence)).
			Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(evidence)) {
			return claimError("invalid_evidence", "摘要证据不属于当前话题上界")
		}
	}

	allowedSubjects, err := m.allowedSubjectIDs(ctx, storeCtx.GroupID, evidence)
	if err != nil {
		return err
	}
	for _, claim := range claims {
		if claim.SubjectUserID == 0 || claim.SubjectUserID == storeCtx.SelfID {
			continue
		}
		if _, ok := allowedSubjects[claim.SubjectUserID]; !ok {
			return claimError("invalid_subject", fmt.Sprintf("主体 %d 未出现在证据消息作者或回复目标中", claim.SubjectUserID))
		}
	}
	return nil
}

func (m *Manager) allowedSubjectIDs(ctx context.Context, groupID int64, evidence []MessageLog) (map[int64]struct{}, error) {
	allowed := make(map[int64]struct{}, len(evidence))
	replies := make([]int64, 0)
	for _, row := range evidence {
		if row.UserID > 0 {
			allowed[row.UserID] = struct{}{}
		}
		if row.ReplyToMessageID != nil && *row.ReplyToMessageID != 0 {
			replies = append(replies, *row.ReplyToMessageID)
		}
	}
	if len(replies) > 0 {
		var replyAuthors []int64
		if err := m.db.WithContext(ctx).Model(&MessageLog{}).Where("group_id=? AND one_bot_message_id IN ?", groupID, uniqueNonzeroInt64(replies)).Pluck("user_id", &replyAuthors).Error; err != nil {
			return nil, err
		}
		for _, id := range replyAuthors {
			if id > 0 {
				allowed[id] = struct{}{}
			}
		}
	}
	return allowed, nil
}

func messageLogIDs(rows []MessageLog) []uint {
	ids := make([]uint, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}

func (m *Manager) StoreClaims(ctx context.Context, storeCtx StoreClaimsContext, claims []MemoryClaim) ([]StoreClaimResult, error) {
	if len(claims) == 0 {
		return []StoreClaimResult{}, nil
	}
	if err := m.validateClaimEvidence(ctx, storeCtx, claims); err != nil {
		return nil, err
	}
	prepared := make([]preparedMemory, 0, len(claims))
	for _, claim := range claims {
		item, err := m.prepareMemory(ctx, storeCtx.GroupID, claim)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, item)
	}
	results := make([]StoreClaimResult, 0, len(prepared))
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range prepared {
			var evidence []MessageLog
			result := tx.Model(&MessageLog{}).Clauses(clause.Locking{Strength: "UPDATE"}).
				Select("id").Where("id IN ? AND recalled_at IS NULL", item.evidence).Find(&evidence)
			if result.Error != nil {
				return result.Error
			}
			if len(evidence) != len(item.evidence) {
				return claimError("invalid_evidence", "证据消息在提交前已撤回")
			}
			memoryRow, action, err := applyPreparedMemory(tx, item)
			if err != nil {
				return err
			}
			results = append(results, StoreClaimResult{Memory: *memoryRow, Action: action})
		}
		return nil
	})
	return results, err
}

func (m *Manager) prepareMemory(ctx context.Context, groupID int64, claim MemoryClaim) (preparedMemory, error) {
	embedding, err := m.embedding.Embed(ctx, claim.Content)
	if err != nil {
		return preparedMemory{}, err
	}
	vector, err := EmbeddingVector(embedding)
	if err != nil {
		return preparedMemory{}, err
	}
	incoming := Memory{GroupID: groupID, SubjectUserID: claim.SubjectUserID, Kind: claim.Kind, Status: MemoryStatusActive, Content: claim.Content, Embedding: vector}
	exact, err := m.exactMemoryCandidate(ctx, incoming)
	if err != nil {
		return preparedMemory{}, err
	}
	if exact != nil {
		evidence, err := evidenceInternalIDs(ctx, m.db, groupID, claim.EvidenceMessageIDs)
		if err != nil {
			return preparedMemory{}, err
		}
		return preparedMemory{incoming: incoming, decision: memoryMergeDecision{ShouldMerge: true, TargetID: exact.ID, MergeIDs: []uint{exact.ID}, MergedContent: incoming.Content}, evidence: evidence}, nil
	}
	candidates, err := m.semanticMergeCandidates(ctx, incoming)
	if err != nil {
		return preparedMemory{}, err
	}
	decision, err := m.decideMemoryMerge(ctx, memoryMergeInput{Incoming: incoming, Candidates: candidates})
	if err != nil {
		return preparedMemory{}, err
	}
	if decision.ShouldMerge && decision.MergedContent != "" && decision.MergedContent != incoming.Content {
		mergedEmbedding, err := m.embedding.Embed(ctx, decision.MergedContent)
		if err != nil {
			return preparedMemory{}, err
		}
		incoming.Content = decision.MergedContent
		incoming.Embedding, err = EmbeddingVector(mergedEmbedding)
		if err != nil {
			return preparedMemory{}, err
		}
	}
	evidence, err := evidenceInternalIDs(ctx, m.db, groupID, claim.EvidenceMessageIDs)
	if err != nil {
		return preparedMemory{}, err
	}
	return preparedMemory{incoming: incoming, decision: decision, evidence: evidence}, nil
}

func evidenceInternalIDs(ctx context.Context, db *gorm.DB, groupID int64, oneBotIDs []int64) ([]uint, error) {
	var ids []uint
	err := db.WithContext(ctx).Model(&MessageLog{}).Where("group_id=? AND one_bot_message_id IN ?", groupID, oneBotIDs).Order("id ASC").Pluck("id", &ids).Error
	if err == nil && len(ids) != len(oneBotIDs) {
		return nil, claimError("invalid_evidence", "证据消息在准备写入时已不存在")
	}
	return ids, err
}

func (m *Manager) exactMemoryCandidate(ctx context.Context, incoming Memory) (*Memory, error) {
	var row Memory
	err := m.db.WithContext(ctx).Where("group_id=? AND subject_user_id=? AND kind=? AND status=? AND lower(btrim(content))=lower(btrim(?))",
		incoming.GroupID, incoming.SubjectUserID, incoming.Kind, MemoryStatusActive, incoming.Content).Order("id ASC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}

func (m *Manager) semanticMergeCandidates(ctx context.Context, incoming Memory) ([]Memory, error) {
	var rows []Memory
	err := m.db.WithContext(ctx).Where("group_id=? AND subject_user_id=? AND status=? AND 1-(embedding <=> ?) >= ?",
		incoming.GroupID, incoming.SubjectUserID, MemoryStatusActive, incoming.Embedding, activeContextVectorThreshold).
		Order(clause.Expr{SQL: "embedding <=> ?", Vars: []any{incoming.Embedding}}).Limit(8).Find(&rows).Error
	return rows, err
}

func applyPreparedMemory(tx *gorm.DB, prepared preparedMemory) (*Memory, string, error) {
	if prepared.decision.ShouldMerge {
		if strings.TrimSpace(prepared.decision.MergedContent) == "" {
			return nil, "", errors.New("长期记忆合并结果缺少完整内容")
		}
		return applyMemoryMerge(tx, prepared)
	}
	incoming := prepared.incoming
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&incoming)
	if result.Error != nil {
		return nil, "", result.Error
	}
	action := "saved"
	if result.RowsAffected == 0 {
		if err := tx.Where("group_id=? AND subject_user_id=? AND kind=? AND status=? AND lower(btrim(content))=lower(btrim(?))",
			incoming.GroupID, incoming.SubjectUserID, incoming.Kind, MemoryStatusActive, incoming.Content).First(&incoming).Error; err != nil {
			return nil, "", err
		}
		action = "deduplicated"
	}
	if err := createEvidence(tx, incoming.ID, prepared.evidence); err != nil {
		return nil, "", err
	}
	return &incoming, action, nil
}

func applyMemoryMerge(tx *gorm.DB, prepared preparedMemory) (*Memory, string, error) {
	ids := prepared.decision.MergeIDs
	if len(ids) == 0 {
		ids = []uint{prepared.decision.TargetID}
	}
	var locked []Memory
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ? AND group_id=? AND subject_user_id=? AND status=?",
		ids, prepared.incoming.GroupID, prepared.incoming.SubjectUserID, MemoryStatusActive).Find(&locked).Error; err != nil {
		return nil, "", err
	}
	if len(locked) == 0 {
		return nil, "", gorm.ErrRecordNotFound
	}
	targetID := prepared.decision.TargetID
	var exact Memory
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("group_id=? AND subject_user_id=? AND kind=? AND status=? AND lower(btrim(content))=lower(btrim(?))",
		prepared.incoming.GroupID, prepared.incoming.SubjectUserID, prepared.incoming.Kind, MemoryStatusActive, prepared.incoming.Content).First(&exact).Error
	if err == nil {
		targetID = exact.ID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", err
	}
	for _, item := range locked {
		if item.ID == targetID {
			continue
		}
		if err := tx.Exec(`INSERT INTO memory_evidence(memory_id,message_log_id) SELECT ?,message_log_id FROM memory_evidence WHERE memory_id=? ON CONFLICT DO NOTHING`, targetID, item.ID).Error; err != nil {
			return nil, "", err
		}
		if err := tx.Delete(&Memory{}, item.ID).Error; err != nil {
			return nil, "", err
		}
	}
	updates := map[string]any{"kind": prepared.incoming.Kind, "content": prepared.incoming.Content, "embedding": prepared.incoming.Embedding, "status": MemoryStatusActive}
	if err := tx.Model(&Memory{}).Where("id=?", targetID).Updates(updates).Error; err != nil {
		return nil, "", err
	}
	if err := createEvidence(tx, targetID, prepared.evidence); err != nil {
		return nil, "", err
	}
	var target Memory
	if err := tx.First(&target, targetID).Error; err != nil {
		return nil, "", err
	}
	return &target, "merged", nil
}

func createEvidence(tx *gorm.DB, memoryID uint, messageLogIDs []uint) error {
	if len(messageLogIDs) == 0 {
		return claimError("missing_evidence", "长期记忆缺少消息证据")
	}
	rows := make([]MemoryEvidence, 0, len(messageLogIDs))
	for _, id := range messageLogIDs {
		rows = append(rows, MemoryEvidence{MemoryID: memoryID, MessageLogID: id})
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}
