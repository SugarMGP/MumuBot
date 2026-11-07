package config

import _ "embed"

// PersonaPresetFallbackName 是没有可用预设时显示的名字
const PersonaPresetFallbackName = "内置预设"

//go:embed persona_builtin_card.md
var builtinPersonaCard string

//go:embed persona_builtin_simulation.md
var builtinSimulationPrompt string

//go:embed persona_builtin_life.md
var builtinLifePrompt string

// BuiltinPersonaTexts 返回代码内置的人格卡、仿真提示词与生活职责
func BuiltinPersonaTexts() (string, string, string) {
	return builtinPersonaCard, builtinSimulationPrompt, builtinLifePrompt
}

// BuiltinLifePrompt 返回代码内置的生活职责，供旧预设缺字段时兜底
func BuiltinLifePrompt() string {
	return builtinLifePrompt
}
