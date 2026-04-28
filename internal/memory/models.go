package memory

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// MemoryType 记忆类型
type MemoryType string

const (
	MemoryTypeGroupFact      MemoryType = "group_fact"      // 群长期事实（群规、群风格、重要事件等）
	MemoryTypeSelfExperience MemoryType = "self_experience" // 自身经历（参与的事、被提及、感受等）
	MemoryTypeConversation   MemoryType = "conversation"    // 对话记忆（重要的对话内容、群友说的事）
)

type CanonicalMemoryType string

const (
	CanonicalMemoryTypeFact       CanonicalMemoryType = "fact"
	CanonicalMemoryTypeEpisode    CanonicalMemoryType = "episode"
	CanonicalMemoryTypePreference CanonicalMemoryType = "preference"
	CanonicalMemoryTypeConstraint CanonicalMemoryType = "constraint"
	CanonicalMemoryTypeGoal       CanonicalMemoryType = "goal"
)

type MemoryStatus string

const (
	MemoryStatusActive    MemoryStatus = "active"
	MemoryStatusCandidate MemoryStatus = "candidate"
	MemoryStatusArchived  MemoryStatus = "archived"
	MemoryStatusLegacy    MemoryStatus = "legacy"
)

type MemorySourceKind string

const (
	MemorySourceKindMessage   MemorySourceKind = "message"
	MemorySourceKindTopic     MemorySourceKind = "topic"
	MemorySourceKindMigration MemorySourceKind = "migration"
)

type MemorySubjectClass string

const (
	MemorySubjectClassGroup   MemorySubjectClass = "group"
	MemorySubjectClassSelf    MemorySubjectClass = "self"
	MemorySubjectClassMember  MemorySubjectClass = "member"
	MemorySubjectClassUnknown MemorySubjectClass = "unknown"
)

const (
	MemoryOpenLoopGraceWindow = 72 * time.Hour
)

var (
	slotAnchorSanitizer = regexp.MustCompile(`[^\p{Han}a-z0-9_]+`)
	slotAnchorSplitters = regexp.MustCompile(`[\s\-]+`)
)

// Memory 长期记忆
type Memory struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Type          MemoryType          `gorm:"type:varchar(50);index" json:"type"`
	GroupID       int64               `gorm:"index" json:"group_id"`
	UserID        int64               `gorm:"index" json:"user_id,omitempty"`
	Content       string              `gorm:"type:text" json:"content"`
	Importance    float64             `gorm:"default:0.5" json:"importance"`
	AccessCount   int                 `gorm:"default:0" json:"access_count"`
	CanonicalType CanonicalMemoryType `gorm:"type:varchar(32);index" json:"canonical_type"`
	Status        MemoryStatus        `gorm:"type:varchar(20);index" json:"status"`
	EvidenceCount int                 `gorm:"default:1" json:"evidence_count"`
	SourceKind    MemorySourceKind    `gorm:"type:varchar(20);index" json:"source_kind"`
	SourceRef     string              `gorm:"type:varchar(191);index" json:"source_ref"`
	FactKey       string              `gorm:"type:varchar(191);index" json:"fact_key"`
}

func (Memory) TableName() string { return "memories" }

func (m Memory) EffectiveStatus() MemoryStatus {
	status := MemoryStatus(strings.TrimSpace(string(m.Status)))
	if status == "" {
		return MemoryStatusLegacy
	}
	return status
}

func (m Memory) RecallEligible() bool {
	switch m.EffectiveStatus() {
	case MemoryStatusActive, MemoryStatusLegacy:
		return true
	default:
		return false
	}
}

func IsKeyedCanonicalType(kind CanonicalMemoryType) bool {
	switch kind {
	case CanonicalMemoryTypeFact, CanonicalMemoryTypePreference, CanonicalMemoryTypeConstraint, CanonicalMemoryTypeGoal:
		return true
	default:
		return false
	}
}

func oldMemoryTypeFromSubject(subjectClass MemorySubjectClass) MemoryType {
	switch subjectClass {
	case MemorySubjectClassGroup:
		return MemoryTypeGroupFact
	case MemorySubjectClassSelf:
		return MemoryTypeSelfExperience
	case MemorySubjectClassMember:
		return MemoryTypeConversation
	default:
		return MemoryTypeConversation
	}
}

func scopeCodeFromMemoryType(kind MemoryType) string {
	switch kind {
	case MemoryTypeGroupFact:
		return "gf"
	case MemoryTypeSelfExperience:
		return "se"
	default:
		return "cv"
	}
}

func buildFactKey(kind MemoryType, subjectToken string, slotKind string, slotAnchor string) string {
	subjectToken = strings.TrimSpace(subjectToken)
	slotKind = strings.TrimSpace(slotKind)
	slotAnchor = strings.TrimSpace(slotAnchor)
	if subjectToken == "" || slotKind == "" || slotAnchor == "" {
		return ""
	}
	hash := sha1.Sum([]byte(slotAnchor))
	shortHash := hex.EncodeToString(hash[:])[:10]
	return fmt.Sprintf("%s:%s:%s:%s", scopeCodeFromMemoryType(kind), subjectToken, slotKind, shortHash)
}

