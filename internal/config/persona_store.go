package config

import (
	"fmt"

	"gorm.io/gorm"
)

// loadActivePersona 读取当前生效的人格预设，没有可用预设时返回 nil
func loadActivePersona(db *gorm.DB) (*PersonaConfig, error) {
	var rows []struct {
		ID          uint   `gorm:"column:id"`
		Name        string `gorm:"column:name"`
		PersonaCard string `gorm:"column:persona_card"`
		Simulation  string `gorm:"column:simulation_prompt"`
		LifePrompt  string `gorm:"column:life_prompt"`
	}
	err := db.Raw(`SELECT id, name, persona_card, simulation_prompt, life_prompt FROM persona_presets WHERE active LIMIT 1`).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("读取人格预设失败: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &PersonaConfig{
		PresetID:    rows[0].ID,
		PresetName:  rows[0].Name,
		PersonaCard: rows[0].PersonaCard,
		Simulation:  rows[0].Simulation,
		LifePrompt:  rows[0].LifePrompt,
	}, nil
}
