package memory

import (
	"encoding/json"
	"time"
)

const (
	SubjectGroup  = "group"
	SubjectUser   = "user"
	SubjectSelf   = "self"
	SubjectObject = "object"

	MemoryProfile    = "profile"
	MemoryExperience = "experience"
)

// ConversationSession 保存单个会话的单调序号，消息编号和可见序号都在事务内分配
type ConversationSession struct {
	ConversationKind string    `gorm:"primaryKey;type:text" json:"conversation_kind"`
	TargetID         int64     `gorm:"primaryKey;column:target_id" json:"target_id"`
	NextInputSeq     uint64    `json:"next_input_seq"`
	NextVisibleSeq   uint64    `json:"next_visible_seq"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (ConversationSession) TableName() string { return "conversation_sessions" }

// TranscriptEntry 是当前工作集的一行，压缩后对应的旧行会删除
type TranscriptEntry struct {
	ID               uint            `gorm:"primaryKey" json:"id"`
	ConversationKind string          `gorm:"type:text;not null" json:"conversation_kind"`
	TargetID         int64           `gorm:"column:target_id;not null" json:"target_id"`
	InputSeq         *uint64         `json:"input_seq,omitempty"`
	VisibleSeq       *uint64         `json:"visible_seq,omitempty"`
	EntryKind        string          `gorm:"type:text;not null" json:"entry_kind"`
	MessageLogID     *uint           `json:"message_log_id,omitempty"`
	RunID            string          `gorm:"type:text;not null;default:''" json:"run_id"`
	ToolCallID       string          `gorm:"type:text;not null;default:''" json:"tool_call_id"`
	ToolName         string          `gorm:"type:text;not null;default:''" json:"tool_name"`
	ToolStatus       string          `gorm:"type:text;not null;default:''" json:"tool_status"`
	Payload          json.RawMessage `gorm:"type:jsonb;not null" json:"payload"`
	ResultRef        string          `gorm:"type:text;not null;default:''" json:"result_ref,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
}

func (TranscriptEntry) TableName() string { return "conversation_transcript" }

// CompactionCheckpoint 保存当前可见上下文的摘要快照
type CompactionCheckpoint struct {
	ConversationKind    string          `gorm:"primaryKey;type:text" json:"conversation_kind"`
	TargetID            int64           `gorm:"primaryKey;column:target_id" json:"target_id"`
	Version             uint64          `gorm:"not null;default:0" json:"version"`
	ThroughVisibleSeq   *uint64         `json:"through_visible_seq,omitempty"`
	ThroughMessageLogID *uint           `json:"through_message_log_id,omitempty"`
	Overview            string          `gorm:"type:text;not null;default:''" json:"overview"`
	ActiveTopicVersions json.RawMessage `gorm:"type:jsonb;not null;default:'[]'" json:"active_topic_versions"`
	BatchID             string          `gorm:"type:text;not null;default:''" json:"batch_id"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

func (CompactionCheckpoint) TableName() string { return "context_compaction_checkpoints" }

// TopicThreadRow 是新话题模型，统一群聊和私聊
type TopicThreadRow struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	ConversationKind string     `gorm:"type:text;not null" json:"conversation_kind"`
	TargetID         int64      `gorm:"column:target_id;not null" json:"target_id"`
	Status           string     `gorm:"type:text;not null;default:'dormant'" json:"status"`
	StabilityDays    float64    `gorm:"column:stability_days;not null;default:30" json:"stability_days"`
	RecallAnchorAt   time.Time  `gorm:"column:recall_anchor_at;not null;default:now()" json:"recall_anchor_at"`
	ForgetAt         time.Time  `gorm:"column:forget_at;not null;default:now()" json:"forget_at"`
	ForgottenAt      *time.Time `gorm:"column:forgotten_at" json:"forgotten_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func (TopicThreadRow) TableName() string { return "topic_threads" }

// TopicSummaryRow 保存不可变话题摘要版本
type TopicSummaryRow struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	TopicID     uint      `gorm:"not null" json:"topic_id"`
	Version     uint64    `gorm:"not null;default:1" json:"version"`
	SummaryJSON string    `gorm:"type:jsonb;not null" json:"summary_json"`
	CreatedAt   time.Time `json:"created_at"`
}

func (TopicSummaryRow) TableName() string { return "topic_summaries" }

