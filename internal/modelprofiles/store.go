package modelprofiles

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/secrets"

	"gorm.io/gorm"
)

// Profile 是数据库中的模型档案，API Key 只保存密文
type Profile struct {
	ID               uint      `gorm:"column:id;primaryKey" json:"id"`
	Name             string    `gorm:"column:name" json:"name"`
	Provider         string    `gorm:"column:provider" json:"provider"`
	BaseURL          string    `gorm:"column:base_url" json:"base_url"`
	ModelName        string    `gorm:"column:model_name" json:"model_name"`
	ContextWindow    int       `gorm:"column:context_window" json:"context_window"`
	ReasoningEffort  string    `gorm:"column:reasoning_effort" json:"reasoning_effort"`
	APIKeyCiphertext []byte    `gorm:"column:api_key_ciphertext" json:"-"`
	APIKeyNonce      []byte    `gorm:"column:api_key_nonce" json:"-"`
	APIKeyKeyVersion int       `gorm:"column:api_key_key_version" json:"-"`
	APIKeyHint       string    `gorm:"column:api_key_hint" json:"api_key_hint"`
	Version          int64     `gorm:"column:version" json:"version"`
	CreatedAt        time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (Profile) TableName() string { return "model_profiles" }

var (
	ErrNotFound        = errors.New("模型不存在")
	ErrActiveModel     = errors.New("当前模型不能删除")
	ErrInvalidModel    = errors.New("模型配置无效")
	ErrVersionConflict = errors.New("模型已被其他页面修改，请刷新后重试")
	reasoningEfforts   = map[string]bool{"": true, "none": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}
)

// Store 管理模型档案
type Store struct {
	db *gorm.DB
}

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// List 返回全部模型档案
func (s *Store) List(ctx context.Context) ([]Profile, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("模型存储未初始化")
	}
	var rows []Profile
	if err := s.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取模型列表失败: %w", err)
	}
	return rows, nil
}

// Get 按 ID 读取模型档案
func (s *Store) Get(ctx context.Context, id uint) (Profile, error) {
	var row Profile
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrNotFound
	}
	if err != nil {
		return row, fmt.Errorf("读取模型失败: %w", err)
	}
	return row, nil
}

// Create 新建尚未分配给场合的模型档案
func (s *Store) Create(ctx context.Context, row Profile, apiKey string) (Profile, error) {
	if err := validate(row, apiKey, true); err != nil {
		return row, err
	}
	ciphertext, nonce, version, err := secrets.EncryptString(strings.TrimSpace(apiKey))
	if err != nil {
		return row, err
	}
	row.Provider = "openai_responses"
	row.APIKeyCiphertext = ciphertext
	row.APIKeyNonce = nonce
	row.APIKeyKeyVersion = version
	row.APIKeyHint = keyHint(apiKey)
	row.Version = 1
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return row, fmt.Errorf("创建模型失败: %w", err)
	}
	return s.Get(ctx, row.ID)
}

// Update 修改模型；apiKey 为空表示保留旧 Key
func (s *Store) Update(ctx context.Context, row Profile, apiKey string) (Profile, error) {
	if row.ID == 0 {
		return row, ErrInvalidModel
	}
	if err := validate(row, apiKey, false); err != nil {
		return row, err
	}
	updates := map[string]any{
		"name":             strings.TrimSpace(row.Name),
		"base_url":         strings.TrimSpace(row.BaseURL),
		"model_name":       strings.TrimSpace(row.ModelName),
		"context_window":   row.ContextWindow,
		"reasoning_effort": row.ReasoningEffort,
		"version":          gorm.Expr("version + 1"),
		"updated_at":       gorm.Expr("now()"),
	}
	if strings.TrimSpace(apiKey) != "" {
		ciphertext, nonce, version, err := secrets.EncryptString(strings.TrimSpace(apiKey))
		if err != nil {
			return row, err
		}
		updates["api_key_ciphertext"] = ciphertext
		updates["api_key_nonce"] = nonce
		updates["api_key_key_version"] = version
		updates["api_key_hint"] = keyHint(apiKey)
	}
	result := s.db.WithContext(ctx).Model(&Profile{}).
		Where("id = ? AND version = ?", row.ID, row.Version).
		Updates(updates)
	if result.Error != nil {
		return row, fmt.Errorf("更新模型失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return row, ErrVersionConflict
	}
	return s.Get(ctx, row.ID)
}

// Delete 删除未被任何场合使用的模型
func (s *Store) Delete(ctx context.Context, id uint, expectedVersion int64) error {
	row, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	selection, err := s.Selection(ctx)
	if err != nil {
		return err
	}
	if selection.Uses(row.ID) {
		return ErrActiveModel
	}
	result := s.db.WithContext(ctx).Where("id = ? AND version = ?", id, expectedVersion).Delete(&Profile{})
	if result.Error != nil {
		return fmt.Errorf("删除模型失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrVersionConflict
	}
	return nil
}

// DecryptKey 解密模型 API Key
func (s *Store) DecryptKey(row Profile) (string, error) {
	return secrets.DecryptString(row.APIKeyCiphertext, row.APIKeyNonce, row.APIKeyKeyVersion)
}

func validate(row Profile, apiKey string, requireKey bool) error {
	if strings.TrimSpace(row.Name) == "" {
		return fmt.Errorf("%w: 名称不能为空", ErrInvalidModel)
	}
	if strings.TrimSpace(row.BaseURL) == "" {
		return fmt.Errorf("%w: Base URL 不能为空", ErrInvalidModel)
	}
	if strings.TrimSpace(row.ModelName) == "" {
		return fmt.Errorf("%w: 模型名不能为空", ErrInvalidModel)
	}
	if row.ContextWindow <= 0 {
		return fmt.Errorf("%w: 上下文窗口必须大于 0", ErrInvalidModel)
	}
	if !reasoningEfforts[row.ReasoningEffort] {
		return fmt.Errorf("%w: reasoning.effort 无效", ErrInvalidModel)
	}
	if requireKey && strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("%w: API Key 不能为空", ErrInvalidModel)
	}
	return nil
}

func keyHint(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if len(apiKey) <= 4 {
		return "••••"
	}
	return "••••" + apiKey[len(apiKey)-4:]
}
