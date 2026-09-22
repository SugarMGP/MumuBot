package memory

import (
	"fmt"

	"gorm.io/gorm"
)

func migrateV9(db *gorm.DB) error {
	if err := db.Exec(`
		ALTER TABLE member_profiles ADD COLUMN intimacy DOUBLE PRECISION NOT NULL DEFAULT 0;
		ALTER TABLE member_profiles ADD CONSTRAINT member_profiles_intimacy_check CHECK (intimacy >= -1 AND intimacy <= 1);
	`).Error; err != nil {
		return fmt.Errorf("执行 v9 迁移失败: %w", err)
	}
	return validateRelationshipSchema(db)
}

func validateRelationshipSchema(db *gorm.DB) error {
	var columnExists bool
	if err := db.Raw(`SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='member_profiles' AND column_name='intimacy'
	)`).Scan(&columnExists).Error; err != nil {
		return err
	}
	if !columnExists {
		return fmt.Errorf("member_profiles.intimacy 不存在")
	}

	var constraintExists bool
	if err := db.Raw(`SELECT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conrelid='member_profiles'::regclass
		  AND conname='member_profiles_intimacy_check'
		  AND convalidated
	)`).Scan(&constraintExists).Error; err != nil {
		return err
	}
	if !constraintExists {
		return fmt.Errorf("member_profiles 好感度范围约束不完整")
	}
	return nil
}
