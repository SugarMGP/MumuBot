package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	// PersonaPresetBuiltinName 是内置预设的固定名称，不允许改名
	PersonaPresetBuiltinName = "内置预设"
)

var (
	// ErrPersonaPresetBuiltin 表示该预设是内置预设
	ErrPersonaPresetBuiltin = errors.New("内置预设不能删除")
	// ErrPersonaPresetActive 表示该预设正在使用
	ErrPersonaPresetActive = errors.New("正在使用的预设不能删除")
	// ErrPersonaPresetVersion 表示版本冲突
	ErrPersonaPresetVersion = errors.New("预设已被其他人修改，请刷新后重试")
	// ErrPersonaPresetNotFound 表示预设不存在
	ErrPersonaPresetNotFound = errors.New("预设不存在")
	// ErrPersonaPresetInvalid 表示预设内容不合法
	ErrPersonaPresetInvalid = errors.New("人格预设内容无效")
	// ErrPersonaPresetDuplicateName 表示预设名称重复
	ErrPersonaPresetDuplicateName = errors.New("已经有同名预设了")
)

// PersonaPreset 是一套人格预设：人格卡与仿真提示词一起切换
type PersonaPreset struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"type:text;not null" json:"name"`
	Description string    `gorm:"type:text;not null" json:"description"`
	PersonaCard string    `gorm:"column:persona_card;type:text;not null" json:"persona_card"`
	Simulation  string    `gorm:"column:simulation_prompt;type:text;not null" json:"simulation_prompt"`
	LifePrompt  string    `gorm:"column:life_prompt;type:text;not null;default:''" json:"life_prompt"`
	Builtin     bool      `gorm:"not null" json:"builtin"`
	Active      bool      `gorm:"not null" json:"active"`
	Version     int64     `gorm:"not null" json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName 指定预设表名
func (PersonaPreset) TableName() string { return "persona_presets" }

// ListPersonaPresets 返回全部预设，内置排在最前
func (m *Manager) ListPersonaPresets(ctx context.Context) ([]PersonaPreset, error) {
	rows := make([]PersonaPreset, 0)
	err := m.db.WithContext(ctx).
		Order("builtin DESC, updated_at DESC, id DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("读取人格预设失败: %w", err)
	}
	return rows, nil
}

// GetPersonaPreset 按 ID 读取预设
func (m *Manager) GetPersonaPreset(ctx context.Context, id uint) (*PersonaPreset, error) {
	var row PersonaPreset
	err := m.db.WithContext(ctx).Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取人格预设失败: %w", err)
	}
	return &row, nil
}

// GetActivePersonaPreset 读取当前使用的预设
func (m *Manager) GetActivePersonaPreset(ctx context.Context) (*PersonaPreset, error) {
	var row PersonaPreset
	err := m.db.WithContext(ctx).Where("active").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取当前人格预设失败: %w", err)
	}
	return &row, nil
}

// CreatePersonaPreset 新建预设，名字重复时报错
func (m *Manager) CreatePersonaPreset(ctx context.Context, preset PersonaPreset) (*PersonaPreset, error) {
	preset.Name = strings.TrimSpace(preset.Name)
	preset.Description = strings.TrimSpace(preset.Description)
	if preset.Name == "" {
		return nil, fmt.Errorf("%w: 预设名称不能为空", ErrPersonaPresetInvalid)
	}
	if strings.TrimSpace(preset.PersonaCard) == "" || strings.TrimSpace(preset.Simulation) == "" || strings.TrimSpace(preset.LifePrompt) == "" {
		return nil, fmt.Errorf("%w: 人格卡、仿真提示词与生活职责都不能为空", ErrPersonaPresetInvalid)
	}
	var exists int64
	if err := m.db.WithContext(ctx).Model(&PersonaPreset{}).Where("name = ?", preset.Name).Count(&exists).Error; err != nil {
		return nil, fmt.Errorf("检查预设名称失败: %w", err)
	}
	if exists > 0 {
		return nil, ErrPersonaPresetDuplicateName
	}
	preset.Builtin = false
	preset.Active = false
	preset.Version = 1
	if err := m.db.WithContext(ctx).Create(&preset).Error; err != nil {
		return nil, fmt.Errorf("保存人格预设失败: %w", err)
	}
	return &preset, nil
}

// UpdatePersonaPreset 保存预设内容，内置预设只允许改说明以外的东西由后台拦截
func (m *Manager) UpdatePersonaPreset(ctx context.Context, id uint, name, description, card, simulation, lifePrompt string, expectedVersion int64) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: 预设名称不能为空", ErrPersonaPresetInvalid)
	}
	if strings.TrimSpace(card) == "" || strings.TrimSpace(simulation) == "" || strings.TrimSpace(lifePrompt) == "" {
		return fmt.Errorf("%w: 人格卡、仿真提示词与生活职责都不能为空", ErrPersonaPresetInvalid)
	}
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row PersonaPreset
		if err := tx.Where("id = ?", id).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPersonaPresetNotFound
			}
			return fmt.Errorf("读取人格预设失败: %w", err)
		}
		if row.Builtin {
			return fmt.Errorf("%w: 内置预设不能编辑，请先克隆一份", ErrPersonaPresetInvalid)
		}
		if expectedVersion > 0 && row.Version != expectedVersion {
			return ErrPersonaPresetVersion
		}
		if name != row.Name {
			var exists int64
			if err := tx.Model(&PersonaPreset{}).Where("name = ? AND id <> ?", name, id).Count(&exists).Error; err != nil {
				return fmt.Errorf("检查预设名称失败: %w", err)
			}
			if exists > 0 {
				return ErrPersonaPresetDuplicateName
			}
		}
		result := tx.Model(&PersonaPreset{}).
			Where("id = ? AND version = ?", id, row.Version).
			Updates(map[string]any{
				"name":              name,
				"description":       strings.TrimSpace(description),
				"persona_card":      card,
				"simulation_prompt": simulation,
				"life_prompt":       lifePrompt,
				"version":           row.Version + 1,
				"updated_at":        time.Now(),
			})
		if result.Error != nil {
			return fmt.Errorf("保存人格预设失败: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrPersonaPresetVersion
		}
		return nil
	})
}

