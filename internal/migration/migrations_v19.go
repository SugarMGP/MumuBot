package migration

import "gorm.io/gorm"

func upgradeV19(tx *gorm.DB) error {
	return tx.Exec(`ALTER TABLE model_profiles
 ADD COLUMN thinking_mode TEXT NOT NULL DEFAULT '' CHECK(thinking_mode IN ('','enabled','disabled')),
 ADD COLUMN body_override JSONB NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(body_override)='object')`).Error
}
