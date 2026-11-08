package modelprofiles

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
)

const SelectionKey = "model_selection"

// Selection 保存三个场合各自使用的档案，0 表示尚未配置
type Selection struct {
	QQ      uint  `json:"qq"`
	Life    uint  `json:"life"`
	Worker  uint  `json:"worker"`
	Version int64 `json:"version"`
}

func (s Selection) ID(role string) uint {
	switch role {
	case "qq":
		return s.QQ
	case "life":
		return s.Life
	case "worker":
		return s.Worker
	}
	return 0
}

func (s Selection) Uses(id uint) bool { return s.QQ == id || s.Life == id || s.Worker == id }

// Selection 读取三个场合的独立档案选择
func (s *Store) Selection(ctx context.Context) (Selection, error) {
	var row struct {
		ConfigValue string
		Version     int64
	}
	err := s.db.WithContext(ctx).Raw("SELECT config_value::text AS config_value,version FROM app_config WHERE config_key=?", SelectionKey).Scan(&row).Error
	if err != nil {
		return Selection{}, err
	}
	if row.Version > 0 {
		var selection Selection
		if err := sonic.UnmarshalString(row.ConfigValue, &selection); err != nil {
			return Selection{}, err
		}
		selection.Version = row.Version
		return selection, nil
	}
	return Selection{}, nil
}

func (s *Store) Select(ctx context.Context, selection Selection) (Selection, error) {
	raw, err := sonic.MarshalString(Selection{QQ: selection.QQ, Life: selection.Life, Worker: selection.Worker})
	if err != nil {
		return Selection{}, err
	}
	var result *gorm.DB
	if selection.Version == 0 {
		result = s.db.WithContext(ctx).Exec(`INSERT INTO app_config(config_key,config_value,version,updated_at) VALUES(?,?::jsonb,1,now()) ON CONFLICT(config_key) DO NOTHING`, SelectionKey, raw)
	} else {
		result = s.db.WithContext(ctx).Exec(`UPDATE app_config SET config_value=?::jsonb,version=version+1,updated_at=now() WHERE config_key=? AND version=?`, raw, SelectionKey, selection.Version)
	}
	if result.Error != nil {
		return Selection{}, fmt.Errorf("保存场合模型失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return Selection{}, ErrVersionConflict
	}
	selection.Version++
	return selection, nil
}

func (s *Store) ForRole(ctx context.Context, role string) (Profile, error) {
	selection, err := s.Selection(ctx)
	if err != nil {
		return Profile{}, err
	}
	id := selection.ID(role)
	if id == 0 {
		return Profile{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Store) ClearSelection(ctx context.Context, version int64) error {
	result := s.db.WithContext(ctx).Exec("DELETE FROM app_config WHERE config_key=? AND version=?", SelectionKey, version)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrVersionConflict
	}
	return nil
}