func normalizeSlotAnchor(raw string) string {
	text := strings.TrimSpace(strings.ToLower(raw))
	if text == "" {
		return ""
	}
	text = slotAnchorSplitters.ReplaceAllString(text, "_")
	text = slotAnchorSanitizer.ReplaceAllString(text, "_")
	text = strings.Trim(text, "_")
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 32 {
		text = string(runes[:32])
	}
	return strings.Trim(text, "_")
}

func subjectToken(subjectClass MemorySubjectClass, groupID int64, userID int64, selfID int64) string {
	switch subjectClass {
	case MemorySubjectClassGroup:
		if groupID > 0 {
			return fmt.Sprintf("g:%d", groupID)
		}
	case MemorySubjectClassSelf:
		if selfID > 0 {
			return fmt.Sprintf("self:%d", selfID)
		}
	case MemorySubjectClassMember:
		if userID > 0 {
			return fmt.Sprintf("u:%d", userID)
		}
	}
	return ""
}

func importanceForStatus(kind CanonicalMemoryType, status MemoryStatus, evidenceCount int) float64 {
	base := 0.45
	switch kind {
	case CanonicalMemoryTypeConstraint:
		base = 0.82
	case CanonicalMemoryTypeGoal:
		base = 0.74
	case CanonicalMemoryTypePreference:
		base = 0.62
	case CanonicalMemoryTypeEpisode:
		base = 0.58
	case CanonicalMemoryTypeFact:
		base = 0.68
	}
	if status == MemoryStatusCandidate {
		base -= 0.18
	}
	if status == MemoryStatusLegacy {
		base -= 0.1
	}
	if evidenceCount > 1 {
		base += float64(evidenceCount-1) * 0.05
	}
	if base < 0.1 {
		return 0.1
	}
	if base > 0.98 {
		return 0.98
	}
	return base
}

// MemberProfile 成员画像
type MemberProfile struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	UserID      int64     `gorm:"uniqueIndex::idx_user" json:"user_id"`
	Nickname    string    `gorm:"type:varchar(100)" json:"nickname"`
	NameRecords string    `gorm:"type:text" json:"name_records,omitempty"`
	SpeakStyle  string    `gorm:"type:text" json:"speak_style"`
	Interests   string    `gorm:"type:text" json:"interests"`
	CommonWords string    `gorm:"type:text" json:"common_words"`
	Activity    float64   `gorm:"default:0.5" json:"activity"`
	Intimacy    float64   `gorm:"default:0.3" json:"intimacy"`
	LastSpeak   time.Time `json:"last_speak"`
	MsgCount    int       `gorm:"default:0" json:"msg_count"`
}

func (MemberProfile) TableName() string { return "member_profiles" }

type MemberNameSource string

const (
	MemberNameSourceGroupCard    MemberNameSource = "group_card"
	MemberNameSourceLearnedAlias MemberNameSource = "learned_alias"
)

type MemberNameRecord struct {
	Content   string           `json:"content"`
	Source    MemberNameSource `json:"source"`
	GroupID   int64            `json:"group_id"`
	UpdatedAt time.Time        `json:"updated_at"`
}

type StyleIntent string

const (
	StyleIntentLightBanter StyleIntent = "轻松起哄"
	StyleIntentAgreement   StyleIntent = "认同接话"
	StyleIntentQuestioning StyleIntent = "询问推进"
	StyleIntentCalming     StyleIntent = "安抚缓和"
)

var styleIntentSet = map[StyleIntent]struct{}{
	StyleIntentLightBanter: {},
	StyleIntentAgreement:   {},
	StyleIntentQuestioning: {},
	StyleIntentCalming:     {},
}

func IsValidStyleIntent(v string) bool {
	_, ok := styleIntentSet[StyleIntent(v)]
	return ok
}

func StyleIntentValues() []string {
	return []string{
		string(StyleIntentLightBanter),
		string(StyleIntentAgreement),
		string(StyleIntentQuestioning),
		string(StyleIntentCalming),
	}
}

type StyleTone string

const (
	StyleToneDirect     StyleTone = "直接"
	StyleToneLight      StyleTone = "轻松"
	StyleToneExaggerate StyleTone = "夸张"
	StyleToneRestrained StyleTone = "克制"
)

var styleToneSet = map[StyleTone]struct{}{
	StyleToneDirect:     {},
	StyleToneLight:      {},
	StyleToneExaggerate: {},
	StyleToneRestrained: {},
}

func IsValidStyleTone(v string) bool {
	_, ok := styleToneSet[StyleTone(v)]
	return ok
}

func StyleToneValues() []string {
	return []string{
		string(StyleToneDirect),
		string(StyleToneLight),
		string(StyleToneExaggerate),
		string(StyleToneRestrained),
	}
}

type StyleCardStatus string

const (
	StyleCardStatusCandidate StyleCardStatus = "candidate"
	StyleCardStatusActive    StyleCardStatus = "active"
	StyleCardStatusRejected  StyleCardStatus = "rejected"
)

