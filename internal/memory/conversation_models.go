package memory

import "time"

const (
	ConversationKindGroup   = "group"
	ConversationKindPrivate = "private"
)

// ConversationTarget 保存会话的后台管理状态和 OneBot 联系人镜像
type ConversationTarget struct {
	ConversationKind string     `gorm:"primaryKey;type:text" json:"conversation_kind"`
	TargetID         int64      `gorm:"primaryKey;column:target_id" json:"target_id"`
	Name             string     `gorm:"type:text;not null;default:''" json:"name"`
	RemoteRemark     string     `gorm:"type:text;not null;default:''" json:"remote_remark"`
	Blocked          bool       `gorm:"not null;default:false" json:"blocked"`
	ExtraPrompt      string     `gorm:"type:text;not null;default:''" json:"extra_prompt"`
	Active           bool       `gorm:"not null;default:true" json:"active"`
	LastSeenAt       *time.Time `json:"last_seen_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func (ConversationTarget) TableName() string { return "conversation_targets" }

// FriendRequest 保存好友申请，flag 是 OneBot 处理凭据，不作为申请去重键
type FriendRequest struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Flag       string    `gorm:"type:text;not null" json:"flag"`
	UserID     int64     `gorm:"index;not null" json:"user_id"`
	Nickname   string    `gorm:"type:text;not null;default:''" json:"nickname"`
	Comment    string    `gorm:"type:text;not null;default:''" json:"comment"`
	Status     string    `gorm:"type:text;not null;default:'pending'" json:"status"`
	ReceivedAt time.Time `gorm:"not null" json:"received_at"`
}

func (FriendRequest) TableName() string { return "friend_requests" }

// PrivateTopicState 保存每个好友唯一的线性私聊话题摘要
type PrivateTopicState struct {
	TargetID            int64     `gorm:"primaryKey;column:target_id" json:"target_id"`
	SummaryJSON         string    `gorm:"type:jsonb;not null" json:"summary_json"`
	ThroughMessageLogID uint      `gorm:"not null;default:0" json:"through_message_log_id"`
	UpdatedAt           time.Time `json:"updated_at"`
	SourceMessageIDs    []uint    `gorm:"-" json:"-"`
	SourcesValid        bool      `gorm:"-" json:"-"`
}

func (PrivateTopicState) TableName() string { return "private_topic_states" }
