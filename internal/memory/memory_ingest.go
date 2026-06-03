package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

func (m *Manager) IngestMemory(ctx context.Context, input MemoryIngestInput) (*Memory, string, error) {
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return nil, "ignored", nil
	}
	if input.SourceKind == "" {
		input.SourceKind = MemorySourceKindMessage
	}
	if input.SourceRef == "" && input.SourceKind == MemorySourceKindMessage {
		return nil, "", fmt.Errorf("消息来源缺少 source_ref")
	}

	claim := m.extractNormalizedClaim(ctx, input, content)
	if claim.CanonicalType == "" {
		if !claim.Ignored {
			zap.L().Warn("长期记忆候选被忽略：未提取到有效 claim",
				zap.String("source_kind", string(input.SourceKind)),
				zap.String("source_ref", input.SourceRef))
		}
		return nil, "ignored", nil
	}
	if len(input.AllowedCanonicalTypes) > 0 && !containsCanonicalType(input.AllowedCanonicalTypes, claim.CanonicalType) {
		zap.L().Warn("长期记忆候选被忽略：类型不在允许范围内",
			zap.String("canonical_type", string(claim.CanonicalType)),
			zap.String("source_kind", string(input.SourceKind)),
			zap.String("source_ref", input.SourceRef))
		return nil, "ignored", nil
	}
	relatedUserID := input.RelatedUserID
	if relatedUserID == 0 {
		relatedUserID = resolveSubjectCandidateUserID(claim.SubjectName, input.SubjectCandidates)
	}
	subjectClass := claim.SubjectClass

	if claim.CanonicalType == CanonicalMemoryTypeGoal && !claim.LongTerm {
		return nil, "ignored", nil
	}

	status := MemoryStatusActive
	if input.SourceKind == MemorySourceKindMigration {
		status = MemoryStatusLegacy
	}
	memType := oldMemoryTypeFromSubject(subjectClass)
	factKey := ""
	if claim.CanonicalType != CanonicalMemoryTypeEpisode {
		factKey = BuildFactKey(content)
	}

	if claim.CanonicalType == CanonicalMemoryTypeEpisode {
		mem := &Memory{
			Type:          memType,
			GroupID:       input.GroupID,
			UserID:        relatedUserID,
			Content:       content,
			CanonicalType: claim.CanonicalType,
			Status:        status,
			EvidenceCount: 1,
			SourceKind:    input.SourceKind,
			SourceRef:     input.SourceRef,
			Importance:    importanceForStatus(claim.CanonicalType, status, 1),
		}
		return m.saveMemoryWithMerge(ctx, input, mem)
	}

	mem := &Memory{
		Type:          memType,
		GroupID:       input.GroupID,
		UserID:        relatedUserID,
		Content:       content,
		CanonicalType: claim.CanonicalType,
		Status:        status,
		EvidenceCount: 1,
		SourceKind:    input.SourceKind,
		SourceRef:     input.SourceRef,
		FactKey:       factKey,
		Importance:    importanceForStatus(claim.CanonicalType, status, 1),
	}
	return m.saveMemoryWithMerge(ctx, input, mem)
}

