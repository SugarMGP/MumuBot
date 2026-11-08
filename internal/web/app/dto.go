package app

import (
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelprofiles"
	"mumu-bot/internal/modelstats"
)

// 本文件是后台 JSON 接口的响应结构，字段名与 web/src/api/types.ts 一一对应

type warningPayload struct {
	Code string `json:"code"`
}

type sessionResponse struct {
	Enabled       bool   `json:"enabled"`
	Authenticated bool   `json:"authenticated"`
	Version       string `json:"version"`
}

type countsDTO struct {
	ActiveConversations int64 `json:"active_conversations"`
	ActiveMemories      int64 `json:"active_memories"`
	ActiveTopics        int64 `json:"active_topics"`
	RunningWorkers      int64 `json:"running_workers"`
	WorkerCount         int64 `json:"worker_count"`
}

type runtimeDTO struct {
	Connected    bool  `json:"connected"`
	SelfID       int64 `json:"self_id"`
	MCPToolCount int   `json:"mcp_tool_count"`
}

type moodDTO struct {
	Energy      float64   `json:"energy"`
	Sociability float64   `json:"sociability"`
	LastReason  string    `json:"last_reason"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type emotionDTO struct {
	ID           uint      `json:"id"`
	Kind         string    `json:"kind"`
	Fact         string    `json:"fact"`
	Reason       string    `json:"reason"`
	Strength     float64   `json:"strength"`
	StrengthBand string    `json:"strength_band"`
	OccurredAt   time.Time `json:"occurred_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Version      uint64    `json:"version"`
}

type dashboardResponse struct {
	Counts        countsDTO    `json:"counts"`
	Runtime       runtimeDTO   `json:"runtime"`
	Mood          *moodDTO     `json:"mood"`
	Emotions      []emotionDTO `json:"emotions"`
	EmotionsError bool         `json:"emotions_error"`
}

type stickerDTO struct {
	ID          uint      `json:"id"`
	FileName    string    `json:"file_name"`
	Description string    `json:"description"`
	UseCount    int       `json:"use_count"`
	UpdatedAt   time.Time `json:"updated_at"`
	FileURL     string    `json:"file_url"`
}

type memberNameDTO struct {
	Value   string `json:"value"`
	GroupID int64  `json:"group_id"`
}

