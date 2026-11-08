package config

import _ "embed"

// PersonaPresetFallbackName 是没有可用预设时显示的名字
const PersonaPresetFallbackName = "内置预设"

//go:embed persona_builtin_card.md
var builtinPersonaCard string

//go:embed persona_builtin_rules.md
var builtinCommonRules string

//go:embed persona_builtin_qq.md
var builtinQQPrompt string

//go:embed persona_builtin_life.md
var builtinLifePrompt string

//go:embed persona_builtin_worker.md
var builtinWorkerPrompt string

// BuiltinPersonaTexts 返回完整的内置预设文本
func BuiltinPersonaTexts() PersonaTexts {
	return PersonaTexts{
		PersonaCard: builtinPersonaCard, CommonRules: builtinCommonRules,
		QQPrompt: builtinQQPrompt, LifePrompt: builtinLifePrompt, WorkerPrompt: builtinWorkerPrompt,
	}
}
