package config

import (
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

var (
	cfg  *Config
	once sync.Once
)

// Config 全局配置结构
type Config struct {
	App       AppConfig       `yaml:"app"`
	Persona   PersonaConfig   `yaml:"persona"`
	OneBot    OneBotConfig    `yaml:"onebot"`
	Groups    []GroupConfig   `yaml:"groups"`
	Agent     AgentConfig     `yaml:"agent"`
	Chat      ChatConfig      `yaml:"chat"` // 聊天行为配置
	LLM       LLMConfig       `yaml:"llm"`
	Embedding EmbeddingConfig `yaml:"embedding"`
	VisionLLM VisionLLMConfig `yaml:"vision_llm"`
	Memory    MemoryConfig    `yaml:"memory"`
	Sticker   StickerConfig   `yaml:"sticker"` // 表情包配置
	Server    ServerConfig    `yaml:"server"`
	Debug     DebugConfig     `yaml:"debug"` // 调试配置
}

// AppConfig 应用基础配置
type AppConfig struct {
	Debug    bool   `yaml:"debug"`
	LogLevel string `yaml:"log_level"`
}

// PersonaConfig 人格配置
type PersonaConfig struct {
	Name          string   `yaml:"name"`
	AliasNames    []string `yaml:"alias_names"` // 别名，都可以触发@检测
	Interests     []string `yaml:"interests"`
	SpeakingStyle string   `yaml:"speaking_style"`
	Personality   string   `yaml:"personality"` // 人格描述
}

// OneBotConfig OneBot协议配置
type OneBotConfig struct {
	WsURL             string `yaml:"ws_url"`
	AccessToken       string `yaml:"access_token"`
	ReconnectInterval int    `yaml:"reconnect_interval"`
}

// GroupConfig 群配置
type GroupConfig struct {
	GroupID     int64  `yaml:"group_id"`
	Enabled     bool   `yaml:"enabled"`
	ExtraPrompt string `yaml:"extra_prompt"` // 群专属额外提示词
}

// AgentConfig Agent决策配置
type AgentConfig struct {
	ObserveWindow     int `yaml:"observe_window"`      // 观察窗口时间（秒）
	ThinkInterval     int `yaml:"think_interval"`      // 决策间隔（秒）
	MessageBufferSize int `yaml:"message_buffer_size"` // 消息缓冲区大小
	MaxStep           int `yaml:"max_step"`            // ReAct 最大步数
}

// ChatConfig 聊天行为配置
type ChatConfig struct {
	TalkFrequency    float64          `yaml:"talk_frequency"`    // 聊天频率，0-1，越大越活跃
	TypingSimulation bool             `yaml:"typing_simulation"` // 是否模拟打字延迟
	TypingSpeed      int              `yaml:"typing_speed"`      // 每秒打字速度（字符）
	EnableTimeRules  bool             `yaml:"enable_time_rules"` // 是否启用时段规则
	TimeRules        []TimeRuleConfig `yaml:"time_rules"`        // 时段发言频率规则
}

// TimeRuleConfig 时段规则配置
type TimeRuleConfig struct {
	TimeRange string  `yaml:"time_range"` // 时间范围，如 "00:00-08:00"
	GroupID   int64   `yaml:"group_id"`   // 群ID，0表示全局
	TalkValue float64 `yaml:"talk_value"` // 该时段的发言频率
}

// LLMConfig LLM 配置
type LLMConfig struct {
	APIKey      string                 `yaml:"api_key"`
	BaseURL     string                 `yaml:"base_url"`
	Model       string                 `yaml:"model"`
	ExtraFields map[string]interface{} `yaml:"extra_fields"` // 额外参数
}

// EmbeddingConfig Embedding 模型配置
type EmbeddingConfig struct {
	Enabled bool   `yaml:"enabled"`
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
}

// VisionLLMConfig 多模态视觉模型配置
type VisionLLMConfig struct {
	Enabled bool   `yaml:"enabled"`
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
}

// MemoryConfig 记忆系统配置
type MemoryConfig struct {
	MySQL             MySQLConfig             `yaml:"mysql"`
	Milvus            MilvusConfig            `yaml:"milvus"`
	LongTerm          LongTermConfig          `yaml:"long_term"`
	MessageLogCleanup MessageLogCleanupConfig `yaml:"message_log_cleanup"`
}

// MessageLogCleanupConfig 消息日志清理配置
type MessageLogCleanupConfig struct {
	Enabled       *bool `yaml:"enabled"`        // 是否启用，默认 true
	IntervalHours int   `yaml:"interval_hours"` // 清理间隔（小时），默认 6
	KeepLatest    int   `yaml:"keep_latest"`    // 每个群保留最新消息数
}

// MySQLConfig MySQL 数据库配置
type MySQLConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"db_name"`
}

// MilvusConfig Milvus 向量数据库配置
type MilvusConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Address        string `yaml:"address"`
	DBName         string `yaml:"db_name"`
	CollectionName string `yaml:"collection_name"`
	VectorDim      int    `yaml:"vector_dim"`
	MetricType     string `yaml:"metric_type"` // IP, L2, COSINE
}

// LongTermConfig 长期记忆配置
type LongTermConfig struct {
	TopK                int     `yaml:"top_k"`                // 检索返回数量
	SimilarityThreshold float64 `yaml:"similarity_threshold"` // 相似度阈值
	ImportanceThreshold float64 `yaml:"importance_threshold"` // 重要性阈值
}

// StickerConfig 表情包配置
type StickerConfig struct {
	AutoSave    bool   `yaml:"auto_save"`    // 是否自动保存收到的表情包，默认 true
	StoragePath string `yaml:"storage_path"` // 表情包存储目录，默认 "data/stickers"
	MaxSizeMB   int    `yaml:"max_size_mb"`  // 单个文件最大大小(MB)，默认 5
}

// ServerConfig HTTP服务配置
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// DebugConfig 调试配置
type DebugConfig struct {
	ShowPrompt    bool `yaml:"show_prompt"`     // 显示系统提示词
	ShowThinking  bool `yaml:"show_thinking"`   // 显示思考过程
	ShowMemory    bool `yaml:"show_memory"`     // 显示记忆检索
	ShowToolCalls bool `yaml:"show_tool_calls"` // 显示工具调用
}

// Load 加载配置文件
func Load(path string) (*Config, error) {
	var err error
	once.Do(func() {
		var data []byte
		data, err = os.ReadFile(path)
		if err != nil {
			return
		}

		cfg = &Config{}
		err = yaml.Unmarshal(data, cfg)
		if err != nil {
			cfg = nil
			return
		}

		// 从环境变量覆盖敏感配置
		if apiKey := os.Getenv("MUMU_LLM_API_KEY"); apiKey != "" {
			cfg.LLM.APIKey = apiKey
		}
		// Embedding API Key：优先使用专用环境变量，否则使用 LLM 的
		if apiKey := os.Getenv("MUMU_EMBEDDING_API_KEY"); apiKey != "" {
			cfg.Embedding.APIKey = apiKey
		} else if cfg.Embedding.APIKey == "" && cfg.LLM.APIKey != "" {
			cfg.Embedding.APIKey = cfg.LLM.APIKey
		}
		if apiKey := os.Getenv("MUMU_VISION_API_KEY"); apiKey != "" {
			cfg.VisionLLM.APIKey = apiKey
		} else if cfg.Embedding.APIKey == "" && cfg.LLM.APIKey != "" {
			cfg.VisionLLM.APIKey = cfg.LLM.APIKey
		}
		if token := os.Getenv("MUMU_ONEBOT_TOKEN"); token != "" {
			cfg.OneBot.AccessToken = token
		}
		// MySQL 密码
		if password := os.Getenv("MUMU_MYSQL_PASSWORD"); password != "" {
			cfg.Memory.MySQL.Password = password
		}
	})
	return cfg, err
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
