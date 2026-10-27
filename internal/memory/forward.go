package memory

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// ForwardNode 合并转发里的一条消息
type ForwardNode struct {
	Sender  string   `json:"sender,omitempty"`
	Content string   `json:"content"`
	Nested  []string `json:"nested,omitempty"` // 嵌套转发的句柄
}

// ForwardNodes 转发消息列表，落库为 jsonb
type ForwardNodes []ForwardNode

// Value 把转发消息列表写成 jsonb
func (n ForwardNodes) Value() (driver.Value, error) {
	if len(n) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(n)
}

// Scan 从 jsonb 读出转发消息列表
func (n *ForwardNodes) Scan(src any) error {
	if src == nil {
		*n = nil
		return nil
	}
	var raw []byte
	switch value := src.(type) {
	case []byte:
		raw = value
	case string:
		raw = []byte(value)
	default:
		return errors.New("无法解析合并转发内容")
	}
	if len(raw) == 0 {
		*n = nil
		return nil
	}
	return json.Unmarshal(raw, n)
}

// SaveMessageForwards 写入合并转发内容，父级必须先于嵌套写入
func SaveMessageForwards(tx *gorm.DB, forwards []MessageForward) error {
	if len(forwards) == 0 {
		return nil
	}
	if err := tx.Create(&forwards).Error; err != nil {
		return err
	}
	return nil
}

// GetMessageForward 按句柄取合并转发内容
func (m *Manager) GetMessageForward(ctx context.Context, handle string) (*MessageForward, error) {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var forward MessageForward
	if err := m.db.WithContext(ctx).Where("handle = ?", handle).First(&forward).Error; err != nil {
		return nil, err
	}
	return &forward, nil
}
