package memory

import (
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// lockConversationSnapshot 同时校验固定批次和本轮读取的历史原文，撤回后不提交旧认识
func lockConversationSnapshot(tx *gorm.DB, batch KnowledgeBatch, rows []MessageLog) ([]MessageLog, error) {
	var current []MessageLog
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_kind=? AND target_id=? AND id>? AND id<=?", batch.ConversationKind, batch.TargetID, batch.AfterID, batch.ThroughID).Order("id").Find(&current).Error; err != nil {
		return nil, err
	}
	if len(current) != len(rows) {
		return nil, ErrSnapshotChanged
	}
	for i, row := range current {
		if row.ID != rows[i].ID || row.TextContent != rows[i].TextContent || row.UserID != rows[i].UserID || (row.RecalledAt == nil) != (rows[i].RecalledAt == nil) {
			return nil, ErrSnapshotChanged
		}
	}
	readIDs := slices.Clone(batch.ReadMessageIDs)
	slices.Sort(readIDs)
	readIDs = slices.Compact(readIDs)
	if len(readIDs) > 0 {
		var valid []uint
		if err := tx.Raw("SELECT id FROM message_logs WHERE conversation_kind=? AND target_id=? AND id IN ? AND id<=? AND recalled_at IS NULL AND "+originalMessageTextSQL+"<>'' ORDER BY id FOR UPDATE", batch.ConversationKind, batch.TargetID, readIDs, batch.ThroughID).Scan(&valid).Error; err != nil {
			return nil, err
		}
		if len(valid) != len(readIDs) {
			return nil, ErrSnapshotChanged
		}
	}
	return current, nil
}
