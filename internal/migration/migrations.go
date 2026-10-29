package migration

import (
	"fmt"

	"mumu-bot/internal/memory"

	"gorm.io/gorm"
)

const latestSchemaVersion = 16

// LatestSchemaVersion 返回当前程序支持的数据结构版本
func LatestSchemaVersion() int { return latestSchemaVersion }

// RunMigrations 接受空库，以及已升级到 v14 或 v15 的数据库
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
		if len(tables) == 0 {
			if err := initializeV15Schema(tx); err != nil {
				return err
			}
			if err := migrateV16(tx); err != nil {
				return err
			}
			if err := recordSchemaVersion(tx, 16, v16BaseName); err != nil {
				return err
			}
			return validateV16Schema(tx)
		}
		if !tables["schema_migrations"] {
			return fmt.Errorf("数据库没有 schema_migrations；请先用旧程序升级到 v14 后重试")
		}
		var versions []memory.SchemaMigration
		if err := tx.Order("version ASC").Find(&versions).Error; err != nil {
			return err
		}
		if len(versions) == 0 {
			return fmt.Errorf("schema 版本表为空，拒绝猜测迁移")
		}
		if err := validateMigrationChain(versions); err != nil {
			return err
		}
		last := versions[len(versions)-1]
		switch {
		case last.Version == 14 && last.Name == "message_forwards":
			if err := migrateV15(tx); err != nil {
				return err
			}
			if err := recordSchemaVersion(tx, 15, v15BaseName); err != nil {
				return err
			}
		case last.Version == 15 && last.Name == v15BaseName:
		case last.Version == 16 && last.Name == v16BaseName:
		default:
			return fmt.Errorf("数据库版本 v%d 不是可升级的 v14、v15 或 v16，拒绝启动", last.Version)
		}
		if last.Version < 16 {
			if err := migrateV16(tx); err != nil {
				return err
			}
			if err := recordSchemaVersion(tx, 16, v16BaseName); err != nil {
				return err
			}
		}
		return validateV16Schema(tx)
	})
}

func validateMigrationChain(versions []memory.SchemaMigration) error {
	if len(versions) == 0 {
		return fmt.Errorf("schema 版本表为空，拒绝猜测迁移")
	}
	first := versions[0].Version
	switch {
	case first == 15:
		for i, item := range versions {
			if item.Version != 15+i {
				return fmt.Errorf("schema 版本记录不连续：第 %d 条为 v%d", i+1, item.Version)
			}
		}
	case len(versions) == 1 && first == latestSchemaVersion:
	default:
		for i, item := range versions {
			if item.Version != i+1 {
				return fmt.Errorf("schema 版本记录不连续：第 %d 条为 v%d", i+1, item.Version)
			}
		}
	}
	for _, item := range versions {
		if item.Version > latestSchemaVersion {
			return fmt.Errorf("数据库 schema v%d 高于程序支持的 v%d", item.Version, latestSchemaVersion)
		}
	}
	return nil
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
