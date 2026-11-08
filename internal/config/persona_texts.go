package config

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const PersonaTextLimit = 8000

// PersonaTexts 是一份预设共同保存和切换的五段文本
type PersonaTexts struct {
	PersonaCard  string `gorm:"column:persona_card;type:text;not null" json:"persona_card"`
	CommonRules  string `gorm:"column:common_rules;type:text;not null" json:"common_rules"`
	QQPrompt     string `gorm:"column:qq_prompt;type:text;not null" json:"qq_prompt"`
	LifePrompt   string `gorm:"column:life_prompt;type:text;not null" json:"life_prompt"`
	WorkerPrompt string `gorm:"column:worker_prompt;type:text;not null" json:"worker_prompt"`
}

// Validate 在配置和请求入口校验完整文本，不按正文标记解释作用域
func (p PersonaTexts) Validate() error {
	for _, field := range []struct {
		name, text string
	}{
		{"persona_card", p.PersonaCard},
		{"common_rules", p.CommonRules},
		{"qq_prompt", p.QQPrompt},
		{"life_prompt", p.LifePrompt},
		{"worker_prompt", p.WorkerPrompt},
	} {
		if strings.TrimSpace(field.text) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
		if utf8.RuneCountInString(field.text) > PersonaTextLimit {
			return fmt.Errorf("%s exceeds %d characters", field.name, PersonaTextLimit)
		}
	}
	return nil
}
