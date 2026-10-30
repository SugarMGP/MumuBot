package config

// DefaultRuntime 返回代码内置的默认动态配置
func DefaultRuntime() RuntimeConfig {
	return RuntimeConfig{
		Persona: PersonaConfig{
			Name:                  "沐沐大魔王",
			AliasNames:            []string{"mumu", "牢沐", "沐沐"},
			Interests:             []string{"编程", "数码科技", "二次元", "单机游戏", "少女乐队番", "异世界番", "Minecraft"},
			GroupPromptTemplate:   defaultGroupPrompt,
			PrivatePromptTemplate: defaultPrivatePrompt,
		},
		Agent: AgentConfig{
			ThinkInterval:         15,
			ThinkDebounceMS:       5000,
			MaxStep:               50,
			MaxGroupConcurrency:   3,
			MaxPrivateConcurrency: 0,
		},
		Chat: ChatConfig{
			TalkFrequency:    0.7,
			TypingSimulation: true,
			TypingSpeed:      6,
			EnableTimeRules:  false,
			TimeRules: []TimeRuleConfig{
				{TimeRange: "03:00-10:00", GroupID: 0, TalkValue: 0.2},
			},
			RateLimit: RateLimitConfig{Enabled: true, PeriodSec: 300, MaxMessages: 30, MinProb: 0.1},
		},
		Compactor: CompactorConfig{
			TriggerRatio:    0.90,
			TargetRatio:     0.40,
			RecentTailRatio: 0.10,
			MaxSteps:        12,
		},
		Media: MediaConfig{
			MaxBytesMB:   10,
			ImageQuality: 75,
			NarrowEdge:   1080,
		},
	}
}
