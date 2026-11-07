// Package filemeta 保存 QQ 文件消息的定位元信息
package filemeta

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
)

// Ref 是一条真实文件消息的底层定位信息
type Ref struct {
	FileID      string `json:"file_id"`
	Name        string `json:"name,omitempty"`
	Size        *int64 `json:"size,omitempty"`
	FileHash    string `json:"file_hash,omitempty"`
	BusID       *int64 `json:"busid,omitempty"`
	SnapshotRef string `json:"snapshot_ref,omitempty"` // file_ref 或 image_ref
}

// Refs 是消息附件列表，落库为 jsonb
type Refs []Ref

// Value 把附件列表写成 jsonb
func (r Refs) Value() (driver.Value, error) {
	if len(r) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(r)
}

// Scan 从 jsonb 读出附件列表
func (r *Refs) Scan(src any) error {
	if src == nil {
		*r = nil
		return nil
	}
	var raw []byte
	switch value := src.(type) {
	case []byte:
		raw = value
	case string:
		raw = []byte(value)
	default:
		return errors.New("无法解析文件附件")
	}
	if len(raw) == 0 {
		*r = nil
		return nil
	}
	return json.Unmarshal(raw, r)
}
