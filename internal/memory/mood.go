package memory

import (
	"time"

	"go.uber.org/zap"
)

// startMoodDecay 启动情绪衰减定时任务（每五分钟执行一次）
func (m *Manager) startMoodDecay() {
	ticker := time.NewTicker(5 * time.Minute)
	m.background.Add(1)
	go func() {
		defer m.background.Done()
		for {
			select {
			case <-ticker.C:
				if err := m.ApplyMoodDecay(); err != nil {
					zap.L().Error("情绪衰减失败", zap.Error(err))
				}
			case <-m.cleanupStop:
				ticker.Stop()
				return
			}
		}
	}()
	zap.L().Info("情绪衰减任务已启动")
}

// GetMoodState 获取当前情绪状态
func (m *Manager) GetMoodState() (*MoodState, error) {
	var mood MoodState
	if err := m.db.First(&mood, 1).Error; err != nil {
		return nil, err
	}
	return &mood, nil
}

// UpdateMoodState 更新情绪状态（增量更新）
func (m *Manager) UpdateMoodState(valenceDelta, energyDelta, sociabilityDelta float64, reason string) (*MoodState, error) {
	mood, err := m.GetMoodState()
	if err != nil {
		return nil, err
	}

	mood.Valence = min(max(mood.Valence+valenceDelta, -1.0), 1.0)
	mood.Energy = min(max(mood.Energy+energyDelta, 0.0), 1.0)
	mood.Sociability = min(max(mood.Sociability+sociabilityDelta, 0.0), 1.0)
	mood.LastReason = reason

	if err := m.db.Save(mood).Error; err != nil {
		return nil, err
	}
	return mood, nil
}

// ApplyMoodDecay 应用情绪自然衰减
func (m *Manager) ApplyMoodDecay() error {
	mood, err := m.GetMoodState()
	if err != nil {
		return err
	}

	mood.Valence *= 0.95
	mood.Energy += (0.5 - mood.Energy) * 0.05
	mood.Sociability += (0.5 - mood.Sociability) * 0.05

	return m.db.Save(mood).Error
}
