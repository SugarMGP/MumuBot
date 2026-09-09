package memory

import (
	"strings"
	"time"

	pgvector "github.com/pgvector/pgvector-go"
)

type MemoryKind string

const (
	MemoryKindFact       MemoryKind = "fact"
	MemoryKindEpisode    MemoryKind = "episode"
	MemoryKindPreference MemoryKind = "preference"
	MemoryKindConstraint MemoryKind = "constraint"
	MemoryKindGoal       MemoryKind = "goal"
)

const SubjectSelfInputID int64 = -1

type RawMemoryClaim struct {
	SubjectUserID      *int64  `json:"subject_user_id" jsonschema:"description=记忆主体；-1 表示机器人自身，0 表示群组，正数表示成员 QQ"`
	Kind               string  `json:"kind" jsonschema:"enum=fact,enum=episode,enum=preference,enum=constraint,enum=goal"`
	Content            string  `json:"content" jsonschema:"description=包含当前昵称且脱离原句仍可理解的完整自然语言命题"`
	EvidenceMessageIDs []int64 `json:"evidence_message_ids" jsonschema:"description=1 到 8 条原始证据消息 ID"`
}

type MemoryClaim struct {
	SubjectUserID      int64      `json:"subject_user_id"`
	Kind               MemoryKind `json:"kind"`
	Content            string     `json:"content"`
	EvidenceMessageIDs []int64    `json:"evidence_message_ids"`
}

func NormalizeContent(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

type MessageLog struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	OneBotMessageID  int64      `gorm:"not null" json:"onebot_message_id"`
	GroupID          int64      `gorm:"index;not null" json:"group_id"`
	UserID           int64      `gorm:"index;not null" json:"user_id"`
	Nickname         string     `gorm:"type:text;not null" json:"nickname"`
	TextContent      string     `gorm:"type:text;not null" json:"text_content"`
	DisplayContent   string     `gorm:"type:text;not null" json:"display_content"`
	ReplyToMessageID *int64     `json:"reply_to_message_id,omitempty"`
	IsMentioned      bool       `gorm:"not null" json:"is_mentioned"`
	MessageTime      time.Time  `gorm:"index;not null" json:"message_time"`
	RecalledAt       *time.Time `json:"recalled_at,omitempty"`
}

func (MessageLog) TableName() string { return "message_logs" }

const RecalledMessageDisplayContent = "【消息已撤回】"

type TopicThread struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	GroupID   int64     `gorm:"index;not null" json:"group_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (TopicThread) TableName() string { return "topic_threads" }

type TopicAssignment struct {
	ID           uint  `gorm:"primaryKey" json:"id"`
	MessageLogID uint  `gorm:"uniqueIndex;not null" json:"message_log_id"`
	TopicID      *uint `gorm:"index" json:"topic_id,omitempty"`
}

func (TopicAssignment) TableName() string { return "topic_assignments" }

type TopicParticipant struct {
	UserID   int64  `json:"user_id"`
	Nickname string `json:"nickname"`
	Position string `json:"position"`
}

type TopicSummary struct {
	Version      int                `json:"version"`
	Title        string             `json:"title"`
	Gist         string             `json:"gist"`
	Participants []TopicParticipant `json:"participants"`
	OpenLoops    []string           `json:"open_loops"`
	RecentTurns  []string           `json:"recent_turns"`
	Keywords     []string           `json:"keywords"`
}

type TopicSummaryRecord struct {
	ID                       uint             `gorm:"primaryKey" json:"id"`
	ThroughTopicAssignmentID uint             `gorm:"uniqueIndex;not null" json:"through_topic_assignment_id"`
	SummaryJSON              string           `gorm:"type:jsonb;not null" json:"summary_json"`
	Embedding                *pgvector.Vector `gorm:"type:vector" json:"-"`
	CreatedAt                time.Time        `json:"created_at"`
}

func (TopicSummaryRecord) TableName() string { return "topic_summaries" }

type MemberProfile struct {
	UserID       int64     `gorm:"primaryKey" json:"user_id"`
	Nickname     string    `gorm:"type:text;not null" json:"nickname"`
	LastSeenAt   time.Time `gorm:"not null" json:"last_seen_at"`
	MessageCount int64     `gorm:"not null" json:"message_count"`
}

func (MemberProfile) TableName() string { return "member_profiles" }

type MemberName struct {
	UserID    int64     `gorm:"primaryKey" json:"user_id"`
	GroupID   int64     `gorm:"primaryKey" json:"group_id"`
	Value     string    `gorm:"primaryKey;type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (MemberName) TableName() string { return "member_names" }

type LearningState struct {
	GroupID          int64 `gorm:"primaryKey" json:"group_id"`
	LastMessageLogID uint  `gorm:"not null" json:"last_message_log_id"`
}

func (LearningState) TableName() string { return "learning_states" }

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
	Valence     float64   `gorm:"not null;default:0" json:"valence"`
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
