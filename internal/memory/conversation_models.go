package memory

import "time"

const (
	ConversationKindGroup   = "group"
	ConversationKindPrivate = "private"
)

// ConversationTarget 保存会话的后台管理状态和 OneBot 联系人镜像
type ConversationTarget struct {
	ConversationKind string    `gorm:"primaryKey;type:text" json:"conversation_kind"`
	TargetID         int64     `gorm:"primaryKey;column:target_id" json:"target_id"`
	Name             string    `gorm:"type:text;not null;default:''" json:"name"`
	RemoteRemark     string    `gorm:"type:text;not null;default:''" json:"remote_remark"`
	Blocked          bool      `gorm:"not null;default:false" json:"blocked"`
	ExtraPrompt      string    `gorm:"type:text;not null;default:''" json:"extra_prompt"`
	Active           bool      `gorm:"not null;default:true" json:"active"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (ConversationTarget) TableName() string { return "conversation_targets" }
