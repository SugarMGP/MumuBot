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

type MemoryStatus string

const (
	MemoryStatusActive   MemoryStatus = "active"
	MemoryStatusArchived MemoryStatus = "archived"
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

type Memory struct {
	ID            uint            `gorm:"primaryKey" json:"id"`
	GroupID       int64           `gorm:"not null;index" json:"group_id"`
	SubjectUserID int64           `gorm:"not null;default:0;index" json:"subject_user_id"`
	Kind          MemoryKind      `gorm:"type:text;not null;index" json:"kind"`
	Status        MemoryStatus    `gorm:"type:text;not null;index" json:"status"`
	Content       string          `gorm:"type:text;not null" json:"content"`
	Embedding     pgvector.Vector `gorm:"type:vector;not null" json:"-"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func (Memory) TableName() string { return "memories" }

func NormalizeContent(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

type MemoryEvidence struct {
	MemoryID     uint `gorm:"primaryKey" json:"memory_id"`
	MessageLogID uint `gorm:"primaryKey" json:"message_log_id"`
}

func (MemoryEvidence) TableName() string { return "memory_evidence" }

type MessageLog struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	OneBotMessageID  int64      `gorm:"not null" json:"onebot_message_id"`
	GroupID          int64      `gorm:"index;not null" json:"group_id"`
	UserID           int64      `gorm:"index;not null" json:"user_id"`
	Nickname         string     `gorm:"type:text;not null" json:"nickname"`
	TextContent      string     `gorm:"type:text;not null" json:"text_content"`
	DisplayContent   string     `gorm:"type:text;not null" json:"display_content"`
	ForwardPayload   *string    `gorm:"type:jsonb" json:"forward_payload,omitempty"`
	ReplyToMessageID *int64     `json:"reply_to_message_id,omitempty"`
	IsMentioned      bool       `gorm:"not null" json:"is_mentioned"`
	MessageTime      time.Time  `gorm:"index;not null" json:"message_time"`
	RecalledAt       *time.Time `json:"recalled_at,omitempty"`
}

func (MessageLog) TableName() string { return "message_logs" }

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
	Claims       []MemoryClaim      `json:"claims"`
	Participants []TopicParticipant `json:"participants"`
	OpenLoops    []string           `json:"open_loops"`
	RecentTurns  []string           `json:"recent_turns"`
	Keywords     []string           `json:"keywords"`
}

type TopicSummaryRecord struct {
	ID                       uint            `gorm:"primaryKey" json:"id"`
	ThroughTopicAssignmentID uint            `gorm:"uniqueIndex;not null" json:"through_topic_assignment_id"`
	SummaryJSON              string          `gorm:"type:jsonb;not null" json:"summary_json"`
	Embedding                pgvector.Vector `gorm:"type:vector;not null" json:"-"`
	MemoryProcessed          bool            `gorm:"not null;default:false" json:"memory_processed"`
	CreatedAt                time.Time       `json:"created_at"`
}

func (TopicSummaryRecord) TableName() string { return "topic_summaries" }

type StylePatternStatus string

const (
	StylePatternStatusCandidate StylePatternStatus = "candidate"
	StylePatternStatusActive    StylePatternStatus = "active"
	StylePatternStatusRejected  StylePatternStatus = "rejected"
)

type StylePattern struct {
	ID         uint               `gorm:"primaryKey" json:"id"`
	GroupID    int64              `gorm:"index;not null" json:"group_id"`
	Situation  string             `gorm:"type:text;not null" json:"situation"`
	Expression string             `gorm:"type:text;not null" json:"expression"`
	Status     StylePatternStatus `gorm:"type:text;not null;index" json:"status"`
	Embedding  pgvector.Vector    `gorm:"type:vector;not null" json:"-"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

func (StylePattern) TableName() string { return "style_patterns" }

type StylePatternEvidence struct {
	StylePatternID uint `gorm:"primaryKey" json:"style_pattern_id"`
	MessageLogID   uint `gorm:"primaryKey" json:"message_log_id"`
}

func (StylePatternEvidence) TableName() string { return "style_pattern_evidence" }

type CultureStatus string

const (
	CultureStatusCandidate CultureStatus = "candidate"
	CultureStatusActive    CultureStatus = "active"
	CultureStatusRejected  CultureStatus = "rejected"
)

type Jargon struct {
	ID        uint          `gorm:"primaryKey" json:"id"`
	GroupID   int64         `gorm:"index;not null" json:"group_id"`
	Term      string        `gorm:"type:text;not null" json:"term"`
	Meaning   string        `gorm:"type:text;not null" json:"meaning"`
	Status    CultureStatus `gorm:"type:text;not null;index" json:"status"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

func (Jargon) TableName() string { return "jargons" }

type JargonEvidence struct {
	JargonID     uint `gorm:"primaryKey" json:"jargon_id"`
	MessageLogID uint `gorm:"primaryKey" json:"message_log_id"`
}

func (JargonEvidence) TableName() string { return "jargon_evidence" }

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

type MemberTrait struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    int64     `gorm:"index;not null" json:"user_id"`
	Kind      string    `gorm:"type:text;not null" json:"kind"`
	Value     string    `gorm:"type:text;not null" json:"value"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (MemberTrait) TableName() string { return "member_traits" }

type MemberTraitEvidence struct {
	MemberTraitID uint `gorm:"primaryKey" json:"member_trait_id"`
	MessageLogID  uint `gorm:"primaryKey" json:"message_log_id"`
}

func (MemberTraitEvidence) TableName() string { return "member_trait_evidence" }

type LearningKind string

const (
	LearningKindCulture       LearningKind = "culture"
	LearningKindMemberProfile LearningKind = "member_profile"
)

type LearningState struct {
	GroupID          int64        `gorm:"primaryKey" json:"group_id"`
	Kind             LearningKind `gorm:"primaryKey;type:text" json:"kind"`
	LastMessageLogID uint         `gorm:"not null" json:"last_message_log_id"`
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