type memberDTO struct {
	UserID         int64           `json:"user_id"`
	Nickname       string          `json:"nickname"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Names          []memberNameDTO `json:"names"`
	KnowledgeCount int64           `json:"knowledge_count"`
}

type conversationDTO struct {
	Kind         string    `json:"kind"`
	TargetID     int64     `json:"target_id"`
	Name         string    `json:"name"`
	RemoteRemark string    `json:"remote_remark"`
	Blocked      bool      `json:"blocked"`
	ExtraPrompt  string    `json:"extra_prompt"`
	Active       bool      `json:"active"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type conversationsResponse struct {
	Groups  []conversationDTO `json:"groups"`
	Friends []conversationDTO `json:"friends"`
}

type memoryDTO struct {
	ID             uint       `json:"id"`
	SubjectType    string     `json:"subject_type"`
	SubjectTarget  string     `json:"subject_target"`
	MemoryType     string     `json:"memory_type"`
	Label          string     `json:"label"`
	Content        string     `json:"content"`
	Pinned         bool       `json:"pinned"`
	Status         string     `json:"status"`
	StabilityDays  float64    `json:"stability_days"`
	RecallAnchorAt time.Time  `json:"recall_anchor_at"`
	ForgetAt       *time.Time `json:"forget_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type memoryDetailResponse struct {
	Item      memoryDTO                  `json:"item"`
	Revisions []memory.KnowledgeRevision `json:"revisions"`
}

type topicDiffSegment struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type topicTextDiff struct {
	Changed  bool               `json:"changed"`
	Segments []topicDiffSegment `json:"segments"`
}

type topicSummaryChangeDTO struct {
	CapturedAt       time.Time     `json:"captured_at"`
	Initial          bool          `json:"initial"`
	Title            topicTextDiff `json:"title"`
	Gist             topicTextDiff `json:"gist"`
	AddedOpenLoops   []string      `json:"added_open_loops"`
	RemovedOpenLoops []string      `json:"removed_open_loops"`
}

type topicListItemDTO struct {
	ID               uint       `json:"id"`
	Title            string     `json:"title"`
	Gist             string     `json:"gist"`
	Keywords         []string   `json:"keywords"`
	Status           string     `json:"status"`
	StabilityDays    float64    `json:"stability_days"`
	RecallAnchorAt   time.Time  `json:"recall_anchor_at"`
	ForgetAt         time.Time  `json:"forget_at"`
	ForgottenAt      *time.Time `json:"forgotten_at,omitempty"`
	SourceTotal      int        `json:"source_total"`
	SourceAvailable  int        `json:"source_available"`
	ConversationKind string     `json:"conversation_kind"`
	TargetID         int64      `json:"target_id"`
	MessageCount     int64      `json:"message_count"`
	AnchorCount      int        `json:"anchor_count"`
	SourceCount      int        `json:"source_count"`
	OpenLoopCount    int        `json:"open_loop_count"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type topicMessageDTO struct {
	ID          uint      `json:"id"`
	Sender      string    `json:"sender"`
	UserID      int64     `json:"user_id"`
	Text        string    `json:"text"`
	MessageTime time.Time `json:"message_time"`
	Recalled    bool      `json:"recalled"`
	Anchor      bool      `json:"anchor"`
	Source      bool      `json:"source"`
}

type topicDetailResponse struct {
	Thread                topicListItemDTO        `json:"thread"`
	CurrentSummary        *memory.TopicSummary    `json:"current_summary"`
	CurrentSummaryVersion uint64                  `json:"current_summary_version"`
	SummarySnapshotCount  int                     `json:"summary_snapshot_count"`
	SummaryChanges        []topicSummaryChangeDTO `json:"summary_changes"`
	Messages              []topicMessageDTO       `json:"messages"`
	MessageLimit          int                     `json:"message_limit"`
}

type personaSummaryDTO struct {
	ID          uint      `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Builtin     bool      `json:"builtin"`
	Active      bool      `json:"active"`
	Version     int64     `json:"version"`
	CardChars   int       `json:"card_chars"`
	RulesChars  int       `json:"rules_chars"`
	QQChars     int       `json:"qq_chars"`
	WorkerChars int       `json:"worker_chars"`
	LifeChars   int       `json:"life_chars"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type personaDetailDTO struct {
	personaSummaryDTO
	config.PersonaTexts
}

type personasResponse struct {
	Items    []personaSummaryDTO `json:"items"`
	ActiveID uint                `json:"active_id"`
}

type settingsResponse struct {
	Config           *config.RuntimeConfig `json:"config"`
	Versions         map[string]int64      `json:"versions"`
	AgentReady       bool                  `json:"agent_ready"`
	SecretConfigured bool                  `json:"secret_configured"`
}

type modelsResponse struct {
	Items            []modelprofiles.Profile `json:"items"`
	SecretConfigured bool                    `json:"secret_configured"`
}

type mcpServerSummaryDTO struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Transport string    `json:"transport"`
	Version   int64     `json:"version"`
	Position  int       `json:"position"`
	UpdatedAt time.Time `json:"updated_at"`
}

type mcpServersResponse struct {
	Items            []mcpServerSummaryDTO `json:"items"`
	SecretConfigured bool                  `json:"secret_configured"`
}

type mcpServerDetailDTO struct {
	mcpServerSummaryDTO
	Type      string            `json:"type"`
	URL       string            `json:"url"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       []string          `json:"env"`
	Headers   map[string]string `json:"headers"`
	ToolNames []string          `json:"tool_names"`
}

type compactorDTO struct {
	TriggerRatio      float64 `json:"trigger_ratio"`
	DigestBudgetRatio float64 `json:"digest_budget_ratio"`
	RecentTailRatio   float64 `json:"recent_tail_ratio"`
	MaxSteps          int     `json:"max_steps"`
	IdleMinutes       int     `json:"idle_minutes"`
	IdleMinRatio      float64 `json:"idle_min_ratio"`
}

type systemPersonaDTO struct {
	PresetName  string `json:"preset_name"`
	CardChars   int    `json:"card_chars"`
	RulesChars  int    `json:"rules_chars"`
	QQChars     int    `json:"qq_chars"`
	LifeChars   int    `json:"life_chars"`
	WorkerChars int    `json:"worker_chars"`
}

type systemContextDTO struct {
	EnabledGroupCount int          `json:"enabled_group_count"`
	EnabledGroupIDs   []int64      `json:"enabled_group_ids"`
	ThinkIntervalSec  int          `json:"think_interval_sec"`
	ThinkDebounceMS   int          `json:"think_debounce_ms"`
	MaxStep           int          `json:"max_step"`
	LifeMaxStep       int          `json:"life_max_step"`
	WorkerMaxStep     int          `json:"worker_max_step"`
	Compactor         compactorDTO `json:"compactor"`
}

type systemModelDTO struct {
	Active        *modelProfileDTO   `json:"active"`
	MCPToolCount  int                `json:"mcp_tool_count"`
	MCPStatuses   []mcp.ServerStatus `json:"mcp_statuses,omitempty"`
	ContextWindow int                `json:"context_window"`
}

type modelProfileDTO struct {
	Name            string `json:"name"`
	BaseURL         string `json:"base_url"`
	ModelName       string `json:"model_name"`
	ThinkingType    string `json:"thinking_type"`
	ReasoningEffort string `json:"reasoning_effort"`
}

type systemRuntimeDTO struct {
	Connected           bool  `json:"connected"`
	AgentReady          bool  `json:"agent_ready"`
	ReconnectSec        int   `json:"reconnect_sec"`
	SchemaVersion       int   `json:"schema_version"`
	LatestSchemaVersion int   `json:"latest_schema_version"`
	SelfID              int64 `json:"self_id"`
	MediaMaxBytesMB     int   `json:"media_max_bytes_mb"`
}

type systemResponse struct {
	Persona systemPersonaDTO `json:"persona"`
	Context systemContextDTO `json:"context"`
	Model   systemModelDTO   `json:"model"`
	Runtime systemRuntimeDTO `json:"runtime"`
}

type modelStatsResponse struct {
	Range   string                    `json:"range"`
	Totals  modelstats.AggregateRow   `json:"totals"`
	ByTask  []modelstats.AggregateRow `json:"by_task"`
	ByModel []modelstats.AggregateRow `json:"by_model"`
	Series  []modelstats.SeriesRow    `json:"series"`
}

type logFieldDTO struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type logLineDTO struct {
	Seq     uint64        `json:"seq"`
	Time    string        `json:"time"`
	Level   string        `json:"level"`
	Message string        `json:"message"`
	Fields  []logFieldDTO `json:"fields"`
	Text    string        `json:"text"`
}

type logSnapshotDTO struct {
	Lines    []logLineDTO `json:"lines"`
	Total    int          `json:"total"`
	Filtered int          `json:"filtered"`
}
