package migration

import "gorm.io/gorm"

func upgradeV18(tx *gorm.DB) error {
	return tx.Exec(`INSERT INTO app_config(config_key,config_value,version,updated_at)
 SELECT 'model_selection',jsonb_build_object('qq',id,'life',id,'worker',id),1,now() FROM model_profiles WHERE active
 ON CONFLICT(config_key) DO NOTHING;
 ALTER TABLE model_profiles
 DROP CONSTRAINT model_profiles_reasoning_effort_check,
 ADD CONSTRAINT model_profiles_reasoning_effort_check CHECK(reasoning_effort IN ('','none','low','medium','high','xhigh','max'));
 UPDATE model_profiles SET reasoning_effort='none' WHERE thinking_type='disabled';
 ALTER TABLE model_profiles DROP COLUMN thinking_type, DROP COLUMN active`).Error
}
