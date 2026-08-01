package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/viper"
)

var (
	cfg     *Config
	loadErr error
	once    sync.Once
)

// Config 全局配置结构
type Config struct {
	App        AppConfig        `mapstructure:"app"`
	Persona    PersonaConfig    `mapstructure:"persona"`
	OneBot     OneBotConfig     `mapstructure:"onebot"`
	Groups     []GroupConfig    `mapstructure:"groups"`
	Agent      AgentConfig      `mapstructure:"agent"`
	Chat       ChatConfig       `mapstructure:"chat"`     // 聊天行为配置
	Learning   LearningConfig   `mapstructure:"learning"` // 学习系统配置
	ModelTiers ModelTiersConfig `mapstructure:"model_tiers"`
	Embedding  EmbeddingConfig  `mapstructure:"embedding"`
	VisionLLM  VisionLLMConfig  `mapstructure:"vision_llm"`
	Database   DatabaseConfig   `mapstructure:"database"`
	Memory     MemoryConfig     `mapstructure:"memory"`
	Sticker    StickerConfig    `mapstructure:"sticker"` // 表情包配置
	Server     ServerConfig     `mapstructure:"server"`
	Web        WebConfig        `mapstructure:"web"`
	Debug      DebugConfig      `mapstructure:"debug"` // 调试配置
}

// AppConfig 应用基础配置
type AppConfig struct {
	Debug    bool   `mapstructure:"debug"`
	LogLevel string `mapstructure:"log_level"`
}

// PersonaConfig 人格配置
type PersonaConfig struct {
	Name           string   `mapstructure:"name"`
	QQ             int64    `mapstructure:"qq"`          // 沐沐的QQ号
	AliasNames     []string `mapstructure:"alias_names"` // 别名，都可以触发@检测
	Interests      []string `mapstructure:"interests"`
	PromptTemplate string   `mapstructure:"-"`
}

// OneBotConfig OneBot协议配置
type OneBotConfig struct {
	WsURL             string `mapstructure:"ws_url"`
	AccessToken       string `mapstructure:"access_token"`
	ReconnectInterval int    `mapstructure:"reconnect_interval"`
}

// GroupConfig 群配置
type GroupConfig struct {
	GroupID     int64  `mapstructure:"group_id"`
	Enabled     bool   `mapstructure:"enabled"`
	ExtraPrompt string `mapstructure:"extra_prompt"` // 群专属额外提示词
}

// AgentConfig Agent决策配置
type AgentConfig struct {
	ObserveWindow     int `mapstructure:"observe_window"`      // 观察窗口时间（秒）
	ThinkInterval     int `mapstructure:"think_interval"`      // 决策间隔（秒）
	ThinkDebounceMS   int `mapstructure:"think_debounce_ms"`   // 思考聚合窗口（毫秒）
	MessageBufferSize int `mapstructure:"message_buffer_size"` // 消息缓冲区大小
	MaxStep           int `mapstructure:"max_step"`            // ReAct 最大步数
	MaxCoroutine      int `mapstructure:"max_coroutine"`       // 最大并发思考进程数（0表示不限制）
}

// ChatConfig 聊天行为配置
type ChatConfig struct {
	TalkFrequency    float64          `mapstructure:"talk_frequency"`    // 聊天频率，0-1，越大越活跃
	TypingSimulation bool             `mapstructure:"typing_simulation"` // 是否模拟打字延迟
	TypingSpeed      int              `mapstructure:"typing_speed"`      // 每秒打字速度（字符）
	EnableTimeRules  bool             `mapstructure:"enable_time_rules"` // 是否启用时段规则
	TimeRules        []TimeRuleConfig `mapstructure:"time_rules"`        // 时段发言频率规则
	RateLimit        RateLimitConfig  `mapstructure:"rate_limit"`        // 频率限制配置
}

// RateLimitConfig 频率限制配置
type RateLimitConfig struct {
	Enabled     bool    `mapstructure:"enabled"`      // 是否启用
	PeriodSec   int     `mapstructure:"period_sec"`   // 统计周期（秒）
	MaxMessages int     `mapstructure:"max_messages"` // 最大消息数
	MinProb     float64 `mapstructure:"min_prob"`     // 最小保底概率（默认0.1）
}

