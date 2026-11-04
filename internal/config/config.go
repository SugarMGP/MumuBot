package config

import (
	"encoding/json"
	"strings"
)

// RuntimeConfig 是进程内不可变的动态配置快照
type RuntimeConfig struct {
	Persona     PersonaConfig     `json:"persona"`
	Agent       AgentConfig       `json:"agent"`
	Chat        ChatConfig        `json:"chat"`
	Compactor   CompactorConfig   `json:"compactor"`
	Maintenance MaintenanceConfig `json:"maintenance"`
	Media       MediaConfig       `json:"media"`
	Debug       DebugConfig       `json:"debug"`
}

// PersonaConfig 是当前生效的人格预设，内容来自 persona_presets 表
type PersonaConfig struct {
	PresetID    uint   `json:"preset_id"`
	PresetName  string `json:"preset_name"`
	PersonaCard string `json:"persona_card"`
	Simulation  string `json:"simulation_prompt"`
}

// AgentConfig Agent 决策配置
type AgentConfig struct {
	ThinkInterval         int `json:"think_interval"`
	ThinkDebounceMS       int `json:"think_debounce_ms"`
	MaxStep               int `json:"max_step"`
	MaxGroupConcurrency   int `json:"max_group_concurrency"`
	MaxPrivateConcurrency int `json:"max_private_concurrency"`
	WorkerCount           int `json:"worker_count"`
}

const MaxWorkerCount = 5

// UnmarshalJSON 把旧配置里缺省的 worker_count 归一为 1，显式 0 仍表示关闭
func (a *AgentConfig) UnmarshalJSON(data []byte) error {
	type plain AgentConfig
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, ok := fields["worker_count"]; !ok {
		decoded.WorkerCount = 1
	}
	*a = AgentConfig(decoded)
	return nil
}

// CompactorConfig 控制主模型同步话题压缩
type CompactorConfig struct {
	TriggerRatio      float64 `json:"trigger_ratio"`
	DigestBudgetRatio float64 `json:"digest_budget_ratio"`
	RecentTailRatio   float64 `json:"recent_tail_ratio"`
	MaxSteps          int     `json:"max_steps"`
	IdleMinutes       int     `json:"idle_minutes"`
	IdleMinRatio      float64 `json:"idle_min_ratio"`
}

// MaintenanceConfig 控制长期记忆和话题维护提醒
type MaintenanceConfig struct {
	MemoryTurns int `json:"memory_turns"`
	TopicTurns  int `json:"topic_turns"`
}

// ChatConfig 聊天行为配置
type ChatConfig struct {
	TalkFrequency    float64          `json:"talk_frequency"`
	TypingSimulation bool             `json:"typing_simulation"`
	TypingSpeed      int              `json:"typing_speed"`
	EnableTimeRules  bool             `json:"enable_time_rules"`
	TimeRules        []TimeRuleConfig `json:"time_rules"`
	RateLimit        RateLimitConfig  `json:"rate_limit"`
}

// RateLimitConfig 频率限制配置
type RateLimitConfig struct {
	Enabled     bool    `json:"enabled"`
	PeriodSec   int     `json:"period_sec"`
	MaxMessages int     `json:"max_messages"`
	MinProb     float64 `json:"min_prob"`
}

// TimeRuleConfig 时段规则配置
type TimeRuleConfig struct {
	TimeRange string  `json:"time_range"`
	GroupID   int64   `json:"group_id"`
	TalkValue float64 `json:"talk_value"`
}

// MediaConfig 媒体转码配置；存储路径属于 Bootstrap
type MediaConfig struct {
	MaxBytesMB   int `json:"max_bytes_mb"`
	ImageQuality int `json:"image_quality"`
	NarrowEdge   int `json:"narrow_edge"`
}

// DebugConfig 调试配置
type DebugConfig struct {
	ShowToolCalls bool `json:"show_tool_calls"`
}

// ValidationError 是配置校验错误
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func validationError(message string) error {
	return &ValidationError{Message: message}
}

