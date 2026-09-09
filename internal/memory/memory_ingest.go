package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
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

func NormalizeMemoryClaims(raw []RawMemoryClaim, selfID int64) ([]MemoryClaim, error) {
	if len(raw) == 0 {
		return []MemoryClaim{}, nil
	}
	claims := make([]MemoryClaim, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		claim, err := NormalizeMemoryClaim(item, selfID)
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

	evidenceByMessageID := make(map[int64]MessageLog, len(evidence))
	for _, row := range evidence {
		evidenceByMessageID[row.OneBotMessageID] = row
	}
	for _, claim := range claims {
		if claim.SubjectUserID == 0 || claim.SubjectUserID == storeCtx.SelfID {
			continue
		}
		allowed := false
		for _, messageID := range claim.EvidenceMessageIDs {
			row := evidenceByMessageID[messageID]
			if row.UserID == claim.SubjectUserID {
				allowed = true
				break
			}
		}
		if !allowed {
			return claimError("invalid_subject", fmt.Sprintf("主体 %d 必须出现在本条证据中；依赖回复目标时须包含被回复原文", claim.SubjectUserID))
		}
	}
	return nil
}

func messageLogIDs(rows []MessageLog) []uint {
	ids := make([]uint, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}
