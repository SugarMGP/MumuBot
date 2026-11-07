package migration

import (
	"fmt"

	"mumu-bot/internal/memory"

	"gorm.io/gorm"
)

const latestSchemaVersion = 17

// LatestSchemaVersion 返回当前程序支持的数据结构版本。
func LatestSchemaVersion() int { return latestSchemaVersion }

// RunMigrations 初始化空库，已初始化的当前库只做校验后复用；旧 schema 不自动迁移或删除。
func RunMigrations(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("PostgreSQL 未初始化")
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
			// 已是当前 schema 时允许直接重启复用；任何前置版本或结构缺失都要求清库
			if validateErr := validateV17Schema(tx); validateErr != nil {
				return fmt.Errorf("当前数据库不兼容本版本 v17 schema，请备份后删除旧数据库并重新初始化: %w", validateErr)
			}
			return nil
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
	for _, column := range []struct{ table, name string }{
		{"worker_sessions", "user_name"},
		{"worker_sessions", "home_dir"},
	} {
		if db.Migrator().HasColumn(column.table, column.name) {
			return fmt.Errorf("v17 schema 含已移除的 %s.%s；请确认这是旧版多账号结构后再手工迁移", column.table, column.name)
		}
	}
	if db.Migrator().HasTable("session_messages") {
		return fmt.Errorf("v17 schema 仍含已移除的 session_messages；内部投信编号已统一使用 transcript 行号")
	}
	for _, table := range []string{
		"schema_migrations", "message_logs", "message_forwards", "conversation_sessions",
		"conversation_transcript", "context_compaction_checkpoints", "topic_threads",
		"topic_assignments", "topic_summaries", "topic_summary_sources", "knowledge_items",
		"knowledge_revisions", "emotion_states", "emotion_revisions", "member_profiles",
		"member_names", "stickers", "mood_state", "model_call_hourly", "conversation_targets",
		"worker_sessions", "life_state", "schedules", "diaries", "app_config", "model_profiles", "mcp_servers", "persona_presets",
	} {
		if !db.Migrator().HasTable(table) {
			return fmt.Errorf("v17 缺少表 %s", table)
		}
	}
	for _, column := range []struct{ table, name string }{
		{"message_logs", "one_bot_message_id"},
		{"message_logs", "file_refs"},
		{"knowledge_items", "subject_type"},
		{"knowledge_items", "subject_target"},
		{"knowledge_items", "memory_type"},
		{"knowledge_items", "pinned"},
		{"topic_summary_sources", "is_anchor"},
		{"app_config", "config_key"},
		{"model_profiles", "thinking_type"},
		{"model_profiles", "reasoning_effort"},
		{"mcp_servers", "config_ciphertext"},
		{"worker_sessions", "context_start_id"},
		{"worker_sessions", "context_summary"},
		{"worker_sessions", "context_through_id"},
		{"life_state", "context_summary"},
		{"life_state", "context_through_id"},
		{"life_state", "last_error_code"},
		{"schedules", "schedule_key"},
		{"schedules", "next_run_at"},
		{"diaries", "diary_date"},
		{"diaries", "content_markdown"},
		{"diaries", "created_at"},
		{"diaries", "updated_at"},
		{"emotion_states", "fact"},
		{"emotion_revisions", "snapshot"},
		{"persona_presets", "life_prompt"},
		{"knowledge_items", "stability_days"},
		{"knowledge_items", "recall_anchor_at"},
		{"knowledge_items", "forget_at"},
		{"topic_threads", "stability_days"},
		{"topic_threads", "recall_anchor_at"},
		{"topic_threads", "forget_at"},
		{"topic_threads", "forgotten_at"},
		{"topic_summary_sources", "one_bot_message_id"},
		{"topic_summary_sources", "message_time"},
	} {
		if !db.Migrator().HasColumn(column.table, column.name) {
			return fmt.Errorf("v17 缺少字段 %s.%s", column.table, column.name)
		}
	}
	for _, index := range []struct{ table, name string }{
		{"knowledge_items", "uq_knowledge_content_active"},
		{"knowledge_items", "idx_knowledge_forget"},
		{"topic_threads", "idx_topic_forget"},
		{"topic_summary_sources", "idx_topic_sources_time"},
		{"message_logs", "idx_message_logs_time_id"},
		{"message_logs", "idx_message_logs_media_refs"},
		{"conversation_transcript", "idx_transcript_message_log"},
	} {
		if !db.Migrator().HasIndex(index.table, index.name) {
			return fmt.Errorf("v17 缺少索引 %s.%s", index.table, index.name)
		}
	}
	// 来源消息不再外键约束原文；旧 FK 会让过期原文无法清理
	var legacyFK int64
	if err := db.Raw(`SELECT count(*) FROM pg_constraint WHERE conname='topic_summary_sources_message_log_id_fkey'`).Scan(&legacyFK).Error; err != nil {
		return err
	}
	if legacyFK > 0 {
		return fmt.Errorf("v17 结构不兼容：topic_summary_sources 仍保留原文外键")
	}
	// 旧情感结构不能因为版本号相同就直接复用
	for _, column := range []struct{ table, name string }{
		{"emotion_states", "about_user_id"},
		{"emotion_states", "decay_class"},
		{"emotion_revisions", "before_strength"},
		{"emotion_revisions", "after_strength"},
		{"emotion_revisions", "reason"},
	} {
		if db.Migrator().HasColumn(column.table, column.name) {
			return fmt.Errorf("v17 结构不兼容：%s.%s 已废弃", column.table, column.name)
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
