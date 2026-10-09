package migration

import (
	"fmt"

	"gorm.io/gorm"
)

func migrateV11(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE learning_states ADD COLUMN next_attempt_at TIMESTAMPTZ`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("v11 迁移失败: %w", err)
		}
	}
	return validateV11Schema(db)
}

// validateV11Schema 确认整理冷却字段存在
func validateV11Schema(db *gorm.DB) error {
	var count int64
	if err := db.Raw(`SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='learning_states' AND column_name='next_attempt_at' AND data_type='timestamp with time zone'`).Scan(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("learning_states 缺少 next_attempt_at 字段")
	}
	return nil
}
