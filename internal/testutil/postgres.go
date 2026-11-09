package testutil

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/migration"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Database 为真实 PostgreSQL 测试隔离 schema；未配置测试 DSN 时跳过数据库用例
func Database(t *testing.T) (*gorm.DB, *memory.Manager) {
	t.Helper()
	dsn := os.Getenv("MUMU_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("需要 MUMU_TEST_POSTGRES_DSN 的 PostgreSQL 关键字格式 DSN")
	}
	root, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	rootSQL, _ := root.DB()
	t.Cleanup(func() { _ = rootSQL.Close() })
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	schema := "mumu_test_" + hex.EncodeToString(nonce[:])
	if err := root.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	})
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema+",public"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := migration.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := config.InitStore(db); err != nil {
		t.Fatal(err)
	}
	manager, err := memory.NewManager(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return db, manager
}
