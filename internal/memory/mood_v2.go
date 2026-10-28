package memory

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpdateAffectState 更新精力和社交意愿，不把 valence 当作具体情感
func (m *Manager) UpdateAffectState(ctx context.Context, energy, sociability float64, reason string) (*MoodState, error) {
	var mood MoodState
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=1").Take(&mood).Error; err != nil {
			return err
		}
		mood.Energy = min(max(energy, 0), 1)
		mood.Sociability = min(max(sociability, 0), 1)
		mood.LastReason = reason
		mood.UpdatedAt = time.Now()
		return tx.Save(&mood).Error
	})
	if err != nil {
		return nil, err
	}
	return &mood, nil
}
