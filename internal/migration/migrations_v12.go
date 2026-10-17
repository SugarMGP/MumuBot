package migration

import (
	"fmt"

	"gorm.io/gorm"
)

// migrateV12 清理已移除的本地好友申请记录，不操作 QQ 账号上的申请
func migrateV12(db *gorm.DB) error {
	if err := db.Exec(`DROP TABLE friend_requests`).Error; err != nil {
		return fmt.Errorf("v12 迁移失败: %w", err)
	}
	return validateV12Schema(db)
}

func validateV12Schema(db *gorm.DB) error {
	var exists bool
	if err := db.Raw(`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='friend_requests')`).Scan(&exists).Error; err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("friend_requests 表尚未移除")
	}
	return nil
}
