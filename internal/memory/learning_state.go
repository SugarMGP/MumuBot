package memory

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Manager) GetLearningState(groupID int64, kind LearningKind) (*LearningState, error) {
	var state LearningState
	result := m.db.Where("group_id = ? AND kind = ?", groupID, kind).Limit(1).Find(&state)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected > 0 {
		return &state, nil
	}
	state = LearningState{GroupID: groupID, Kind: kind}
	created := m.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&state)
	if created.Error != nil {
		return nil, created.Error
	}
	if created.RowsAffected == 0 {
		if err := m.db.Where("group_id = ? AND kind = ?", groupID, kind).First(&state).Error; err != nil {
			return nil, err
		}
	}
	return &state, nil
}

func updateLearningState(tx *gorm.DB, groupID int64, kind LearningKind, lastMessageID uint) error {
	state := LearningState{GroupID: groupID, Kind: kind, LastMessageLogID: lastMessageID}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "group_id"}, {Name: "kind"}},
		DoUpdates: clause.Assignments(map[string]any{"last_message_log_id": gorm.Expr("GREATEST(learning_states.last_message_log_id, EXCLUDED.last_message_log_id)")}),
	}).Create(&state).Error
}

func (m *Manager) UpdateLearningState(groupID int64, kind LearningKind, lastMessageID uint) error {
	return updateLearningState(m.db, groupID, kind, lastMessageID)
}
