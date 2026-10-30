package memory

import (
	"strings"
	"time"

	"mumu-bot/internal/media"
)

// NormalizeContent 统一记忆去重时的空白和大小写
func NormalizeContent(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

type MessageLog struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	OneBotMessageID  int64      `gorm:"not null" json:"onebot_message_id"`
	ConversationKind string     `gorm:"type:text;not null" json:"conversation_kind"`
	TargetID         int64      `gorm:"column:target_id;index;not null" json:"target_id"`
	UserID           int64      `gorm:"index;not null" json:"user_id"`
	Nickname         string     `gorm:"type:text;not null" json:"nickname"`
	TextContent      string     `gorm:"type:text;not null" json:"text_content"`
	DisplayContent   string     `gorm:"type:text;not null" json:"display_content"`
	ReplyToMessageID *int64     `json:"reply_to_message_id,omitempty"`
	IsMentioned      bool       `gorm:"not null" json:"is_mentioned"`
	MessageTime      time.Time  `gorm:"index;not null" json:"message_time"`
	RecalledAt       *time.Time `json:"recalled_at,omitempty"`
	MediaRefs        media.Refs `gorm:"column:media_refs;type:jsonb" json:"media_refs,omitempty"`
}

func (MessageLog) TableName() string { return "message_logs" }

// MessageForward 一份合并转发的内容，句柄出现在消息正文里
type MessageForward struct {
	ID           uint         `gorm:"primaryKey" json:"id"`
	Handle       string       `gorm:"column:handle;type:text;not null;uniqueIndex" json:"handle"`
	MessageLogID uint         `gorm:"column:message_log_id;index;not null" json:"message_log_id"`
	ParentHandle string       `gorm:"column:parent_handle;type:text;index" json:"parent_handle,omitempty"`
	Nodes        ForwardNodes `gorm:"column:nodes;type:jsonb;not null" json:"nodes"`
	CreatedAt    time.Time    `gorm:"column:created_at;not null" json:"created_at"`
}

func (MessageForward) TableName() string { return "message_forwards" }

const RecalledMessageDisplayContent = "【消息已撤回】"

type TopicAssignment struct {
	ID           uint  `gorm:"primaryKey" json:"id"`
	MessageLogID uint  `gorm:"uniqueIndex;not null" json:"message_log_id"`
	TopicID      *uint `gorm:"index" json:"topic_id,omitempty"`
}

func (TopicAssignment) TableName() string { return "topic_assignments" }

type TopicParticipant struct {
	UserID   int64  `json:"user_id"`
	Nickname string `json:"nickname"`
}

type TopicSummary struct {
	Title        string             `json:"title"`
	Gist         string             `json:"gist"`
	Participants []TopicParticipant `json:"participants"`
	OpenLoops    []string           `json:"open_loops"`
	RecentTurns  []string           `json:"recent_turns"`
	Keywords     []string           `json:"keywords"`
}

type MemberProfile struct {
	UserID    int64     `gorm:"primaryKey" json:"user_id"`
	Nickname  string    `gorm:"type:text;not null" json:"nickname"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (MemberProfile) TableName() string { return "member_profiles" }

type MemberName struct {
	UserID    int64     `gorm:"primaryKey" json:"user_id"`
	GroupID   int64     `gorm:"primaryKey" json:"group_id"`
	Value     string    `gorm:"primaryKey;type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (MemberName) TableName() string { return "member_names" }

type Sticker struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	FileName    string    `gorm:"type:text;not null" json:"file_name"`
	FileHash    string    `gorm:"type:text;uniqueIndex;not null" json:"file_hash"`
	Description string    `gorm:"type:text" json:"description"`
	UseCount    int       `gorm:"not null;default:0" json:"use_count"`
}

func (Sticker) TableName() string { return "stickers" }

type MoodState struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	UpdatedAt   time.Time `json:"updated_at"`
	Energy      float64   `gorm:"not null;default:0.5" json:"energy"`
	Sociability float64   `gorm:"not null;default:0.5" json:"sociability"`
	LastReason  string    `gorm:"type:text" json:"last_reason,omitempty"`
}

func (MoodState) TableName() string { return "mood_state" }

type SchemaMigration struct {
	Version   int       `gorm:"primaryKey" json:"version"`
	Name      string    `gorm:"type:text;not null" json:"name"`
	AppliedAt time.Time `gorm:"not null;default:now()" json:"applied_at"`
}

func (SchemaMigration) TableName() string { return "schema_migrations" }
