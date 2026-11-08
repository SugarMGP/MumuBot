package persona

import (
	"fmt"
	"strings"

	"mumu-bot/internal/config"
)

const (
	ModeGroup   = "group"
	ModePrivate = "private"
	ModeLife    = "life"
	ModeWorker  = "worker"
)

// Persona 把共同人格和场合职责装成固定文本，正文不含配置语法
type Persona struct {
	qqText     string
	lifeText   string
	workerText string
}

// NewPersona 使用已经在入口校验的预设和全局身份装配提示词
func NewPersona(cfg *config.PersonaConfig, identity config.IdentityConfig) (*Persona, error) {
	if cfg == nil {
		return nil, fmt.Errorf("人格预设未初始化")
	}
	common := strings.TrimSpace(cfg.PersonaCard) + "\n\n" + strings.TrimSpace(cfg.CommonRules)
	values := IdentityFromConfig(identity)
	return &Persona{
		qqText:     Render(common+"\n\n"+strings.TrimSpace(cfg.QQPrompt), values),
		lifeText:   Render(common+"\n\n"+strings.TrimSpace(cfg.LifePrompt), values),
		workerText: Render(common+"\n\n"+strings.TrimSpace(cfg.WorkerPrompt), values),
	}, nil
}

// Instruction 返回场合文本；会话身份是程序提供的事实，不从正文猜测
func (p *Persona) Instruction(mode string) string {
	if p == nil {
		return ""
	}
	switch mode {
	case ModeLife:
		return p.lifeText
	case ModeWorker:
		return p.workerText
	default:
		return p.qqText
	}
}

// SessionInstruction 为当前会话的固定提示补充真实身份
func (p *Persona) SessionInstruction(kind string, targetID int64) string {
	return p.Instruction(kind) + fmt.Sprintf("\n\n当前会话：%s:%d", kind, targetID)
}
