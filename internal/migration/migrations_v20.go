package migration

import "gorm.io/gorm"

func upgradeV20(tx *gorm.DB) error {
	return tx.Exec(`ALTER TABLE model_profiles
 ADD COLUMN thinking_budget INTEGER NULL CHECK(thinking_budget IS NULL OR thinking_budget >= -1),
 ADD COLUMN thinking_level TEXT NOT NULL DEFAULT '' CHECK(thinking_level IN ('','low','medium','high'))`).Error
}
