package config

import (
	"strings"
	"text/template"
)

// RuntimeConfig 是进程内不可变的动态配置快照
type RuntimeConfig struct {
	Persona   PersonaConfig   `json:"persona"`
	Agent     AgentConfig     `json:"agent"`
	Chat      ChatConfig      `json:"chat"`
	Compactor CompactorConfig `json:"compactor"`
	Media     MediaConfig     `json:"media"`
	Debug     DebugConfig     `json:"debug"`
}

// PersonaConfig 人格配置，提示词模板由后台维护
type PersonaConfig struct {
	Name                  string   `json:"name"`
	AliasNames            []string `json:"alias_names"`
	Interests             []string `json:"interests"`
	GroupPromptTemplate   string   `json:"group_prompt_template"`
	PrivatePromptTemplate string   `json:"private_prompt_template"`
}

// AgentConfig Agent 决策配置
type AgentConfig struct {
	ThinkInterval         int `json:"think_interval"`
	ThinkDebounceMS       int `json:"think_debounce_ms"`
	MaxStep               int `json:"max_step"`
	MaxGroupConcurrency   int `json:"max_group_concurrency"`
	MaxPrivateConcurrency int `json:"max_private_concurrency"`
}

// CompactorConfig 控制主模型同步话题压缩
type CompactorConfig struct {
	TriggerRatio    float64 `json:"trigger_ratio"`
	TargetRatio     float64 `json:"target_ratio"`
	RecentTailRatio float64 `json:"recent_tail_ratio"`
	MaxSteps        int     `json:"max_steps"`
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
	if strings.TrimSpace(cfg.Persona.Name) == "" {
		return cfg, validationError("人格名称不能为空")
	}
	if err := validatePrompt("群聊人格模板", cfg.Persona.GroupPromptTemplate, cfg.Persona); err != nil {
		return cfg, err
	}
	if err := validatePrompt("私聊人格模板", cfg.Persona.PrivatePromptTemplate, cfg.Persona); err != nil {
		return cfg, err
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
	if compactor.TargetRatio <= 0 || compactor.TargetRatio > 0.55 || compactor.TargetRatio >= compactor.TriggerRatio {
		return cfg, validationError("压缩目标必须大于 0、不超过 0.55 且小于触发比例")
	}
	if compactor.RecentTailRatio <= 0 || compactor.RecentTailRatio >= compactor.TargetRatio {
		return cfg, validationError("近期原文比例必须大于 0 且小于压缩目标")
	}
	if compactor.MaxSteps <= 0 || compactor.MaxSteps > 100 {
		return cfg, validationError("压缩器步数必须在 1 到 100 之间")
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
	cfg.Persona.AliasNames = append([]string(nil), cfg.Persona.AliasNames...)
	cfg.Persona.Interests = append([]string(nil), cfg.Persona.Interests...)
	return cfg, nil
}
func validatePrompt(name, body string, persona PersonaConfig) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return validationError(name + "不能为空")
	}
	tmpl, err := template.New(name).Option("missingkey=error").Parse(body)
	if err != nil {
		return validationError(name + "格式错误: " + err.Error())
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, map[string]string{
		"Name":      persona.Name,
		"Interests": strings.Join(persona.Interests, "、"),
	}); err != nil {
		return validationError(name + "渲染失败: " + err.Error())
	}
	if strings.TrimSpace(b.String()) == "" {
		return validationError(name + "渲染结果为空")
	}
	return nil
}
