package config

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	SectionPersona   = "persona"
	SectionAgent     = "agent"
	SectionChat      = "chat"
	SectionCompactor = "compactor"
	SectionMedia     = "media"
	SectionDebug     = "debug"
)

var store struct {
	mu       sync.RWMutex
	db       *gorm.DB
	current  atomic.Pointer[RuntimeConfig]
	versions map[string]int64
}

func init() {
	defaults := DefaultRuntime()
	store.current.Store(&defaults)
	store.versions = make(map[string]int64)
}

// InitStore 连接数据库并加载动态配置，缺失项使用代码默认值
func InitStore(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("配置数据库未初始化")
	}
	store.mu.Lock()
	store.db = db
	store.mu.Unlock()
	return Reload()
}

// Reload 重新读取 app_config，未知或损坏的行回退默认值
func Reload() error {
	store.mu.RLock()
	db := store.db
	store.mu.RUnlock()
	if db == nil {
		return fmt.Errorf("配置数据库未初始化")
	}
	rows := make([]struct {
		ConfigKey   string          `gorm:"column:config_key"`
		ConfigValue json.RawMessage `gorm:"column:config_value"`
		Version     int64           `gorm:"column:version"`
	}, 0)
	if err := db.Raw(`SELECT config_key, config_value, version FROM app_config`).Scan(&rows).Error; err != nil {
		return fmt.Errorf("读取动态配置失败: %w", err)
	}
	cfg := DefaultRuntime()
	versions := make(map[string]int64)
	for _, row := range rows {
		versions[row.ConfigKey] = row.Version
		if err := applySection(&cfg, row.ConfigKey, row.ConfigValue); err != nil {
			zap.L().Warn("动态配置项无效，使用默认值", zap.String("key", row.ConfigKey), zap.Error(err))
			continue
		}
	}
	validated, err := cfg.Validate()
	if err != nil {
		return fmt.Errorf("动态配置校验失败: %w", err)
	}
	store.mu.Lock()
	store.current.Store(&validated)
	store.versions = versions
	store.mu.Unlock()
	return nil
}

// Snapshot 返回当前不可变配置快照
func Snapshot() *RuntimeConfig {
	if value := store.current.Load(); value != nil {
		return value
	}
	defaults := DefaultRuntime()
	return &defaults
}

// SectionVersions 返回当前各配置 section 的版本
func SectionVersions() map[string]int64 {
	store.mu.RLock()
	defer store.mu.RUnlock()
	result := make(map[string]int64, len(store.versions))
	for key, version := range store.versions {
		result[key] = version
	}
	return result
}

// SaveSection 保存一个配置 section；expectedVersion 为 0 表示新建
func SaveSection(ctx context.Context, key string, value any, expectedVersion int64) error {
	store.mu.RLock()
	db := store.db
	current := Snapshot()
	store.mu.RUnlock()
	if db == nil {
		return fmt.Errorf("配置数据库未初始化")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	candidate := *current
	if err := applySection(&candidate, key, raw); err != nil {
		return err
	}
	validated, err := candidate.Validate()
	if err != nil {
		return err
	}
	var changed int64
	if expectedVersion > 0 {
		result := db.WithContext(ctx).Exec(`
			UPDATE app_config SET config_value = ?::jsonb, version = version + 1, updated_at = now()
			WHERE config_key = ? AND version = ?`, string(raw), key, expectedVersion)
		if result.Error != nil {
			return fmt.Errorf("保存配置失败: %w", result.Error)
		}
		changed = result.RowsAffected
	} else {
		result := db.WithContext(ctx).Exec(`
			INSERT INTO app_config(config_key, config_value, version, updated_at)
			VALUES (?, ?::jsonb, 1, now())
			ON CONFLICT (config_key) DO NOTHING`, key, string(raw))
		if result.Error != nil {
			return fmt.Errorf("保存配置失败: %w", result.Error)
		}
		changed = result.RowsAffected
	}
	if changed == 0 {
		return fmt.Errorf("配置已被其他页面修改，请刷新后重试")
	}
	store.mu.Lock()
	store.current.Store(&validated)
	if expectedVersion > 0 {
		store.versions[key] = expectedVersion + 1
	} else {
		store.versions[key] = 1
	}
	store.mu.Unlock()
	return nil
}

// ResetSection 删除覆盖值并恢复代码默认
func ResetSection(ctx context.Context, key string, expectedVersion int64) error {
	store.mu.RLock()
	db := store.db
	store.mu.RUnlock()
	if db == nil {
		return fmt.Errorf("配置数据库未初始化")
	}
	result := db.WithContext(ctx).Exec(`DELETE FROM app_config WHERE config_key = ? AND version = ?`, key, expectedVersion)
	if result.Error != nil {
		return fmt.Errorf("恢复默认配置失败: %w", result.Error)
	}
	if expectedVersion > 0 && result.RowsAffected == 0 {
		return fmt.Errorf("配置已被其他页面修改，请刷新后重试")
	}
	return Reload()
}

func applySection(cfg *RuntimeConfig, key string, raw json.RawMessage) error {
	switch key {
	case SectionPersona:
		return json.Unmarshal(raw, &cfg.Persona)
	case SectionAgent:
		return json.Unmarshal(raw, &cfg.Agent)
	case SectionChat:
		return json.Unmarshal(raw, &cfg.Chat)
	case SectionCompactor:
		return json.Unmarshal(raw, &cfg.Compactor)
	case SectionMedia:
		return json.Unmarshal(raw, &cfg.Media)
	case SectionDebug:
		return json.Unmarshal(raw, &cfg.Debug)
	default:
		return fmt.Errorf("未知配置项 %q", key)
	}
}
