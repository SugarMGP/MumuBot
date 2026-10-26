package migration

import (
	"fmt"

	"gorm.io/gorm"
)

// migrateV13 给消息记录补上媒体引用列，保存会话里已缓存的媒体路径
func migrateV13(db *gorm.DB) error {
	if err := db.Exec(`ALTER TABLE message_logs ADD COLUMN IF NOT EXISTS media_refs jsonb`).Error; err != nil {
		return fmt.Errorf("v13 迁移失败: %w", err)
	}
	return validateV13Schema(db)
}

func validateV13Schema(db *gorm.DB) error {
	var exists bool
	if err := db.Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='message_logs' AND column_name='media_refs')`).Scan(&exists).Error; err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("message_logs.media_refs 列尚未建立")
	}
	return nil
}
