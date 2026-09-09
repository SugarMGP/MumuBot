package memory

import (
	"context"
	"fmt"
)

// PrepareClaimBatch 在提交方入口统一将外部消息 ID 解析为内部消息 ID
func (m *Manager) PrepareClaimBatch(ctx context.Context, scope StoreClaimsContext, claims []MemoryClaim) (KnowledgeBatch, error) {
	batch := KnowledgeBatch{GroupID: scope.GroupID, SelfID: scope.SelfID}
	if err := m.validateClaimEvidence(ctx, scope, claims); err != nil {
		return batch, err
	}
	if scope.SnapshotOneBotMessageID != 0 {
		row, err := m.GetMessageLogByID(scope.GroupID, scope.SnapshotOneBotMessageID)
		if err != nil {
			return batch, err
		}
		batch.ThroughID = row.ID
	} else {
		return batch, fmt.Errorf("缺少知识证据上界")
	}
	for i, claim := range claims {
		var rows []MessageLog
		if err := m.db.WithContext(ctx).Where("group_id=? AND one_bot_message_id IN ? AND id<=?", scope.GroupID, claim.EvidenceMessageIDs, batch.ThroughID).Order("id").Find(&rows).Error; err != nil {
			return batch, err
		}
		if len(rows) != len(claim.EvidenceMessageIDs) {
			return batch, fmt.Errorf("知识证据已变化")
		}
		ids := messageLogIDs(rows)
		batch.ReadMessageIDs = append(batch.ReadMessageIDs, ids...)
		batch.Items = append(batch.Items, KnowledgeItemInput{Key: fmt.Sprintf("claim%d", i), SubjectUserID: claim.SubjectUserID, Kind: string(claim.Kind), Content: claim.Content, Status: "candidate", EvidenceSets: [][]uint{ids}})
	}
	return batch, nil
}