func (m *Manager) saveMemoryWithMerge(ctx context.Context, input MemoryIngestInput, incoming *Memory) (*Memory, string, error) {
	if incoming == nil {
		return nil, "ignored", nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if exact, action, ok, err := m.findExactMemory(ctx, input, *incoming); err != nil || ok {
		return exact, action, err
	}

	if shouldSemanticMerge(incoming.CanonicalType) {
		candidates, err := m.findSemanticMergeCandidates(ctx, *incoming)
		if err != nil {
			return nil, "", err
		}
		if len(candidates) > 0 {
			decision, err := m.decideMemoryMerge(ctx, memoryMergeInput{
				Incoming:   *incoming,
				Candidates: candidates,
			})
			if err != nil {
				return nil, "", err
			}
			if decision.ShouldMerge {
				mem, err := m.applyMemoryMerge(ctx, input, *incoming, candidates, decision)
				return mem, "merged", err
			}
		}
	}

	mem, err := m.createMemoryWithVector(ctx, incoming)
	if err != nil {
		return nil, "", err
	}
	return mem, "created", nil
}

func (m *Manager) findExactMemory(ctx context.Context, input MemoryIngestInput, incoming Memory) (*Memory, string, bool, error) {
	var existing Memory
	var ok bool
	var err error
	if incoming.CanonicalType == CanonicalMemoryTypeEpisode {
		existing, ok, err = m.findFirstCompatibleMemory(ctx, incoming, m.db.WithContext(ctx).
			Where("group_id = ? AND type = ? AND canonical_type = ? AND source_kind = ? AND source_ref = ? AND content = ? AND (status <> ? OR status = '' OR status IS NULL)",
				incoming.GroupID, incoming.Type, incoming.CanonicalType, incoming.SourceKind, incoming.SourceRef, incoming.Content, MemoryStatusArchived))
	} else if incoming.FactKey != "" {
		existing, ok, err = m.findFirstCompatibleMemory(ctx, incoming, m.db.WithContext(ctx).
			Where("group_id = ? AND type = ? AND canonical_type = ? AND fact_key = ? AND (status <> ? OR status = '' OR status IS NULL)",
				incoming.GroupID, incoming.Type, incoming.CanonicalType, incoming.FactKey, MemoryStatusArchived))
	} else {
		existing, ok, err = m.findFirstCompatibleMemory(ctx, incoming, m.db.WithContext(ctx).
			Where("group_id = ? AND type = ? AND canonical_type = ? AND content = ? AND (status <> ? OR status = '' OR status IS NULL)",
				incoming.GroupID, incoming.Type, incoming.CanonicalType, incoming.Content, MemoryStatusArchived))
	}
	if err != nil {
		return nil, "", false, err
	}
	if !ok {
		if incoming.CanonicalType != CanonicalMemoryTypeEpisode {
			if legacy, ok, legacyErr := m.findExactMemoryByContent(ctx, incoming); legacyErr != nil || ok {
				if legacyErr != nil {
					return nil, "", false, legacyErr
				}
				mem, action, reinforceErr := m.reinforceExistingMemory(ctx, legacy, input)
				return mem, action, true, reinforceErr
			}
		}
		return nil, "", false, nil
	}
	mem, action, err := m.reinforceExistingMemory(ctx, existing, input)
	return mem, action, true, err
}

func (m *Manager) findFirstCompatibleMemory(ctx context.Context, incoming Memory, query *gorm.DB) (Memory, bool, error) {
	var candidates []Memory
	if err := query.WithContext(ctx).Order("id DESC").Find(&candidates).Error; err != nil {
		return Memory{}, false, err
	}
	for _, candidate := range candidates {
		if memorySubjectCompatible(incoming, candidate) {
			return candidate, true, nil
		}
	}
	return Memory{}, false, nil
}

func (m *Manager) findExactMemoryByContent(ctx context.Context, incoming Memory) (Memory, bool, error) {
	normalized := NormalizeContentForKey(incoming.Content)
	if normalized == "" {
		return Memory{}, false, nil
	}
	var candidates []Memory
	if err := m.db.WithContext(ctx).
		Where("group_id = ? AND type = ? AND canonical_type = ? AND content = ? AND (status <> ? OR status = '' OR status IS NULL)",
			incoming.GroupID, incoming.Type, incoming.CanonicalType, incoming.Content, MemoryStatusArchived).
		Order("id DESC").
		Find(&candidates).Error; err != nil {
		return Memory{}, false, err
	}
	for _, candidate := range candidates {
		if memorySubjectCompatible(incoming, candidate) {
			return candidate, true, nil
		}
	}
	var lastID uint
	const batchSize = 100
	for {
		candidates = candidates[:0]
		query := m.db.WithContext(ctx).
			Where("group_id = ? AND type = ? AND canonical_type = ? AND (status <> ? OR status = '' OR status IS NULL)",
				incoming.GroupID, incoming.Type, incoming.CanonicalType, MemoryStatusArchived).
			Order("id DESC").
			Limit(batchSize)
		if lastID > 0 {
			query = query.Where("id < ?", lastID)
		}
		if err := query.Find(&candidates).Error; err != nil {
			return Memory{}, false, err
		}
		if len(candidates) == 0 {
			break
		}
		for _, candidate := range candidates {
			if !memorySubjectCompatible(incoming, candidate) {
				continue
			}
			if NormalizeContentForKey(candidate.Content) == normalized {
				return candidate, true, nil
			}
		}
		lastID = candidates[len(candidates)-1].ID
	}
	return Memory{}, false, nil
}

func (m *Manager) reinforceExistingMemory(ctx context.Context, existing Memory, input MemoryIngestInput) (*Memory, string, error) {
	now := time.Now()
	action := "deduplicated"
	nextEvidenceCount := existing.EvidenceCount
	if nextEvidenceCount <= 0 {
		nextEvidenceCount = 1
	}
	if !sameEvidenceSource(existing.SourceKind, existing.SourceRef, input.SourceKind, input.SourceRef) {
		action = "reinforced"
		nextEvidenceCount++
	}
	nextStatus := existing.EffectiveStatus()
	if input.SourceKind != MemorySourceKindMigration && nextStatus != MemoryStatusArchived {
		nextStatus = MemoryStatusActive
	}
	if nextStatus == MemoryStatusLegacy {
		nextStatus = MemoryStatusActive
	}
	mem, err := m.updateMemoryFields(ctx, existing.ID, func(mem *Memory) {
		mem.Status = nextStatus
		mem.EvidenceCount = nextEvidenceCount
		mem.Importance = importanceForStatus(existing.CanonicalType, nextStatus, nextEvidenceCount)
		mem.UpdatedAt = now
		if existing.CanonicalType != CanonicalMemoryTypeEpisode {
			mem.FactKey = BuildFactKey(existing.Content)
		}
		if action == "reinforced" {
			mem.SourceKind = input.SourceKind
			mem.SourceRef = input.SourceRef
		}
	})
	return mem, action, err
}

func (m *Manager) createMemoryWithVector(ctx context.Context, mem *Memory) (*Memory, error) {
	if mem == nil {
		return nil, nil
	}
	if mem.EvidenceCount <= 0 {
		mem.EvidenceCount = 1
	}
	if mem.Status == "" {
		mem.Status = MemoryStatusActive
	}
	if mem.Importance <= 0 {
		mem.Importance = importanceForStatus(mem.CanonicalType, mem.Status, mem.EvidenceCount)
	}
	prepared, err := m.prepareMemoryVector(ctx, *mem)
	if err != nil {
		return nil, err
	}
	var created Memory
	if err := m.db.WithContext(ctx).Create(mem).Error; err != nil {
		return nil, err
	}
	created = *mem
	if prepared != nil {
		prepared.memory = created
	}
	if err := m.insertPreparedMemoryVector(ctx, prepared); err != nil {
		if err := m.db.WithContext(ctx).Delete(&Memory{}, created.ID).Error; err != nil {
			zap.L().Warn("补偿删除长期记忆行失败", zap.Uint("memory_id", created.ID), zap.Error(err))
		}
		if err := m.deleteMemoryVector(ctx, created.ID); err != nil {
			zap.L().Warn("补偿删除长期记忆向量失败", zap.Uint("memory_id", created.ID), zap.Error(err))
		}
		return nil, err
	}
	return &created, nil
}

func (m *Manager) applyMemoryMerge(ctx context.Context, input MemoryIngestInput, incoming Memory, candidates []Memory, decision memoryMergeDecision) (*Memory, error) {
	targetID := decision.TargetID
	if targetID == 0 && len(candidates) > 0 {
		targetID = candidates[0].ID
	}
	if targetID == 0 {
		return nil, fmt.Errorf("语义合并缺少目标记忆")
	}
	candidateByID := make(map[uint]Memory, len(candidates))
	for _, candidate := range candidates {
		candidateByID[candidate.ID] = candidate
	}
	target, ok := candidateByID[targetID]
	if !ok {
		return nil, fmt.Errorf("语义合并目标不在候选列表中")
	}

	mergeIDs := utils.UniqueIDs(append(decision.MergeIDs, targetID))
	content := strings.TrimSpace(decision.MergedContent)
	if content == "" {
		content = incoming.Content
	}
	now := time.Now()
	nextEvidenceCount := target.EvidenceCount
	if nextEvidenceCount <= 0 {
		nextEvidenceCount = 1
	}
	incomingAddsEvidence := !sameEvidenceSource(target.SourceKind, target.SourceRef, input.SourceKind, input.SourceRef)
	if incomingAddsEvidence {
		nextEvidenceCount++
	}
	deletedCandidates := make([]Memory, 0, len(mergeIDs))
	for _, id := range mergeIDs {
		if id == targetID {
			continue
		}
		if candidate, ok := candidateByID[id]; ok {
			nextEvidenceCount += max(candidate.EvidenceCount, 1)
			deletedCandidates = append(deletedCandidates, candidate)
		}
	}
	nextStatus := target.EffectiveStatus()
	if nextStatus == MemoryStatusLegacy {
		nextStatus = MemoryStatusActive
	}
	nextTarget := target
	nextTarget.Content = content
	nextTarget.Status = nextStatus
	nextTarget.FactKey = BuildFactKey(content)
	nextTarget.EvidenceCount = nextEvidenceCount
	nextTarget.Importance = importanceForStatus(target.CanonicalType, nextStatus, nextEvidenceCount)
	nextTarget.UpdatedAt = now
	if incomingAddsEvidence {
		nextTarget.SourceKind = input.SourceKind
		nextTarget.SourceRef = input.SourceRef
	}
	prepared, err := m.prepareMemoryVector(ctx, nextTarget)
	if err != nil {
		return nil, err
	}
	var updated Memory
	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&nextTarget).Error; err != nil {
			return err
		}
		archiveIDs := make([]uint, 0, len(mergeIDs))
		for _, id := range mergeIDs {
			if id != targetID {
				archiveIDs = append(archiveIDs, id)
			}
		}
		if len(archiveIDs) > 0 {
			if err := tx.Where("id IN ?", archiveIDs).Delete(&Memory{}).Error; err != nil {
				return err
			}
		}
		updated = nextTarget
		return nil
	})
	if err != nil {
		return nil, err
	}
	if prepared != nil {
		prepared.memory = updated
	}
	if err := m.replaceMergedMemoryVectors(ctx, prepared, mergeIDs); err != nil {
		m.restoreExistingMemoryRowBestEffort(ctx, target)
		m.restoreDeletedMemoryRowsBestEffort(ctx, deletedCandidates...)
		oldVectors := make([]Memory, 0, 1+len(deletedCandidates))
		oldVectors = append(oldVectors, target)
		oldVectors = append(oldVectors, deletedCandidates...)
		m.restoreMemoryVectorsBestEffort(ctx, oldVectors...)
		return nil, err
	}
	return &updated, nil
}

