package migration

import (
	"fmt"

	"mumu-bot/internal/memory"

	"gorm.io/gorm"
)

const latestSchemaVersion = 11

func LatestSchemaVersion() int { return latestSchemaVersion }

var v1BusinessTables = []string{
	"message_logs", "topic_threads", "topic_assignments", "topic_summaries",
	"memories", "memory_evidence", "style_patterns", "style_pattern_evidence",
	"jargons", "jargon_evidence", "member_profiles", "member_names",
	"member_traits", "member_trait_evidence", "learning_states", "stickers", "mood_state",
}

func RunMigrations(db *gorm.DB, selfID int64, dimensions int) error {
	if db == nil {
		return fmt.Errorf("PostgreSQL 未初始化")
	}
	if selfID <= 0 {
		return fmt.Errorf("OneBot self_id 无效")
	}
	if dimensions <= 0 {
		return fmt.Errorf("embedding 维度无效")
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
		if tables["schema_migrations"] {
			return applyVersionedMigrations(tx, selfID, dimensions)
		}
		present := 0
		for _, table := range v1BusinessTables {
			if tables[table] {
				present++
			}
		}
		switch {
		case len(tables) == 0:
			if err := initializeV1Schema(tx, dimensions); err != nil {
				return err
			}
		case present == len(v1BusinessTables) && len(tables) == len(v1BusinessTables):
			if err := migrateV1(tx, selfID, dimensions); err != nil {
				return err
			}
		default:
			return fmt.Errorf("数据库结构不完整：发现 %d/%d 张已知业务表，拒绝猜测修复", present, len(v1BusinessTables))
		}
		if err := recordSchemaVersion(tx, 1, "v1_schema"); err != nil {
			return err
		}
		return applyVersionedMigrations(tx, selfID, dimensions)
	})
}

func ensureExtensions(db *gorm.DB) error {
	for _, extension := range []string{"vector", "pg_trgm"} {
		if err := db.Exec("CREATE EXTENSION IF NOT EXISTS " + extension + " WITH SCHEMA public").Error; err != nil {
			return fmt.Errorf("启用 PostgreSQL 扩展 %s 失败: %w", extension, err)
		}
	}
	return nil
}

func currentTables(db *gorm.DB) (map[string]bool, error) {
	var names []string
	if err := db.Raw(`SELECT tablename FROM pg_tables WHERE schemaname = current_schema()`).Scan(&names).Error; err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(names))
	for _, name := range names {
		result[name] = true
	}
	return result, nil
}

func applyVersionedMigrations(db *gorm.DB, selfID int64, dimensions int) error {
	migrations := []struct {
		name  string
		apply func() error
	}{
		{"v1_schema", nil},
		{"drop_forward_payload", func() error { return migrateV2(db) }},
		{"normalize_message_display_content", func() error { return migrateV3(db) }},
		{"unified_knowledge", func() error { return migrateV4(db, selfID, dimensions) }},
		// v5、v6 的改动已随已发布的 v4 执行，这里只补齐连续版本记录
		{"knowledge_review_repair", nil},
		{"unified_conversation", nil},
		{"topic_sources", func() error { return migrateV7(db) }},
		{"active_knowledge", func() error { return migrateV8(db) }},
		{"member_intimacy", func() error { return migrateV9(db) }},
		{"conversation_targets_and_memory_model", func() error { return migrateV10(db) }},
		{"learning_cooldown", func() error { return migrateV11(db) }},
	}
	if len(migrations) != latestSchemaVersion {
		return fmt.Errorf("程序迁移定义不完整：声明 v%d，实际 %d 个版本", latestSchemaVersion, len(migrations))
	}
	tables, err := currentTables(db)
	if err != nil {
		return err
	}
	var versions []memory.SchemaMigration
	if err := db.Order("version ASC").Find(&versions).Error; err != nil {
		return err
	}
	current := 0
	for i, item := range versions {
		expected := i + 1
		if item.Version != expected {
			return fmt.Errorf("schema 版本记录不连续：期望 v%d，实际 v%d", expected, item.Version)
		}
		if item.Version > latestSchemaVersion {
			return fmt.Errorf("数据库 schema v%d 高于程序支持的 v%d", item.Version, latestSchemaVersion)
		}
		if item.Version < 1 || item.Version > len(migrations) {
			return fmt.Errorf("schema 版本号无效：v%d", item.Version)
		}
		expectedName := migrations[item.Version-1].name
		if item.Name != expectedName {
			return fmt.Errorf("schema v%d 名称无效：%s", item.Version, item.Name)
		}
		current = item.Version
	}
	if current < 1 {
		return fmt.Errorf("schema 版本表存在但没有有效版本记录，拒绝猜测迁移")
	}
	if current < 4 {
		for _, table := range append(append([]string{}, v1BusinessTables...), "model_call_hourly") {
			if !tables[table] {
				return fmt.Errorf("schema v%d 缺少业务表 %s", current, table)
			}
		}
		if err := validateV1Schema(db, dimensions); err != nil {
			return err
		}
	}
	for version := current + 1; version <= len(migrations); version++ {
		migration := migrations[version-1]
		if migration.apply != nil {
			if err := migration.apply(); err != nil {
				return err
			}
		}
		if err := recordSchemaVersion(db, version, migration.name); err != nil {
			return err
		}
	}
	if err := validateV4Schema(db, dimensions); err != nil {
		return err
	}
	if err := db.Exec("SELECT summary_id,message_log_id FROM topic_summary_sources LIMIT 0").Error; err != nil {
		return fmt.Errorf("v7 摘要来源结构不完整: %w", err)
	}
	if err := validateV8Schema(db); err != nil {
		return err
	}
	if err := validateCurrentSchema(db); err != nil {
		return err
	}
	if err := validateRelationshipSchema(db); err != nil {
		return err
	}
	if err := validateV10Schema(db); err != nil {
		return err
	}
	return validateV11Schema(db)
}

func recordSchemaVersion(db *gorm.DB, version int, name string) error {
	return db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, now())`, version, name).Error
}
