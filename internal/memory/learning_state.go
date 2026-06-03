package memory

import (
	"errors"

	"gorm.io/gorm"
)

// GetLearningState 获取群组的学习进度
func (m *Manager) GetLearningState(groupID int64) (*LearningState, error) {
	var state LearningState
	err := m.db.Where("group_id = ?", groupID).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &LearningState{GroupID: groupID}, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// UpdateLearningState 更新群组的学习进度
func (m *Manager) UpdateLearningState(groupID int64, lastMessageID uint) error {
	var state LearningState
	err := m.db.Where("group_id = ?", groupID).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		state = LearningState{
			GroupID:       groupID,
			LastMessageID: lastMessageID,
		}
		return m.db.Create(&state).Error
	}
	if err != nil {
		return err
	}

	state.LastMessageID = lastMessageID
	return m.db.Save(&state).Error
}
