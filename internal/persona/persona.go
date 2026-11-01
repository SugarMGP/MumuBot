package persona

import (
	"fmt"
	"strings"

	"mumu-bot/internal/config"
)

// 会话模式，决定装配时保留哪些段落
const (
	ModeGroup   = "group"
	ModePrivate = "private"
)

const (
	markerGroup   = "群"
	markerPrivate = "私"
	markerCommon  = "通用"
)

// Persona 是当前生效的人格预设，按模式预先裁好两段静态文本
type Persona struct {
	id          uint
	name        string
	groupText   string
	privateText string
}

// NewPersona 用配置里的两份文本构造人格，构造时完成按模式切块
func NewPersona(cfg *config.PersonaConfig) (*Persona, error) {
	if cfg == nil {
		return nil, fmt.Errorf("人格预设未初始化")
	}
	card := strings.TrimSpace(cfg.PersonaCard)
	simulation := strings.TrimSpace(cfg.Simulation)
	if card == "" || simulation == "" {
		return nil, fmt.Errorf("人格卡与仿真提示词不能为空")
	}
	return &Persona{
		id:          cfg.PresetID,
		name:        cfg.PresetName,
		groupText:   assemble(card, simulation, markerGroup),
		privateText: assemble(card, simulation, markerPrivate),
	}, nil
}

// Instruction 返回某个模式的系统提示词，同一模式恒定不变
func (p *Persona) Instruction(mode string) string {
	if p == nil {
		return ""
	}
	if mode == ModePrivate {
		return p.privateText
	}
	return p.groupText
}

// assemble 把人格卡与仿真提示词按模式裁好并拼接
func assemble(card, simulation, mode string) string {
	return strings.TrimSpace(sliceDoc(card, mode)) + "\n\n" + strings.TrimSpace(sliceDoc(simulation, mode))
}

// sliceDoc 裁剪出目标模式的段落；目标模式为空时依次退回另一模式、整份去标记文本
func sliceDoc(text, mode string) string {
	if sliced := sliceByMode(text, mode); strings.TrimSpace(sliced) != "" {
		return sliced
	}
	other := markerPrivate
	if mode == markerPrivate {
		other = markerGroup
	}
	if sliced := sliceByMode(text, other); strings.TrimSpace(sliced) != "" {
		return sliced
	}
	return stripMarkers(text)
}

// stripMarkers 去掉所有标记行，保留其余正文
func stripMarkers(text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if _, _, ok := parseMarker(strings.TrimSpace(line)); ok {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// sliceByMode 按行首标记切出通用段与目标模式的段落
func sliceByMode(text, mode string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	current := markerCommon
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// 二级标题是块的天然边界，回到通用
		if strings.HasPrefix(trimmed, "## ") {
			current = markerCommon
		}
		if marker, rest, ok := parseMarker(trimmed); ok {
			current = marker
			if rest != "" {
				if current == markerCommon || current == mode {
					kept = append(kept, rest)
				}
			}
			continue
		}
		if current != markerCommon && current != mode {
			continue
		}
		kept = append(kept, line)
	}
	return collapseBlank(strings.Join(kept, "\n"))
}

// parseMarker 识别行首 [群] / [私] / [通用] 标记，返回模式与标记后的剩余正文
func parseMarker(line string) (string, string, bool) {
	if len(line) < 3 || line[0] != '[' {
		return "", "", false
	}
	end := strings.IndexByte(line, ']')
	if end < 0 {
		return "", "", false
	}
	inner := strings.TrimSpace(line[1:end])
	switch inner {
	case markerGroup, markerPrivate, markerCommon:
		return inner, strings.TrimSpace(line[end+1:]), true
	default:
		return "", "", false
	}
}

// collapseBlank 压掉连续空行，保证装配结果稳定
func collapseBlank(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blank++
			if blank > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blank = 0
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
