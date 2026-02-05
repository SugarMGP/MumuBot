package memory

import (
	"time"
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
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Type        MemoryType `gorm:"type:varchar(50);index" json:"type"`
	GroupID     int64      `gorm:"index" json:"group_id"`
	UserID      int64      `gorm:"index" json:"user_id,omitempty"`
	Content     string     `gorm:"type:text" json:"content"`
	Importance  float64    `gorm:"default:0.5" json:"importance"`
	AccessCount int        `gorm:"default:0" json:"access_count"`
}

func (Memory) TableName() string { return "memories" }

// MemberProfile 成员画像
type MemberProfile struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	UserID      int64     `gorm:"uniqueIndex::idx_user" json:"user_id"`
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
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	GroupID   int64  `gorm:"index" json:"group_id"`
	Situation string `gorm:"type:varchar(200)" json:"situation"` // 使用场景
	Style     string `gorm:"type:varchar(200)" json:"style"`     // 表达风格
	Examples  string `gorm:"type:text" json:"examples"`          // 示例 JSON
	Checked   bool   `gorm:"default:false" json:"checked"`
	Rejected  bool   `gorm:"default:false" json:"rejected"`
}

func (Expression) TableName() string { return "expressions" }

// Jargon 黑话/术语
type Jargon struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	GroupID  int64  `gorm:"index" json:"group_id"`
	Content  string `gorm:"type:varchar(100);index" json:"content"`
	Meaning  string `gorm:"type:text" json:"meaning"`
	Context  string `gorm:"type:text" json:"context"`
	Verified bool   `gorm:"default:false" json:"verified"`
}

func (Jargon) TableName() string { return "jargons" }

// MessageLog 消息日志
type MessageLog struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`

	MessageID   string `gorm:"type:varchar(100);uniqueIndex" json:"message_id"`
	GroupID     int64  `gorm:"index" json:"group_id"`
	UserID      int64  `gorm:"index" json:"user_id"`
	Nickname    string `gorm:"type:varchar(100)" json:"nickname"`
	Content     string `gorm:"type:text" json:"content"`
	MsgType     string `gorm:"type:varchar(50)" json:"msg_type"`
	IsMentioned bool   `gorm:"default:false" json:"is_mentioned"`
	Forwards    string `gorm:"type:text" json:"forwards,omitempty"` // 合并转发内容的 JSON
}

func (MessageLog) TableName() string { return "message_logs" }

// Sticker 收集的表情包
type Sticker struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	FileName    string `gorm:"type:varchar(100)" json:"file_name"`            // 本地文件名（uuid.ext）
	FileHash    string `gorm:"type:varchar(64);uniqueIndex" json:"file_hash"` // 文件 MD5 哈希（用于去重）
	Description string `gorm:"type:text" json:"description"`                  // Vision 模型生成的描述
	UseCount    int    `gorm:"default:0" json:"use_count"`                    // 使用次数
}

func (Sticker) TableName() string { return "stickers" }

// MoodState 情绪状态（全局唯一）
type MoodState struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UpdatedAt time.Time `json:"updated_at"`

	// 情绪三维度
	Valence     float64 `gorm:"default:0.0" json:"valence"`     // [-1.0, 1.0] 心情好坏：负数=心情差，正数=心情好
	Energy      float64 `gorm:"default:0.5" json:"energy"`      // [0.0, 1.0] 精神/活跃度：低=疲惫，高=活跃
	Sociability float64 `gorm:"default:0.5" json:"sociability"` // [0.0, 1.0] 社交意愿：低=想安静，高=想聊天

	// 最后变化原因（用于调试）
	LastReason string `gorm:"type:varchar(200)" json:"last_reason,omitempty"`
}

func (MoodState) TableName() string { return "mood_state" }
