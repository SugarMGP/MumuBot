package memory

import (
	"time"

	"gorm.io/gorm"
)

// MemoryType 记忆类型
type MemoryType string

const (
	MemoryTypeGroupFact      MemoryType = "group_fact"      // 群长期事实（群规、群风格、重要事件等）
	MemoryTypeSelfExperience MemoryType = "self_experience" // 自身经历（参与的事、被提及、感受等）
	MemoryTypeConversation   MemoryType = "conversation"    // 对话记忆（重要的对话内容、群友说的事）
)

// Memory 长期记忆
type Memory struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	Type        MemoryType `gorm:"type:varchar(50);index" json:"type"`
	GroupID     int64      `gorm:"index" json:"group_id"`
	UserID      int64      `gorm:"index" json:"user_id,omitempty"`
	Content     string     `gorm:"type:text" json:"content"`
	Summary     string     `gorm:"type:varchar(500)" json:"summary"`
	Keywords    string     `gorm:"type:varchar(500)" json:"keywords"` // 关键词，逗号分隔
	Importance  float64    `gorm:"default:0.5" json:"importance"`
	AccessCount int        `gorm:"default:0" json:"access_count"`
	LastAccess  time.Time  `json:"last_access"`
	SourceMsgID string     `gorm:"type:varchar(100)" json:"source_msg_id,omitempty"`
	Metadata    string     `gorm:"type:text" json:"metadata,omitempty"`
	// 向量存储在 Milvus 中，不再存储在此表
}

func (Memory) TableName() string { return "memories" }

// TopicSummary 话题概括（参考 MaiBot ChatHistorySummarizer）
type TopicSummary struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	GroupID      int64     `gorm:"index" json:"group_id"`
	Topic        string    `gorm:"type:varchar(200)" json:"topic"`
	Summary      string    `gorm:"type:text" json:"summary"`
	Keywords     string    `gorm:"type:varchar(500)" json:"keywords"`
	KeyPoints    string    `gorm:"type:text" json:"key_points"`
	Participants string    `gorm:"type:varchar(500)" json:"participants"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	MsgCount     int       `gorm:"default:0" json:"msg_count"`
	// 向量存储在 Milvus 中（如需要），不再存储在此表
}

func (TopicSummary) TableName() string { return "topic_summaries" }

// MemberProfile 成员画像
type MemberProfile struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	GroupID     int64     `gorm:"uniqueIndex:idx_group_user" json:"group_id"`
	UserID      int64     `gorm:"uniqueIndex:idx_group_user" json:"user_id"`
	Nickname    string    `gorm:"type:varchar(100)" json:"nickname"`
	SpeakStyle  string    `gorm:"type:text" json:"speak_style"`
	Interests   string    `gorm:"type:text" json:"interests"`
	CommonWords string    `gorm:"type:text" json:"common_words"`
	Activity    float64   `gorm:"default:0.5" json:"activity"`
	Intimacy    float64   `gorm:"default:0.3" json:"intimacy"`
	LastSpeak   time.Time `json:"last_speak"`
	MsgCount    int       `gorm:"default:0" json:"msg_count"`
}

func (MemberProfile) TableName() string { return "member_profiles" }

// Expression 学习到的表达方式（参考 MaiBot Expression）
type Expression struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	GroupID   int64     `gorm:"index" json:"group_id"`
	Situation string    `gorm:"type:varchar(200)" json:"situation"` // 使用场景
	Style     string    `gorm:"type:varchar(200)" json:"style"`     // 表达风格
	Examples  string    `gorm:"type:text" json:"examples"`          // 示例 JSON
	Count     int       `gorm:"default:1" json:"count"`
	LastUsed  time.Time `json:"last_used"`
	Checked   bool      `gorm:"default:false" json:"checked"`
	Rejected  bool      `gorm:"default:false" json:"rejected"`
}

func (Expression) TableName() string { return "expressions" }

// Jargon 黑话/术语
type Jargon struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	GroupID  int64  `gorm:"index" json:"group_id"`
	Content  string `gorm:"type:varchar(100);index" json:"content"`
	Meaning  string `gorm:"type:text" json:"meaning"`
	Context  string `gorm:"type:text" json:"context"`
	Count    int    `gorm:"default:1" json:"count"`
	Verified bool   `gorm:"default:false" json:"verified"`
}

func (Jargon) TableName() string { return "jargons" }

// GroupInfo 群信息
type GroupInfo struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	GroupID     int64  `gorm:"uniqueIndex" json:"group_id"`
	GroupName   string `gorm:"type:varchar(200)" json:"group_name"`
	Topic       string `gorm:"type:text" json:"topic"`
	HotTopics   string `gorm:"type:text" json:"hot_topics"`
	Admins      string `gorm:"type:text" json:"admins"`
	Rules       string `gorm:"type:text" json:"rules"`
	Atmosphere  string `gorm:"type:text" json:"atmosphere"`
	MemberCount int    `gorm:"default:0" json:"member_count"`
}

func (GroupInfo) TableName() string { return "group_infos" }

// MessageLog 消息日志
type MessageLog struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`

	MessageID  string `gorm:"type:varchar(100);uniqueIndex" json:"message_id"`
	GroupID    int64  `gorm:"index" json:"group_id"`
	UserID     int64  `gorm:"index" json:"user_id"`
	Nickname   string `gorm:"type:varchar(100)" json:"nickname"`
	Content    string `gorm:"type:text" json:"content"`
	MsgType    string `gorm:"type:varchar(50)" json:"msg_type"`
	MentionAmu bool   `gorm:"default:false" json:"mention_amu"`
	Processed  bool   `gorm:"default:false" json:"processed"`
	Summarized bool   `gorm:"default:false" json:"summarized"`
}

func (MessageLog) TableName() string { return "message_logs" }