// ActivatePersonaPreset 切换当前使用的预设
func (m *Manager) ActivatePersonaPreset(ctx context.Context, id uint) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row PersonaPreset
		if err := tx.Where("id = ?", id).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPersonaPresetNotFound
			}
			return fmt.Errorf("读取人格预设失败: %w", err)
		}
		if err := tx.Model(&PersonaPreset{}).Where("active").Update("active", false).Error; err != nil {
			return fmt.Errorf("切换人格预设失败: %w", err)
		}
		if err := tx.Model(&PersonaPreset{}).Where("id = ?", id).Update("active", true).Error; err != nil {
			return fmt.Errorf("切换人格预设失败: %w", err)
		}
		return nil
	})
}

// DeletePersonaPreset 删除预设，内置与正在使用的不允许删除
func (m *Manager) DeletePersonaPreset(ctx context.Context, id uint) error {
	var row PersonaPreset
	if err := m.db.WithContext(ctx).Where("id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrPersonaPresetNotFound
		}
		return fmt.Errorf("读取人格预设失败: %w", err)
	}
	if row.Builtin {
		return ErrPersonaPresetBuiltin
	}
	if row.Active {
		return ErrPersonaPresetActive
	}
	if err := m.db.WithContext(ctx).Where("id = ?", id).Delete(&PersonaPreset{}).Error; err != nil {
		return fmt.Errorf("删除人格预设失败: %w", err)
	}
	return nil
}

// ClonePersonaPreset 复制一份预设，名字自动加"副本"
func (m *Manager) ClonePersonaPreset(ctx context.Context, id uint) (*PersonaPreset, error) {
	source, err := m.GetPersonaPreset(ctx, id)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, ErrPersonaPresetNotFound
	}
	names, err := m.personaPresetNames(ctx)
	if err != nil {
		return nil, err
	}
	base := source.Name + " 副本"
	name := base
	for i := 2; ; i++ {
		if _, ok := names[name]; !ok {
			break
		}
		name = fmt.Sprintf("%s %d", base, i)
	}
	clone := PersonaPreset{
		Name:        name,
		Description: source.Description,
		PersonaCard: source.PersonaCard,
		Simulation:  source.Simulation,
		LifePrompt:  source.LifePrompt,
		Version:     1,
	}
	if err := m.db.WithContext(ctx).Create(&clone).Error; err != nil {
		return nil, fmt.Errorf("克隆人格预设失败: %w", err)
	}
	return &clone, nil
}

func (m *Manager) personaPresetNames(ctx context.Context) (map[string]struct{}, error) {
	var names []string
	if err := m.db.WithContext(ctx).Model(&PersonaPreset{}).Pluck("name", &names).Error; err != nil {
		return nil, fmt.Errorf("读取预设名称失败: %w", err)
	}
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result, nil
}

// EnsureBuiltinPersonaPreset 用代码里的文本维护内置预设：没有就建，有就同步
func (m *Manager) EnsureBuiltinPersonaPreset(ctx context.Context, card, simulation, lifePrompt string) (*PersonaPreset, error) {
	card = strings.TrimSpace(card)
	simulation = strings.TrimSpace(simulation)
	lifePrompt = strings.TrimSpace(lifePrompt)
	if card == "" || simulation == "" || lifePrompt == "" {
		return nil, fmt.Errorf("内置预设内容为空")
	}
	var row PersonaPreset
	err := m.db.WithContext(ctx).Where("builtin").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var activeCount int64
		if err := m.db.WithContext(ctx).Model(&PersonaPreset{}).Where("active").Count(&activeCount).Error; err != nil {
			return nil, fmt.Errorf("检查当前预设失败: %w", err)
		}
		row = PersonaPreset{
			Name:        PersonaPresetBuiltinName,
			Description: "项目自带的人格预设，随版本更新，不可编辑；需要修改请克隆一份。",
			PersonaCard: card,
			Simulation:  simulation,
			LifePrompt:  lifePrompt,
			Builtin:     true,
			Active:      activeCount == 0,
			Version:     1,
		}
		if err := m.db.WithContext(ctx).Create(&row).Error; err != nil {
			return nil, fmt.Errorf("创建内置预设失败: %w", err)
		}
		return &row, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取内置预设失败: %w", err)
	}
	if strings.TrimSpace(row.PersonaCard) == card && strings.TrimSpace(row.Simulation) == simulation && strings.TrimSpace(row.LifePrompt) == lifePrompt {
		return &row, nil
	}
	if err := m.db.WithContext(ctx).Model(&PersonaPreset{}).Where("id = ?", row.ID).Updates(map[string]any{
		"name":              PersonaPresetBuiltinName,
		"persona_card":      card,
		"simulation_prompt": simulation,
		"life_prompt":       lifePrompt,
		"version":           row.Version + 1,
		"updated_at":        time.Now(),
	}).Error; err != nil {
		return nil, fmt.Errorf("更新内置预设失败: %w", err)
	}
	row.PersonaCard = card
	row.Simulation = simulation
	row.LifePrompt = lifePrompt
	return &row, nil
}