// TimeRuleConfig 时段规则配置
type TimeRuleConfig struct {
	TimeRange string  `mapstructure:"time_range"` // 时间范围，如 "00:00-08:00"
	GroupID   int64   `mapstructure:"group_id"`   // 群ID，0表示全局
	TalkValue float64 `mapstructure:"talk_value"` // 该时段的发言频率
}

// LearningConfig 学习系统配置
type LearningConfig struct {
	Enabled               bool `mapstructure:"enabled"`                 // 是否启用
	IntervalMinutes       int  `mapstructure:"interval_minutes"`        // 学习任务间隔（分钟）
	ReviewIntervalMinutes int  `mapstructure:"review_interval_minutes"` // 审核任务间隔（分钟）
	BatchSize             int  `mapstructure:"batch_size"`              // 每次学习的消息数量限制
	MinMsgCount           int  `mapstructure:"min_msg_count"`           // 触发学习的最少消息数量
}

// ModelConfig 对话模型配置
type ModelConfig struct {
	APIKey      string                 `mapstructure:"api_key"`
	BaseURL     string                 `mapstructure:"base_url"`
	Model       string                 `mapstructure:"model"`
	ExtraFields map[string]interface{} `mapstructure:"extra_fields"` // 额外参数
}

// ModelTiersConfig 高 / 低两档模型配置
type ModelTiersConfig struct {
	High ModelConfig `mapstructure:"high"`
	Low  ModelConfig `mapstructure:"low"`
}

// EmbeddingConfig Embedding 模型配置
type EmbeddingConfig struct {
	APIKey     string `mapstructure:"api_key"`
	BaseURL    string `mapstructure:"base_url"`
	Model      string `mapstructure:"model"`
	Dimensions int    `mapstructure:"dimensions"`
}

// VisionLLMConfig 多模态视觉模型配置
type VisionLLMConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	APIKey  string `mapstructure:"api_key"`
	BaseURL string `mapstructure:"base_url"`
	Model   string `mapstructure:"model"`
}

// MemoryConfig 记忆系统配置
type MemoryConfig struct {
	MessageLogCleanup MessageLogCleanupConfig `mapstructure:"message_log_cleanup"`
}

type DatabaseConfig struct {
	DSN string `mapstructure:"dsn"`
}

// MessageLogCleanupConfig 消息日志清理配置
type MessageLogCleanupConfig struct {
	Enabled       *bool `mapstructure:"enabled"`        // 是否启用，默认 true
	IntervalHours int   `mapstructure:"interval_hours"` // 清理间隔（小时），默认 6
	KeepLatest    int   `mapstructure:"keep_latest"`    // 每个群保留最新消息数
}

// StickerConfig 表情包配置
type StickerConfig struct {
	AutoSave    bool   `mapstructure:"auto_save"`    // 是否自动保存收到的表情包，默认 true
	StoragePath string `mapstructure:"storage_path"` // 表情包存储目录，默认 "data/stickers"
	MaxSizeMB   int    `mapstructure:"max_size_mb"`  // 单个文件最大大小(MB)，默认 5
}

// ServerConfig HTTP服务配置
type ServerConfig struct {
	Port int `mapstructure:"port"`
}

// WebConfig 管理后台配置
type WebConfig struct {
	AdminKey string `mapstructure:"admin_key"`
}

// DebugConfig 调试配置
type DebugConfig struct {
	ShowPrompt    bool `mapstructure:"show_prompt"`     // 显示系统提示词
	ShowThinking  bool `mapstructure:"show_thinking"`   // 显示思考过程
	ShowMemory    bool `mapstructure:"show_memory"`     // 显示记忆检索
	ShowToolCalls bool `mapstructure:"show_tool_calls"` // 显示工具调用
}

