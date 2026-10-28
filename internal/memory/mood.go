package memory

// GetMoodState 获取当前的精力与社交意愿
func (m *Manager) GetMoodState() (*MoodState, error) {
	var mood MoodState
	if err := m.db.First(&mood, 1).Error; err != nil {
		return nil, err
	}
	return &mood, nil
}
