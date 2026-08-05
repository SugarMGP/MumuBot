package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type preparedMemory struct {
	incoming Memory
	decision memoryMergeDecision
}

func (m *Manager) IngestMemory(ctx context.Context, input MemoryIngestInput) (*Memory, string, error) {
	prepared, err := m.prepareMemory(ctx, input)
	if err != nil || prepared == nil {
		return nil, "ignored", err
	}
	var result *Memory
	var action string
	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if input.MessageLogID != nil {
			var source MessageLog
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").
				Where("id = ? AND recalled_at IS NULL", *input.MessageLogID).Take(&source).Error; err != nil {
				return err
			}
		}
		var applyErr error
		result, action, applyErr = applyPreparedMemory(tx, input, *prepared)
		return applyErr
	})
	return result, action, err
}

func (m *Manager) prepareMemory(ctx context.Context, input MemoryIngestInput) (*preparedMemory, error) {
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return nil, nil
	}
	if (input.MessageLogID == nil) == (input.TopicSummaryID == nil) {
		return nil, errors.New("长期记忆必须且只能关联一个证据来源")
	}
	claim, err := m.extractNormalizedClaim(ctx, input, content)
	if err != nil {
		return nil, err
	}
	if claim.Ignored || claim.Kind == "" || claim.ValueSummary == "" {
		return nil, nil
	}
	var subjectUserID *int64
	if claim.Scope == MemoryScopeMember {
		id := input.RelatedUserID
		if id == 0 {
			id = resolveSubjectCandidateUserID(claim.SubjectName, input.SubjectCandidates)
		}
		if id == 0 {
			return nil, nil
		}
		subjectUserID = &id
	}
	embedding, err := m.embedding.Embed(ctx, claim.ValueSummary)
	if err != nil {
		return nil, err
	}
	vector, err := EmbeddingVector(embedding)
	if err != nil {
		return nil, err
	}
	incoming := Memory{
		GroupID: input.GroupID, SubjectUserID: subjectUserID, Scope: claim.Scope,
		Kind: claim.Kind, Status: MemoryStatusActive, Content: claim.ValueSummary,
		Embedding: vector,
	}
	exact, err := m.exactMemoryCandidate(ctx, incoming)
	if err != nil {
		return nil, err
	}
	if exact != nil {
		return &preparedMemory{incoming: incoming, decision: memoryMergeDecision{
			ShouldMerge: true, TargetID: exact.ID, MergeIDs: []uint{exact.ID}, MergedContent: incoming.Content,
		}}, nil
	}
	candidates, err := m.semanticMergeCandidates(ctx, incoming)
	if err != nil {
		return nil, err
	}
	decision, err := m.decideMemoryMerge(ctx, memoryMergeInput{Incoming: incoming, Candidates: candidates})
	if err != nil {
		return nil, err
	}
	if decision.ShouldMerge && decision.MergedContent != "" {
		mergedEmbedding, err := m.embedding.Embed(ctx, decision.MergedContent)
		if err != nil {
			return nil, err
		}
		incoming.Content = decision.MergedContent
		incoming.Embedding, err = EmbeddingVector(mergedEmbedding)
		if err != nil {
			return nil, err
		}
	}
	return &preparedMemory{incoming: incoming, decision: decision}, nil
}