// TopicSourceRow 记录摘要来源和用于分层查询的关键消息
type TopicSourceRow struct {
	SummaryID       uint      `gorm:"primaryKey" json:"summary_id"`
	MessageLogID    uint      `gorm:"primaryKey" json:"message_log_id"`
	OneBotMessageID int64     `gorm:"column:one_bot_message_id;not null;default:0" json:"one_bot_message_id"`
	MessageTime     time.Time `gorm:"column:message_time;not null;default:now()" json:"message_time"`
	IsAnchor        bool      `gorm:"not null;default:false" json:"is_anchor"`
}

func (TopicSourceRow) TableName() string { return "topic_summary_sources" }

// KnowledgeRecord 是新长期记忆模型，旧 knowledge_items 结构由迁移保留
type KnowledgeRecord struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	SubjectType    string     `gorm:"type:text;not null" json:"subject_type"`
	SubjectTarget  string     `gorm:"type:text;not null" json:"subject_target"`
	MemoryType     string     `gorm:"type:text;not null" json:"memory_type"`
	Label          string     `gorm:"type:text;not null;default:''" json:"label"`
	Content        string     `gorm:"type:text;not null" json:"content"`
	Pinned         bool       `gorm:"not null;default:false" json:"pinned"`
	Status         string     `gorm:"type:text;not null;default:'active'" json:"status"`
	StabilityDays  float64    `gorm:"column:stability_days;not null;default:30" json:"stability_days"`
	RecallAnchorAt time.Time  `gorm:"column:recall_anchor_at;not null;default:now()" json:"recall_anchor_at"`
	ForgetAt       *time.Time `gorm:"column:forget_at" json:"forget_at,omitempty"`
	Version        uint64     `gorm:"not null;default:1" json:"version"`
	ContentHash    string     `gorm:"type:text;not null;default:''" json:"content_hash"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (KnowledgeRecord) TableName() string { return "knowledge_items" }

type KnowledgeRevision struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	ItemID        uint      `gorm:"not null" json:"item_id"`
	Version       uint64    `gorm:"not null" json:"version"`
	BeforeContent string    `gorm:"type:text;not null;default:''" json:"before_content"`
	AfterContent  string    `gorm:"type:text;not null;default:''" json:"after_content"`
	BeforeStatus  string    `gorm:"type:text;not null;default:''" json:"before_status"`
	AfterStatus   string    `gorm:"type:text;not null;default:''" json:"after_status"`
	BeforePinned  bool      `gorm:"not null;default:false" json:"before_pinned"`
	AfterPinned   bool      `gorm:"not null;default:false" json:"after_pinned"`
	Reason        string    `gorm:"type:text;not null;default:''" json:"reason"`
	CreatedAt     time.Time `json:"created_at"`
}

func (KnowledgeRevision) TableName() string { return "knowledge_revisions" }

// EmotionState 保存一条全局共享的感受；ID 是身份，允许同名感受并存
type EmotionState struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	Kind       string     `gorm:"type:text;not null" json:"kind"`
	Fact       string     `gorm:"type:text;not null;default:''" json:"fact"`
	Reason     string     `gorm:"type:text;not null;default:''" json:"reason"`
	Strength   float64    `gorm:"not null;default:0" json:"strength"`
	OccurredAt time.Time  `gorm:"not null" json:"occurred_at"`
	AnchorAt   time.Time  `gorm:"not null" json:"anchor_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	Version    uint64     `gorm:"not null;default:1" json:"version"`
}

func (EmotionState) TableName() string { return "emotion_states" }

// EmotionRevision 保存每次有效修订的完整状态快照，上一版就是 before
type EmotionRevision struct {
	ID        uint            `gorm:"primaryKey" json:"id"`
	EmotionID uint            `gorm:"not null" json:"emotion_id"`
	Version   uint64          `gorm:"not null" json:"version"`
	Snapshot  json.RawMessage `gorm:"type:jsonb;not null" json:"snapshot"`
	CreatedAt time.Time       `json:"created_at"`
}

func (EmotionRevision) TableName() string { return "emotion_revisions" }

// TopicSnapshot 是 checkpoints.active_topic_versions 的单个元素
type TopicSnapshot struct {
	TopicID   uint `json:"topic_id"`
	SummaryID uint `json:"summary_id"`
}
