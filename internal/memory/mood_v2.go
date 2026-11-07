package memory

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpdateAffectState 更新精力和社交意愿，不把 valence 当作具体情感
func (m *Manager) UpdateAffectState(ctx context.Context, energy, sociability float64, reason string) (*MoodState, error) {
	var mood *MoodState
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var writeErr error
		mood, writeErr = writeAffectTx(tx, energy, sociability, reason)
		return writeErr
	})
	if err != nil {
		return nil, err
	}
	return mood, nil
}

// applyAffectUpdateTx 在调用方事务里按 low/normal/high 改共享精力与交流意愿
func applyAffectUpdateTx(tx *gorm.DB, update AffectUpdate) (*MoodState, error) {
	var mood MoodState
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=1").Take(&mood).Error; err != nil {
		return nil, err
	}
	energy, sociability := mood.Energy, mood.Sociability
	if update.Energy != nil {
		level, err := AffectLevel(*update.Energy)
		if err != nil {
			return nil, err
		}
		energy = level
	}
	if update.Sociability != nil {
		level, err := AffectLevel(*update.Sociability)
		if err != nil {
			return nil, err
		}
		sociability = level
	}
	return writeAffectLocked(tx, &mood, energy, sociability, update.Reason)
}

func writeAffectTx(tx *gorm.DB, energy, sociability float64, reason string) (*MoodState, error) {
	var mood MoodState
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=1").Take(&mood).Error; err != nil {
		return nil, err
	}
	return writeAffectLocked(tx, &mood, energy, sociability, reason)
}

func writeAffectLocked(tx *gorm.DB, mood *MoodState, energy, sociability float64, reason string) (*MoodState, error) {
	mood.Energy = min(max(energy, 0), 1)
	mood.Sociability = min(max(sociability, 0), 1)
	mood.LastReason = reason
	mood.UpdatedAt = time.Now()
	if err := tx.Save(mood).Error; err != nil {
		return nil, err
	}
	return mood, nil
}
