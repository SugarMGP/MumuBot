package migration

import (
	"fmt"

	"mumu-bot/internal/memory"

	"gorm.io/gorm"
)

const latestSchemaVersion = 17

// LatestSchemaVersion 返回当前程序支持的数据结构版本。
func LatestSchemaVersion() int { return latestSchemaVersion }

// RunMigrations 只接受空数据库；旧 schema 不自动迁移或删除。
func RunMigrations(db *gorm.DB, selfID int64) error {
	if db == nil {
		return fmt.Errorf("PostgreSQL 未初始化")
	}
	if selfID <= 0 {
		return fmt.Errorf("OneBot self_id 无效")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('mumubot_schema_migrations'))`).Error; err != nil {
			return fmt.Errorf("获取数据库迁移锁失败: %w", err)
		}
		if err := ensureExtensions(tx); err != nil {
			return err
		}
		tables, err := currentTables(tx)
		if err != nil {
			return err
		}
		if len(tables) > 0 {
			return fmt.Errorf("当前数据库不是 v17 空库。该版本不兼容旧数据，请备份后删除旧数据库并重新初始化")
		}
		if err := initializeV17Schema(tx); err != nil {
			return err
		}
		if err := recordSchemaVersion(tx, latestSchemaVersion, v17BaseName); err != nil {
			return err
		}
		return validateV17Schema(tx)
	})
}

func ensureExtensions(db *gorm.DB) error {
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public").Error; err != nil {
		return fmt.Errorf("启用 PostgreSQL 扩展 pg_trgm 失败: %w", err)
	}
	return nil
}

func currentTables(db *gorm.DB) (map[string]bool, error) {
	var names []string
	if err := db.Raw(`SELECT tablename FROM pg_tables WHERE schemaname=current_schema()`).Scan(&names).Error; err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(names))
	for _, name := range names {
		result[name] = true
	}
	return result, nil
}

func recordSchemaVersion(db *gorm.DB, version int, name string) error {
	return db.Exec(`INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,now())`, version, name).Error
}

func validateV17Schema(db *gorm.DB) error {
	for _, table := range []string{
		"schema_migrations", "message_logs", "message_forwards", "conversation_sessions",
		"conversation_transcript", "context_compaction_checkpoints", "topic_threads",
		"topic_assignments", "topic_summaries", "topic_summary_sources", "knowledge_items",
		"knowledge_revisions", "emotion_states", "emotion_revisions", "member_profiles",
		"member_names", "stickers", "mood_state", "model_call_hourly", "conversation_targets",
		"session_messages",
	} {
		if !db.Migrator().HasTable(table) {
			return fmt.Errorf("v17 缺少表 %s", table)
		}
	}
	for _, column := range []struct{ table, name string }{
		{"conversation_sessions", "reference_seen"},
		{"message_logs", "one_bot_message_id"},
		{"knowledge_items", "subject_type"},
		{"knowledge_items", "subject_target"},
		{"knowledge_items", "memory_type"},
		{"knowledge_items", "pinned"},
		{"topic_summary_sources", "is_anchor"},
		{"context_compaction_checkpoints", "reference_versions"},
	} {
		if !db.Migrator().HasColumn(column.table, column.name) {
			return fmt.Errorf("v17 缺少字段 %s.%s", column.table, column.name)
		}
	}
	var version int
	if err := db.Model(&memory.SchemaMigration{}).Select("COALESCE(max(version),0)").Scan(&version).Error; err != nil {
		return err
	}
	if version != latestSchemaVersion {
		return fmt.Errorf("v17 schema 版本记录为 %d，期望 %d", version, latestSchemaVersion)
	}
	return nil
}
