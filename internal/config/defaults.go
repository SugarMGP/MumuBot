package config

// DefaultRuntime 返回代码内置的默认动态配置
func DefaultRuntime() RuntimeConfig {
	return RuntimeConfig{
		Persona: PersonaConfig{
			PresetName:  PersonaPresetFallbackName,
			PersonaCard: builtinPersonaCard,
			Simulation:  builtinSimulationPrompt,
			LifePrompt:  builtinLifePrompt,
		},
		Identity: IdentityConfig{Name: "沐沐"},
		Agent: AgentConfig{
			ThinkInterval:         15,
			ThinkDebounceMS:       5000,
			MaxStep:               50,
			LifeMaxStep:           50,
			WorkerMaxStep:         30,
			MaxGroupConcurrency:   3,
			MaxPrivateConcurrency: 0,
			WorkerCount:           1,
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
			TriggerRatio:      0.90,
			DigestBudgetRatio: 0.30,
			RecentTailRatio:   0.10,
			MaxSteps:          12,
			IdleMinutes:       20,
			IdleMinRatio:      0.60,
		},
		Maintenance: MaintenanceConfig{MemoryTurns: 8, TopicTurns: 6},
		Retention: RetentionConfig{
			InitialHalfLifeDays: 30, MaxHalfLifeDays: 180,
			ReinforceFactor: 0.9, ForgetThreshold: 0.10, HistoryDays: 180,
		},
		Media: MediaConfig{
			MaxBytesMB:   10,
			ImageQuality: 75,
			NarrowEdge:   1080,
		},
	}
}