func (m *Manager) findSemanticMergeCandidates(ctx context.Context, incoming Memory) ([]Memory, error) {
	if !shouldSemanticMerge(incoming.CanonicalType) || m.embedding == nil || m.milvus == nil {
		return nil, nil
	}
	embedding, err := m.embedding.Embed(ctx, incoming.Content)
	if err != nil {
		return nil, err
	}
	results, err := m.milvus.Search(ctx, embedding, incoming.GroupID, string(incoming.Type), 8, 0.9)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	ids := make([]uint, 0, len(results))
	for _, result := range results {
		if result.MemoryID != 0 {
			ids = append(ids, result.MemoryID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var memories []Memory
	if err := m.db.WithContext(ctx).
		Where("id IN ? AND group_id = ? AND type = ? AND canonical_type = ? AND (status = ? OR status = ? OR status = '')",
			ids, incoming.GroupID, incoming.Type, incoming.CanonicalType, MemoryStatusActive, MemoryStatusLegacy).
		Find(&memories).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]Memory, len(memories))
	for _, mem := range memories {
		byID[mem.ID] = mem
	}
	ordered := make([]Memory, 0, len(memories))
	for _, result := range results {
		if mem, ok := byID[result.MemoryID]; ok {
			if !memorySubjectCompatible(incoming, mem) {
				continue
			}
			ordered = append(ordered, mem)
		}
	}
	return ordered, nil
}

func memorySubjectCompatible(incoming Memory, candidate Memory) bool {
	if incoming.Type != candidate.Type {
		return false
	}
	switch incoming.Type {
	case MemoryTypeConversation, MemoryTypeSelfExperience:
		if incoming.UserID > 0 && candidate.UserID > 0 && incoming.UserID != candidate.UserID {
			return false
		}
	}
	return true
}

func shouldSemanticMerge(kind CanonicalMemoryType) bool {
	switch kind {
	case CanonicalMemoryTypeFact, CanonicalMemoryTypePreference, CanonicalMemoryTypeConstraint, CanonicalMemoryTypeGoal:
		return true
	default:
		return false
	}
}

func sameEvidenceSource(leftKind MemorySourceKind, leftRef string, rightKind MemorySourceKind, rightRef string) bool {
	return leftKind == rightKind && strings.TrimSpace(leftRef) == strings.TrimSpace(rightRef)
}

func (m *Manager) deleteMemoryVector(ctx context.Context, id uint) error {
	if id == 0 || m.milvus == nil {
		return nil
	}
	return m.milvus.Delete(ctx, []uint{id})
}

type preparedMemoryVector struct {
	memory    Memory
	embedding []float64
}

func (m *Manager) prepareMemoryVector(ctx context.Context, mem Memory) (*preparedMemoryVector, error) {
	if !memoryVectorEligible(mem) || m.embedding == nil || m.milvus == nil {
		return nil, nil
	}
	embedding, err := m.embedding.Embed(ctx, mem.Content)
	if err != nil {
		return nil, err
	}
	return &preparedMemoryVector{memory: mem, embedding: embedding}, nil
}

func (m *Manager) insertPreparedMemoryVector(ctx context.Context, prepared *preparedMemoryVector) error {
	if prepared == nil || m.milvus == nil {
		return nil
	}
	_, err := m.milvus.Insert(ctx, prepared.memory.ID, prepared.memory.GroupID, string(prepared.memory.Type), prepared.embedding)
	return err
}

func (m *Manager) upsertMemoryVector(ctx context.Context, prepared *preparedMemoryVector) error {
	if prepared == nil || m.milvus == nil {
		return nil
	}
	_, err := m.milvus.Upsert(ctx, prepared.memory.ID, prepared.memory.GroupID, string(prepared.memory.Type), prepared.embedding)
	return err
}

func (m *Manager) replaceMergedMemoryVectors(ctx context.Context, prepared *preparedMemoryVector, oldIDs []uint) error {
	for _, id := range utils.UniqueIDs(oldIDs) {
		if err := m.deleteMemoryVector(ctx, id); err != nil {
			return err
		}
	}
	return m.upsertMemoryVector(ctx, prepared)
}

func (m *Manager) restoreExistingMemoryRowBestEffort(ctx context.Context, mem Memory) {
	if mem.ID == 0 {
		return
	}
	if err := m.db.WithContext(ctx).Save(&mem).Error; err != nil {
		zap.L().Warn("补偿恢复长期记忆行失败", zap.Uint("memory_id", mem.ID), zap.Error(err))
	}
}

func (m *Manager) restoreDeletedMemoryRowsBestEffort(ctx context.Context, memories ...Memory) {
	for _, mem := range memories {
		if mem.ID == 0 {
			continue
		}
		if err := m.db.WithContext(ctx).Create(&mem).Error; err != nil {
			zap.L().Warn("补偿重建长期记忆行失败", zap.Uint("memory_id", mem.ID), zap.Error(err))
		}
	}
}

func (m *Manager) restoreMemoryVectorsBestEffort(ctx context.Context, memories ...Memory) {
	for _, mem := range memories {
		prepared, err := m.prepareMemoryVector(ctx, mem)
		if err != nil {
			zap.L().Warn("补偿准备长期记忆向量失败", zap.Uint("memory_id", mem.ID), zap.Error(err))
			continue
		}
		if err := m.upsertMemoryVector(ctx, prepared); err != nil {
			zap.L().Warn("补偿恢复长期记忆向量失败", zap.Uint("memory_id", mem.ID), zap.Error(err))
		}
	}
}
