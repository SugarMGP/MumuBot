package memory

import (
	"fmt"

	"gorm.io/gorm"
)

func migrateV2(db *gorm.DB) error {
	if err := db.Exec(`ALTER TABLE message_logs DROP COLUMN forward_payload`).Error; err != nil {
		return fmt.Errorf("执行 v2 迁移失败: %w", err)
	}
	return nil
}

func validateCurrentSchema(db *gorm.DB) error {
	var exists bool
	if err := db.Raw(`SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='message_logs' AND column_name='forward_payload'
	)`).Scan(&exists).Error; err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("message_logs.forward_payload 在当前 schema 中不应存在")
	}
	return nil
}