// Load 加载配置文件
func Load(path string) (*Config, error) {
	once.Do(func() {
		loaded := &Config{}
		reader := viper.New()
		reader.SetConfigFile(path)
		if err := reader.ReadInConfig(); err != nil {
			loadErr = fmt.Errorf("解析配置文件: %w", err)
			return
		}
		if err := reader.UnmarshalExact(loaded); err != nil {
			loadErr = fmt.Errorf("解析配置文件: %w", err)
			return
		}

		prompt, err := LoadPersonaPrompt(filepath.Join("config", "persona.prompt"))
		if err != nil {
			loadErr = err
			return
		}
		loaded.Persona.PromptTemplate = prompt
		// 从环境变量覆盖模型端点和敏感配置
		overrideModelEndpoint("MUMU_MODEL_HIGH", &loaded.ModelTiers.High.APIKey, &loaded.ModelTiers.High.BaseURL, &loaded.ModelTiers.High.Model)
		overrideModelEndpoint("MUMU_MODEL_LOW", &loaded.ModelTiers.Low.APIKey, &loaded.ModelTiers.Low.BaseURL, &loaded.ModelTiers.Low.Model)
		overrideModelEndpoint("MUMU_EMBEDDING", &loaded.Embedding.APIKey, &loaded.Embedding.BaseURL, &loaded.Embedding.Model)
		overrideModelEndpoint("MUMU_VISION", &loaded.VisionLLM.APIKey, &loaded.VisionLLM.BaseURL, &loaded.VisionLLM.Model)
		if token := os.Getenv("MUMU_ONEBOT_TOKEN"); token != "" {
			loaded.OneBot.AccessToken = token
		}
		if wsURL := os.Getenv("MUMU_ONEBOT_WS_URL"); wsURL != "" {
			loaded.OneBot.WsURL = wsURL
		}
		if dsn := os.Getenv("MUMU_DATABASE_DSN"); dsn != "" {
			loaded.Database.DSN = dsn
		}
		if adminKey := os.Getenv("MUMU_WEB_ADMIN_KEY"); adminKey != "" {
			loaded.Web.AdminKey = adminKey
		}

		if err := validate(loaded); err != nil {
			loadErr = err
			return
		}
		cfg = loaded
	})
	return cfg, loadErr
}

func overrideModelEndpoint(prefix string, apiKey, baseURL, modelName *string) {
	if value := os.Getenv(prefix + "_API_KEY"); value != "" {
		*apiKey = value
	}
	if value := os.Getenv(prefix + "_BASE_URL"); value != "" {
		*baseURL = value
	}
	if value := os.Getenv(prefix + "_MODEL"); value != "" {
		*modelName = value
	}
}

func validate(c *Config) error {
	if c.Learning.BatchSize == 0 {
		c.Learning.BatchSize = 100
	}
	if c.Learning.MinMsgCount == 0 {
		c.Learning.MinMsgCount = 5
	}
	if c.Learning.BatchSize < 0 {
		return fmt.Errorf("learning.batch_size 不能小于 0")
	}
	if c.Learning.MinMsgCount < 0 {
		return fmt.Errorf("learning.min_msg_count 不能小于 0")
	}
	if c.Learning.BatchSize < c.Learning.MinMsgCount {
		return fmt.Errorf("learning.batch_size 不能小于 learning.min_msg_count")
	}
	if c.OneBot.ReconnectInterval <= 0 {
		return fmt.Errorf("onebot.reconnect_interval 必须大于 0")
	}
	if c.Agent.ThinkInterval <= 0 {
		return fmt.Errorf("agent.think_interval 必须大于 0")
	}
	if c.Agent.MaxCoroutine < 0 {
		return fmt.Errorf("agent.max_coroutine 不能小于 0")
	}
	if c.Chat.TalkFrequency < 0 || c.Chat.TalkFrequency > 1 {
		return fmt.Errorf("chat.talk_frequency 必须在 0 到 1 之间")
	}
	for i, rule := range c.Chat.TimeRules {
		if rule.TalkValue < 0 || rule.TalkValue > 1 {
			return fmt.Errorf("chat.time_rules[%d].talk_value 必须在 0 到 1 之间", i)
		}
	}
	if strings.TrimSpace(c.Database.DSN) == "" {
		return fmt.Errorf("database.dsn 不能为空")
	}
	if c.Embedding.Dimensions <= 0 {
		return fmt.Errorf("embedding.dimensions 必须大于 0")
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port 必须在 1 到 65535 之间")
	}
	return nil
}

func LoadPersonaPrompt(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	prompt := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	if prompt == "" {
		return "", fmt.Errorf("persona prompt body is empty: %s", path)
	}
	return prompt, nil
}

// Get 获取全局配置
func Get() *Config {
	return cfg
}

// GetGroupConfig 获取指定群的配置
func (c *Config) GetGroupConfig(groupID int64) *GroupConfig {
	for i := range c.Groups {
		if c.Groups[i].GroupID == groupID {
			return &c.Groups[i]
		}
	}
	return nil
}

// IsGroupEnabled 检查群是否启用
func (c *Config) IsGroupEnabled(groupID int64) bool {
	gc := c.GetGroupConfig(groupID)
	return gc != nil && gc.Enabled
}