// Validate 校验动态配置并返回补齐后的副本
func (c RuntimeConfig) Validate() (RuntimeConfig, error) {
	cfg := c
	if strings.TrimSpace(cfg.Persona.PersonaCard) == "" {
		return cfg, validationError("人格卡不能为空")
	}
	if strings.TrimSpace(cfg.Persona.Simulation) == "" {
		return cfg, validationError("仿真提示词不能为空")
	}
	if strings.TrimSpace(cfg.Persona.PresetName) == "" {
		cfg.Persona.PresetName = PersonaPresetFallbackName
	}
	if cfg.Agent.ThinkInterval <= 0 {
		return cfg, validationError("思考间隔必须大于 0")
	}
	if cfg.Agent.ThinkDebounceMS < 0 {
		return cfg, validationError("思考聚合窗口不能小于 0")
	}
	if cfg.Agent.MaxStep <= 0 || cfg.Agent.MaxStep > 1000 {
		return cfg, validationError("单轮最大步数必须在 1 到 1000 之间")
	}
	if cfg.Agent.MaxGroupConcurrency < 0 || cfg.Agent.MaxPrivateConcurrency < 0 {
		return cfg, validationError("并发数不能小于 0")
	}
	if cfg.Agent.WorkerCount < 0 || cfg.Agent.WorkerCount > MaxWorkerCount {
		return cfg, validationError("worker 数量必须在 0 到 5 之间")
	}
	if cfg.Chat.TalkFrequency < 0 || cfg.Chat.TalkFrequency > 1 {
		return cfg, validationError("聊天频率必须在 0 到 1 之间")
	}
	if cfg.Chat.TypingSpeed <= 0 {
		return cfg, validationError("打字速度必须大于 0")
	}
	for _, rule := range cfg.Chat.TimeRules {
		if strings.TrimSpace(rule.TimeRange) == "" {
			return cfg, validationError("时段规则的 time_range 不能为空")
		}
		if rule.TalkValue < 0 || rule.TalkValue > 1 {
			return cfg, validationError("时段规则的 talk_value 必须在 0 到 1 之间")
		}
	}
	rate := cfg.Chat.RateLimit
	if rate.Enabled {
		if rate.PeriodSec <= 0 {
			return cfg, validationError("限流统计周期必须大于 0")
		}
		if rate.MaxMessages <= 0 {
			return cfg, validationError("限流最大消息数必须大于 0")
		}
		if rate.MinProb < 0 || rate.MinProb > 1 {
			return cfg, validationError("限流最小概率必须在 0 到 1 之间")
		}
	}
	compactor := cfg.Compactor
	if compactor.TriggerRatio <= 0 || compactor.TriggerRatio >= 1 {
		return cfg, validationError("压缩触发比例必须在 0 到 1 之间")
	}
	if compactor.DigestBudgetRatio < 0.10 || compactor.DigestBudgetRatio > 0.60 {
		return cfg, validationError("摘要注入预算必须在 10% 到 60% 之间")
	}
	if compactor.RecentTailRatio < 0.05 || compactor.RecentTailRatio > 0.30 {
		return cfg, validationError("近期原文比例必须在 5% 到 30% 之间")
	}
	if compactor.RecentTailRatio+compactor.DigestBudgetRatio >= compactor.TriggerRatio {
		return cfg, validationError("摘要预算与近期原文之和必须小于压缩触发比例")
	}
	if compactor.MaxSteps <= 0 || compactor.MaxSteps > 100 {
		return cfg, validationError("压缩器步数必须在 1 到 100 之间")
	}
	if compactor.IdleMinutes != 0 && (compactor.IdleMinutes < 5 || compactor.IdleMinutes > 240) {
		return cfg, validationError("空闲压缩分钟必须是 0（关闭）或在 5 到 240 之间")
	}
	if compactor.IdleMinRatio < 0.45 || compactor.IdleMinRatio > 0.85 {
		return cfg, validationError("空闲压缩最低占用必须在 45% 到 85% 之间")
	}
	if compactor.IdleMinRatio <= compactor.DigestBudgetRatio+compactor.RecentTailRatio {
		return cfg, validationError("空闲压缩最低占用必须高于摘要预算与近期原文之和")
	}
	if cfg.Maintenance.MemoryTurns < 0 || cfg.Maintenance.MemoryTurns > 50 || cfg.Maintenance.TopicTurns < 0 || cfg.Maintenance.TopicTurns > 50 {
		return cfg, validationError("维护提醒间隔必须在 0 到 50 轮之间")
	}
	if cfg.Media.MaxBytesMB <= 0 {
		return cfg, validationError("媒体大小上限必须大于 0")
	}
	if cfg.Media.ImageQuality <= 0 || cfg.Media.ImageQuality > 100 {
		return cfg, validationError("图片质量必须在 1 到 100 之间")
	}
	if cfg.Media.NarrowEdge <= 0 {
		return cfg, validationError("图片窄边必须大于 0")
	}
	cfg.Chat.TimeRules = append([]TimeRuleConfig(nil), cfg.Chat.TimeRules...)
	return cfg, nil
}
