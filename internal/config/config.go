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
	Agent      AgentConfig      `mapstructure:"agent"`
	Chat       ChatConfig       `mapstructure:"chat"` // 聊天行为配置
	ModelTiers ModelTiersConfig `mapstructure:"model_tiers"`
	Database   DatabaseConfig   `mapstructure:"database"`
	Sticker    StickerConfig    `mapstructure:"sticker"` // 表情包配置
	Media      MediaConfig      `mapstructure:"media"`   // 聊天媒体缓存配置
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
	Name                  string   `mapstructure:"name"`
	AliasNames            []string `mapstructure:"alias_names"` // 别名，都可以触发@检测
	Interests             []string `mapstructure:"interests"`
	GroupPromptTemplate   string   `mapstructure:"-"` // config/persona_group.prompt 静态群聊人格
	PrivatePromptTemplate string   `mapstructure:"-"` // config/persona_private.prompt 静态私聊人格
}

// OneBotConfig OneBot协议配置
type OneBotConfig struct {
	WsURL             string `mapstructure:"ws_url"`
	AccessToken       string `mapstructure:"access_token"`
	ReconnectInterval int    `mapstructure:"reconnect_interval"`
}

// AgentConfig Agent决策配置
type AgentConfig struct {
	ThinkTimeoutSeconds int              `mapstructure:"think_timeout_seconds"` // 主 Agent 单轮超时（秒）
	ThinkInterval       int              `mapstructure:"think_interval"`        // 决策间隔（秒）
	ThinkDebounceMS     int              `mapstructure:"think_debounce_ms"`     // 思考聚合窗口（毫秒）
	MaxStep             int              `mapstructure:"max_step"`              // ReAct 最大步数
	MaxCoroutine        int              `mapstructure:"max_coroutine"`         // 最大并发思考进程数（0表示不限制）
	Compaction          CompactionConfig `mapstructure:"compaction"`
}

// CompactionConfig 控制主模型同步话题压缩
type CompactionConfig struct {
	TriggerRatio    float64 `mapstructure:"trigger_ratio"`
	TargetRatio     float64 `mapstructure:"target_ratio"`
	RecentTailRatio float64 `mapstructure:"recent_tail_ratio"`
	MaxStep         int     `mapstructure:"max_step"`
	TimeoutSeconds  int     `mapstructure:"timeout_seconds"`
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

// MediaConfig 聊天媒体缓存配置，普通图片转 webp、表情包保留原件
type MediaConfig struct {
	StoragePath  string `mapstructure:"storage_path"`  // 媒体缓存目录
	MaxBytesMB   int    `mapstructure:"max_bytes_mb"`  // 单个媒体最大体积（MB）
	ImageQuality int    `mapstructure:"image_quality"` // webp 有损质量（1-100）
	NarrowEdge   int    `mapstructure:"narrow_edge"`   // 普通图片窄边上限（像素）
}

// ModelConfig 对话模型配置
type ModelConfig struct {
	APIKey        string                 `mapstructure:"api_key"`
	BaseURL       string                 `mapstructure:"base_url"`
	Model         string                 `mapstructure:"model"`
	ContextWindow int                    `mapstructure:"context_window"` // 模型上下文窗口，仅高档必填
	ExtraFields   map[string]interface{} `mapstructure:"extra_fields"`   // 额外参数
}

// ModelTiersConfig 只保留一个主模型，沿用 high 配置名以兼容现有部署
type ModelTiersConfig struct {
	High ModelConfig `mapstructure:"high"`
}

type DatabaseConfig struct {
	DSN string `mapstructure:"dsn"`
}

// StickerConfig 表情包配置
type StickerConfig struct {
	StoragePath string `mapstructure:"storage_path"` // 表情包存储目录，默认 "./stickers"
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
		if err := reader.Unmarshal(loaded); err != nil {
			loadErr = fmt.Errorf("解析配置文件: %w", err)
			return
		}

		groupPrompt, err := LoadPersonaPrompt(filepath.Join("config", "persona_group.prompt"))
		if err != nil {
			loadErr = err
			return
		}
		privatePrompt, err := LoadPersonaPrompt(filepath.Join("config", "persona_private.prompt"))
		if err != nil {
			loadErr = err
			return
		}
		loaded.Persona.GroupPromptTemplate = groupPrompt
		loaded.Persona.PrivatePromptTemplate = privatePrompt
		// 从环境变量覆盖模型端点和敏感配置
		overrideModelEndpoint("MUMU_MODEL_HIGH", &loaded.ModelTiers.High.APIKey, &loaded.ModelTiers.High.BaseURL, &loaded.ModelTiers.High.Model)
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
	if strings.TrimSpace(c.Media.StoragePath) == "" {
		c.Media.StoragePath = "./media"
	}
	if c.Media.MaxBytesMB <= 0 {
		c.Media.MaxBytesMB = 10
	}
	if c.Media.ImageQuality <= 0 {
		c.Media.ImageQuality = 75
	}
	if c.Media.ImageQuality > 100 {
		return fmt.Errorf("media.image_quality 必须在 1 到 100 之间")
	}
	if c.Media.NarrowEdge <= 0 {
		c.Media.NarrowEdge = 1080
	}
	if strings.TrimSpace(c.Sticker.StoragePath) == "" {
		c.Sticker.StoragePath = "./stickers"
	}
	if c.OneBot.ReconnectInterval <= 0 {
		return fmt.Errorf("onebot.reconnect_interval 必须大于 0")
	}
	if c.Agent.ThinkTimeoutSeconds <= 0 {
		c.Agent.ThinkTimeoutSeconds = 60
	}
	if c.Agent.ThinkInterval <= 0 {
		return fmt.Errorf("agent.think_interval 必须大于 0")
	}
	if c.Agent.MaxStep <= 0 {
		c.Agent.MaxStep = 13
	}
	if c.Agent.MaxCoroutine < 0 {
		return fmt.Errorf("agent.max_coroutine 不能小于 0")
	}
	if c.Agent.Compaction.TriggerRatio <= 0 {
		c.Agent.Compaction.TriggerRatio = 0.90
	}
	if c.Agent.Compaction.TargetRatio <= 0 {
		c.Agent.Compaction.TargetRatio = 0.40
	}
	if c.Agent.Compaction.RecentTailRatio <= 0 {
		c.Agent.Compaction.RecentTailRatio = 0.10
	}
	if c.Agent.Compaction.MaxStep <= 0 {
		c.Agent.Compaction.MaxStep = 12
	}
	if c.Agent.Compaction.TimeoutSeconds <= 0 {
		c.Agent.Compaction.TimeoutSeconds = 300
	}
	if c.Agent.Compaction.TargetRatio > 0.55 || c.Agent.Compaction.TargetRatio >= c.Agent.Compaction.TriggerRatio || c.Agent.Compaction.TriggerRatio >= 1 || c.Agent.Compaction.RecentTailRatio >= c.Agent.Compaction.TargetRatio {
		return fmt.Errorf("agent.compaction 比例无效：target 不超过 0.55、target < trigger < 1 且 recent_tail < target")
	}
	if c.ModelTiers.High.ContextWindow <= 0 {
		return fmt.Errorf("model_tiers.high.context_window 必须大于 0；模型需要支持 OpenAI Responses 端点（/responses）")
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
