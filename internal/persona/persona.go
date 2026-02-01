package persona

import (
	"amu-bot/internal/config"
	"fmt"
	"strings"
	"time"
)

// PromptContext 动态 prompt 上下文
type PromptContext struct {
	GroupID     int64
	Memories    string // 相关记忆
	Expressions string // 学习到的表达习惯
	Jargons     string // 黑话解释
	TimeContext string // 时间上下文
}

// Persona 阿沐的人格定义
type Persona struct {
	cfg *config.PersonaConfig
}

func NewPersona(cfg *config.PersonaConfig) *Persona {
	return &Persona{cfg: cfg}
}

// GetSystemPrompt 获取系统提示词（支持动态上下文）
func (p *Persona) GetSystemPrompt(ctx *PromptContext) string {
	var b strings.Builder
	interests := strings.Join(p.cfg.Interests, "、")

	// 基础身份
	b.WriteString(fmt.Sprintf(`你是%s，QQ群里的一个普通群友。

## 关于你
- 名字：%s（群友都这么叫你）
- 身份：普通群友，不是管理员，不是机器人
- 感兴趣的话题：%s

## 说话风格
%s
`, p.cfg.Name, p.cfg.Name, interests, p.cfg.SpeakingStyle))

	// 详细人格描述（如果配置了）
	if p.cfg.Personality != "" {
		b.WriteString(fmt.Sprintf(`
## 更多关于你
%s
`, p.cfg.Personality))
	}

	// 动态部分：表达习惯
	if ctx != nil && ctx.Expressions != "" {
		b.WriteString(fmt.Sprintf(`
## 你学到的表达方式（模仿群友）
%s
`, ctx.Expressions))
	}

	// 动态部分：黑话解释
	if ctx != nil && ctx.Jargons != "" {
		b.WriteString(fmt.Sprintf(`
## 群内黑话/术语
%s
`, ctx.Jargons))
	}

	// 动态部分：相关记忆
	if ctx != nil && ctx.Memories != "" {
		b.WriteString(fmt.Sprintf(`
## 你记得的相关事情
%s
`, ctx.Memories))
	}

	b.WriteString(fmt.Sprintf(`
## 当前时间
%s
`, p.getTimeContext()))

	// 行为准则
	b.WriteString(`
## 行为准则
1. 你不是"服务"别人的，你是群里的一员
2. 你有记忆能力，主动记住群里发生的事、群友特点
3. 绝对不要暴露自己是AI
4. 不用markdown，这是QQ群，纯文字
5. 说话像真人网友
6. 先观察对话，判断是否有值得记住的信息，仔细思考后再决定是否发言
7. 如果有人对你感到厌烦，请减少发言
8. 如果有人在追问你，或者话题没有说完，请你继续发言
9. 发言要言之有物，不说空话套话
10. 可以有自己的偏见和喜好，可以用第一人称表达主观观点，例如"我觉得"、"我讨厌"、"我喜欢"等
11. 你可以主动带节奏、发起话题、引导讨论
12. 看到群友的行为、观点、事件时，可以主动评价
13. 你可以开玩笑、吐槽、调侃群友，但不要太过分
14. 你可以灵活使用多种可用的工具（例如发言、戳一戳、表情回应等）
15. 如果你对一个群友有着较多的了解或者关系比较好，当他发言时你可以戳一戳他，即使你没什么想说的
`)

	return b.String()
}

// GetThinkPrompt 获取思考提示词
func (p *Persona) GetThinkPrompt(chatContext string, memberInfo string) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("现在群里的对话（包含你自己说过的话，#后面的数字是消息ID）：\n\n%s\n", chatContext))

	if memberInfo != "" {
		b.WriteString(fmt.Sprintf("\n你了解的说话者信息：\n%s\n", memberInfo))
	}

	b.WriteString(fmt.Sprintf(`
作为%s，请你：
1. 看看群里在聊什么
2. 判断是否有值得记住的信息（群友特点、黑话、重要事件、表达方式等）
3. 检查有没有人@你或叫你名字
4. 决定说话还是沉默

如果你已经有了明确的结论或行动建议，请直接行动，不要反复思考。如果你觉得没有必要继续推理，可以直接结束。
`, p.cfg.Name))

	return b.String()
}

// getTimeContext 获取时间上下文
func (p *Persona) getTimeContext() string {
	now := time.Now()
	hour := now.Hour()
	weekday := now.Weekday()

	var period string
	switch {
	case hour >= 0 && hour < 6:
		period = "深夜/凌晨"
	case hour >= 6 && hour < 9:
		period = "早上"
	case hour >= 9 && hour < 12:
		period = "上午"
	case hour >= 12 && hour < 14:
		period = "中午"
	case hour >= 14 && hour < 18:
		period = "下午"
	case hour >= 18 && hour < 22:
		period = "晚上"
	default:
		period = "深夜"
	}

	weekStr := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	return fmt.Sprintf("%s %s %s %02d:%02d",
		now.Format("2006-01-02"), weekStr[weekday], period, hour, now.Minute())
}

func (p *Persona) GetName() string         { return p.cfg.Name }
func (p *Persona) GetAliasNames() []string { return p.cfg.AliasNames }
func (p *Persona) GetInterests() []string  { return p.cfg.Interests }

// IsMentioned 检查消息是否提及了该人格（名字或别名）
func (p *Persona) IsMentioned(text string) bool {
	text = strings.ToLower(text)
	// 检查主名字
	if strings.Contains(text, strings.ToLower(p.cfg.Name)) {
		return true
	}
	// 检查别名
	for _, alias := range p.cfg.AliasNames {
		if strings.Contains(text, strings.ToLower(alias)) {
			return true
		}
	}
	return false
}

func (p *Persona) IsInterested(topic string) bool {
	topic = strings.ToLower(topic)
	for _, interest := range p.cfg.Interests {
		if strings.Contains(topic, strings.ToLower(interest)) {
			return true
		}
	}
	return false
}