// StyleCard 群风格卡片
type StyleCard struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	GroupID       int64           `gorm:"index" json:"group_id"`
	Intent        string          `gorm:"type:varchar(32);index" json:"intent"`
	Tone          string          `gorm:"type:varchar(32);index" json:"tone"`
	TriggerRule   string          `gorm:"type:varchar(255)" json:"trigger_rule"`
	AvoidRule     string          `gorm:"type:varchar(255)" json:"avoid_rule"`
	Example       string          `gorm:"type:varchar(255)" json:"example"`
	SourceExcerpt string          `gorm:"type:text" json:"source_excerpt"`
	Status        StyleCardStatus `gorm:"type:varchar(20);index;default:'candidate'" json:"status"`
	EvidenceCount int             `gorm:"default:1" json:"evidence_count"`
	UseCount      int             `gorm:"default:0" json:"use_count"`
	LastUsedAt    *time.Time      `json:"last_used_at,omitempty"`
}

var styleCardTableName = "style_cards"

func (StyleCard) TableName() string { return styleCardTableName }

// Jargon 黑话/术语
type Jargon struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	GroupID  int64  `gorm:"index" json:"group_id"`
	Content  string `gorm:"type:varchar(100);index" json:"content"`
	Meaning  string `gorm:"type:text" json:"meaning"`
	Context  string `gorm:"type:text" json:"context"`
	Checked  bool   `gorm:"default:false" json:"checked"`
	Rejected bool   `gorm:"default:false" json:"rejected"`
}

func (Jargon) TableName() string { return "jargons" }

// MessageLog 消息日志
type MessageLog struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`

	MessageID        string  `gorm:"type:varchar(100);uniqueIndex" json:"message_id"`
	GroupID          int64   `gorm:"index" json:"group_id"`
	UserID           int64   `gorm:"index" json:"user_id"`
	Nickname         string  `gorm:"type:varchar(100)" json:"nickname"`
	Content          string  `gorm:"type:text" json:"content"`
	OriginalContent  string  `gorm:"type:text" json:"original_content,omitempty"` // 原始消息内容
	MsgType          string  `gorm:"type:varchar(50)" json:"msg_type"`
	IsMentioned      bool    `gorm:"default:false" json:"is_mentioned"`
	Forwards         string  `gorm:"type:text" json:"forwards,omitempty"` // 合并转发内容的 JSON
	TopicThreadID    uint    `gorm:"index;default:0" json:"topic_thread_id"`
	TopicMatchReason string  `gorm:"type:varchar(255)" json:"topic_match_reason,omitempty"`
	TopicMatchScore  float64 `gorm:"default:0" json:"topic_match_score"`
}

func (MessageLog) TableName() string { return "message_logs" }

const (
	MaxActiveTopicThreadsPerGroup = 5
	TopicSummaryHistoryLimit      = 5
	TopicTailKeepMessages         = 8
	TopicSummaryTriggerMessages   = 10
)

type TopicThreadStatus string

const (
	TopicThreadStatusActive   TopicThreadStatus = "active"
	TopicThreadStatusArchived TopicThreadStatus = "archived"
)

type TopicSummaryParticipant struct {
	Nickname string `json:"nickname"`
	Position string `json:"position"`
}

type TopicParticipantRef struct {
	UserID   int64  `json:"user_id"`
	Nickname string `json:"nickname"`
}

type TopicSummaryV1 struct {
	Version      int                       `json:"version"`
	Title        string                    `json:"title"`
	Gist         string                    `json:"gist"`
	Facts        []string                  `json:"facts"`
	Participants []TopicSummaryParticipant `json:"participants"`
	OpenLoops    []string                  `json:"open_loops"`
	RecentTurns  []string                  `json:"recent_turns"`
	Keywords     []string                  `json:"keywords"`
}

type TopicSummarySnapshot struct {
	CapturedAt string         `json:"captured_at"`
	Summary    TopicSummaryV1 `json:"summary"`
}

type TopicThread struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	GroupID                  int64             `gorm:"index" json:"group_id"`
	Status                   TopicThreadStatus `gorm:"type:varchar(20);index" json:"status"`
	SummaryJSON              string            `gorm:"type:text" json:"summary_json"`
	SummaryHistoryJSON       string            `gorm:"type:text" json:"summary_history_json"`
	SummaryUntilMessageLogID uint              `gorm:"default:0" json:"summary_until_message_log_id"`
	LastMessageLogID         uint              `gorm:"index;default:0" json:"last_message_log_id"`
}

func (TopicThread) TableName() string { return "topic_threads" }

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

// LearningState 学习状态记录
type LearningState struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UpdatedAt time.Time `json:"updated_at"`

	GroupID       int64 `gorm:"uniqueIndex" json:"group_id"`
	LastMessageID uint  `json:"last_message_id"` // learner 已处理到的最后一条消息ID (数据库自增ID)
}

func (LearningState) TableName() string { return "learning_states" }
