package migration

import (
	"fmt"

	"gorm.io/gorm"
)

// migrateV14 新建合并转发内容表，句柄出现在消息正文里，内容跟着消息一起保留
func migrateV14(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS message_forwards (
			id BIGSERIAL PRIMARY KEY,
			handle TEXT NOT NULL UNIQUE,
			message_log_id BIGINT NOT NULL REFERENCES message_logs(id) ON DELETE CASCADE,
			parent_handle TEXT REFERENCES message_forwards(handle) ON DELETE CASCADE,
			nodes JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE INDEX IF NOT EXISTS idx_message_forwards_message_log_id ON message_forwards(message_log_id)`,
		`CREATE INDEX IF NOT EXISTS idx_message_forwards_parent_handle ON message_forwards(parent_handle)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("v14 迁移失败: %w", err)
		}
	}
	return validateV14Schema(db)
}

func validateV14Schema(db *gorm.DB) error {
	var exists bool
	if err := db.Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='message_forwards')`).Scan(&exists).Error; err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("message_forwards 表尚未建立")
	}
	return nil
}
