package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/secrets"

	"gorm.io/gorm"
)

// StoredServer 是 mcp_servers 表的元数据；完整配置只保存密文
type StoredServer struct {
	ID           uint      `gorm:"column:id;primaryKey" json:"id"`
	Name         string    `gorm:"column:name" json:"name"`
	Enabled      bool      `gorm:"column:enabled" json:"enabled"`
	Transport    string    `gorm:"column:transport" json:"transport"`
	ConfigCipher []byte    `gorm:"column:config_ciphertext" json:"-"`
	ConfigNonce  []byte    `gorm:"column:config_nonce" json:"-"`
	KeyVersion   int       `gorm:"column:key_version" json:"-"`
	Version      int64     `gorm:"column:version" json:"version"`
	Position     int       `gorm:"column:position" json:"position"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (StoredServer) TableName() string { return "mcp_servers" }

var ErrNotFound = errors.New("MCP 服务器不存在")

// Store 管理 MCP 服务器
type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// List 返回服务器元数据
func (s *Store) List(ctx context.Context) ([]StoredServer, error) {
	var rows []StoredServer
	if err := s.db.WithContext(ctx).Order("position ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取 MCP 服务器失败: %w", err)
	}
	return rows, nil
}

// Get 返回解密后的服务器配置
func (s *Store) Get(ctx context.Context, id uint) (ServerConfig, StoredServer, error) {
	var row StoredServer
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ServerConfig{}, row, ErrNotFound
	}
	if err != nil {
		return ServerConfig{}, row, fmt.Errorf("读取 MCP 服务器失败: %w", err)
	}
	cfg, err := decryptServer(row)
	if err != nil {
		return ServerConfig{}, row, err
	}
	return cfg, row, nil
}

// Save 创建或更新服务器；version 为 0 表示新建
func (s *Store) Save(ctx context.Context, id uint, cfg ServerConfig, version int64, position int) (StoredServer, error) {
	if err := validateServerConfig(cfg); err != nil {
		return StoredServer{}, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return StoredServer{}, fmt.Errorf("序列化 MCP 配置失败: %w", err)
	}
	ciphertext, nonce, keyVersion, err := secrets.Encrypt(raw)
	if err != nil {
		return StoredServer{}, err
	}
	if id == 0 || version == 0 {
		row := StoredServer{
			Name: cfg.Name, Enabled: cfg.Enabled, Transport: cfg.Type,
			ConfigCipher: ciphertext, ConfigNonce: nonce, KeyVersion: keyVersion,
			Version: 1, Position: position,
		}
		if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
			return StoredServer{}, fmt.Errorf("创建 MCP 服务器失败: %w", err)
		}
		return row, nil
	}
	var row StoredServer
	result := s.db.WithContext(ctx).Model(&row).
		Where("id = ? AND version = ?", id, version).
		Updates(map[string]any{
			"name":              cfg.Name,
			"enabled":           cfg.Enabled,
			"transport":         cfg.Type,
			"config_ciphertext": ciphertext,
			"config_nonce":      nonce,
			"key_version":       keyVersion,
			"position":          position,
			"version":           gorm.Expr("version + 1"),
			"updated_at":        gorm.Expr("now()"),
		})
	if result.Error != nil {
		return StoredServer{}, fmt.Errorf("更新 MCP 服务器失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return StoredServer{}, fmt.Errorf("MCP 服务器已被其他页面修改，请刷新后重试")
	}
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return StoredServer{}, fmt.Errorf("读取 MCP 服务器失败: %w", err)
	}
	return row, nil
}

// Delete 删除服务器
func (s *Store) Delete(ctx context.Context, id uint, version int64) error {
	result := s.db.WithContext(ctx).Where("id = ? AND version = ?", id, version).Delete(&StoredServer{})
	if result.Error != nil {
		return fmt.Errorf("删除 MCP 服务器失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("MCP 服务器已被其他页面修改，请刷新后重试")
	}
	return nil
}

// EnabledConfigs 返回启用服务器的明文配置
func (s *Store) EnabledConfigs(ctx context.Context) ([]ServerConfig, error) {
	rows, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]ServerConfig, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		cfg, err := decryptServer(row)
		if err != nil {
			return nil, fmt.Errorf("解密 MCP 服务器 %q 失败: %w", row.Name, err)
		}
		result = append(result, cfg)
	}
	return result, nil
}

func decryptServer(row StoredServer) (ServerConfig, error) {
	raw, err := secrets.Decrypt(row.ConfigCipher, row.ConfigNonce, row.KeyVersion)
	if err != nil {
		return ServerConfig{}, err
	}
	var cfg ServerConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ServerConfig{}, fmt.Errorf("解析 MCP 配置失败: %w", err)
	}
	if strings.TrimSpace(cfg.Name) == "" {
		cfg.Name = row.Name
	}
	return cfg, nil
}

func validateServerConfig(cfg ServerConfig) error {
	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.Name == "" {
		return fmt.Errorf("MCP 服务器名称不能为空")
	}
	cfg.Type = strings.TrimSpace(cfg.Type)
	switch cfg.Type {
	case "sse":
		if strings.TrimSpace(cfg.URL) == "" {
			return fmt.Errorf("SSE 服务器 URL 不能为空")
		}
	case "stdio":
		if strings.TrimSpace(cfg.Command) == "" {
			return fmt.Errorf("stdio 服务器命令不能为空")
		}
	default:
		return fmt.Errorf("不支持的 MCP 传输类型 %q", cfg.Type)
	}
	return nil
}