func (m *Manager) exactMemoryCandidate(ctx context.Context, incoming Memory) (*Memory, error) {
	query := m.db.WithContext(ctx).Where(
		"group_id = ? AND scope = ? AND kind = ? AND status IN ? AND lower(btrim(content)) = lower(btrim(?))",
		incoming.GroupID, incoming.Scope, incoming.Kind, []MemoryStatus{MemoryStatusActive, MemoryStatusCandidate}, incoming.Content,
	)
	if incoming.SubjectUserID == nil {
		query = query.Where("subject_user_id IS NULL")
	} else {
		query = query.Where("subject_user_id = ?", *incoming.SubjectUserID)
	}
	var row Memory
	if err := query.Order("CASE WHEN status = 'active' THEN 0 ELSE 1 END, id ASC").First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (m *Manager) semanticMergeCandidates(ctx context.Context, incoming Memory) ([]Memory, error) {
	query := m.db.WithContext(ctx).Where(
		"group_id = ? AND status IN ? AND 1 - (embedding <=> ?) >= ?",
		incoming.GroupID, []MemoryStatus{MemoryStatusActive, MemoryStatusCandidate}, incoming.Embedding, activeContextVectorThreshold,
	)
	var rows []Memory
	if err := query.Order(clause.Expr{SQL: "embedding <=> ?", Vars: []any{incoming.Embedding}}).Limit(8).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func applyPreparedMemory(tx *gorm.DB, input MemoryIngestInput, prepared preparedMemory) (*Memory, string, error) {
	if prepared.decision.ShouldMerge {
		if strings.TrimSpace(prepared.decision.MergedContent) == "" {
			return nil, "", errors.New("长期记忆合并结果缺少完整内容")
		}
		return applyMemoryMerge(tx, input, prepared)
	}
	incoming := prepared.incoming
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&incoming)
	if result.Error != nil {
		return nil, "", result.Error
	}
	action := "created"
	if result.RowsAffected == 0 {
		query := tx.Where("group_id = ? AND scope = ? AND kind = ? AND status = ? AND lower(btrim(content)) = lower(btrim(?))",
			incoming.GroupID, incoming.Scope, incoming.Kind, MemoryStatusActive, incoming.Content)
		if incoming.SubjectUserID == nil {
			query = query.Where("subject_user_id IS NULL")
		} else {
			query = query.Where("subject_user_id = ?", *incoming.SubjectUserID)
		}
		if err := query.First(&incoming).Error; err != nil {
			return nil, "", err
		}
		action = "deduplicated"
	}
	if err := createEvidence(tx, incoming.ID, input); err != nil {
		return nil, "", err
	}
	return &incoming, action, nil
}

func applyMemoryMerge(tx *gorm.DB, input MemoryIngestInput, prepared preparedMemory) (*Memory, string, error) {
	ids := append([]uint(nil), prepared.decision.MergeIDs...)
	if len(ids) == 0 {
		ids = []uint{prepared.decision.TargetID}
	}
	var locked []Memory
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"id IN ? AND group_id = ? AND status IN ?", ids, prepared.incoming.GroupID,
		[]MemoryStatus{MemoryStatusActive, MemoryStatusCandidate},
	).Find(&locked).Error; err != nil {
		return nil, "", err
	}
	if len(locked) == 0 {
		return nil, "", gorm.ErrRecordNotFound
	}
	targetID := prepared.decision.TargetID
	foundTarget := false
	for _, item := range locked {
		if item.ID == targetID {
			foundTarget = true
			break
		}
	}
	if !foundTarget {
		return nil, "", fmt.Errorf("长期记忆合并目标不存在: %d", targetID)
	}
	exactQuery := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"group_id = ? AND scope = ? AND kind = ? AND status = ? AND lower(btrim(content)) = lower(btrim(?))",
		prepared.incoming.GroupID, prepared.incoming.Scope, prepared.incoming.Kind, MemoryStatusActive, prepared.incoming.Content,
	)
	if prepared.incoming.SubjectUserID == nil {
		exactQuery = exactQuery.Where("subject_user_id IS NULL")
	} else {
		exactQuery = exactQuery.Where("subject_user_id = ?", *prepared.incoming.SubjectUserID)
	}
	var exact Memory
	if err := exactQuery.First(&exact).Error; err == nil {
		present := false
		for _, item := range locked {
			present = present || item.ID == exact.ID
		}
		if !present {
			locked = append(locked, exact)
		}
		targetID = exact.ID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", err
	}
	for _, item := range locked {
		if item.ID == targetID {
			continue
		}
		if err := tx.Exec(`INSERT INTO memory_evidence (memory_id, message_log_id, topic_summary_id)
			SELECT ?, message_log_id, topic_summary_id FROM memory_evidence WHERE memory_id = ? ON CONFLICT DO NOTHING`, targetID, item.ID).Error; err != nil {
			return nil, "", err
		}
		if err := tx.Delete(&Memory{}, item.ID).Error; err != nil {
			return nil, "", err
		}
	}
	updates := map[string]any{
		"scope":           prepared.incoming.Scope,
		"subject_user_id": prepared.incoming.SubjectUserID,
		"kind":            prepared.incoming.Kind,
		"content":         prepared.incoming.Content,
		"embedding":       prepared.incoming.Embedding,
		"status":          MemoryStatusActive,
	}
	if err := tx.Model(&Memory{}).Where("id = ?", targetID).Updates(updates).Error; err != nil {
		return nil, "", err
	}
	if err := createEvidence(tx, targetID, input); err != nil {
		return nil, "", err
	}
	var target Memory
	if err := tx.First(&target, targetID).Error; err != nil {
		return nil, "", err
	}
	return &target, "merged", nil
}
