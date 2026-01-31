package agent

import (
	"amu-bot/internal/config"
	"amu-bot/internal/llm"
	"amu-bot/internal/memory"
	"amu-bot/internal/onebot"
	"amu-bot/internal/persona"
	"amu-bot/internal/tools"
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// Agent 阿沐Agent（基于 eino ReAct）
type Agent struct {
	cfg     *config.Config
	persona *persona.Persona
	memory  *memory.Manager
	model   model.ToolCallingChatModel
	vision  *llm.VisionClient // 多模态视觉模型
	bot     *onebot.Client
	react   *react.Agent
	tools   []tool.BaseTool

	// 消息缓冲
	buffers   map[int64][]*onebot.GroupMessage
	buffersMu sync.RWMutex

	// 发言冷却
	lastSpeak   map[int64]time.Time
	lastSpeakMu sync.RWMutex

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// New 创建 Agent
func New(
	cfg *config.Config,
	p *persona.Persona,
	mem *memory.Manager,
	m model.ToolCallingChatModel,
	vision *llm.VisionClient,
	bot *onebot.Client,
) (*Agent, error) {
	a := &Agent{
		cfg:       cfg,
		persona:   p,
		memory:    mem,
		model:     m,
		vision:    vision,
		bot:       bot,
		buffers:   make(map[int64][]*onebot.GroupMessage),
		lastSpeak: make(map[int64]time.Time),
		stopCh:    make(chan struct{}),
	}

	if err := a.initTools(); err != nil {
		return nil, err
	}
	if err := a.initReact(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Agent) initTools() error {
	toolBuilders := []func() (tool.BaseTool, error){
		// 记忆相关
		func() (tool.BaseTool, error) { return tools.NewSaveMemoryTool() },
		func() (tool.BaseTool, error) { return tools.NewQueryMemoryTool() },
		func() (tool.BaseTool, error) { return tools.NewSaveJargonTool() },
		func() (tool.BaseTool, error) { return tools.NewUpdateMemberProfileTool() },
		func() (tool.BaseTool, error) { return tools.NewGetMemberInfoTool() },
		// 发言相关
		func() (tool.BaseTool, error) { return tools.NewSpeakTool() },
		func() (tool.BaseTool, error) { return tools.NewStayQuietTool() },
		// 时间
		func() (tool.BaseTool, error) { return tools.NewGetCurrentTimeTool() },
		// 群交互
		func() (tool.BaseTool, error) { return tools.NewGetGroupInfoTool() },
		func() (tool.BaseTool, error) { return tools.NewGetGroupMemberDetailTool() },
		func() (tool.BaseTool, error) { return tools.NewPokeTool() },
		func() (tool.BaseTool, error) { return tools.NewReactToMessageTool() },
		func() (tool.BaseTool, error) { return tools.NewRecallMessageTool() },
	}

	for _, build := range toolBuilders {
		t, err := build()
		if err != nil {
			return err
		}
		a.tools = append(a.tools, t)
	}
	return nil
}

func (a *Agent) initReact() error {
	maxStep := a.cfg.Agent.MaxStep
	if maxStep <= 0 {
		maxStep = 5 // 默认最大步数
	}
	agent, err := react.NewAgent(context.Background(), &react.AgentConfig{
		ToolCallingModel: a.model,
		ToolsConfig:      compose.ToolsNodeConfig{Tools: a.tools},
		MaxStep:          maxStep,
	})
	if err != nil {
		return err
	}
	a.react = agent
	return nil
}

// Start 启动
func (a *Agent) Start() {
	a.bot.OnMessage(a.onMessage)
	a.wg.Add(1)
	go a.thinkLoop()
	zap.L().Info("Agent已启动")
}

// Stop 停止
func (a *Agent) Stop() {
	close(a.stopCh)
	a.wg.Wait()
	zap.L().Info("Agent已停止")
}

func (a *Agent) onMessage(msg *onebot.GroupMessage) {
	if !a.cfg.IsGroupEnabled(msg.GroupID) || msg.UserID == a.bot.GetSelfID() {
		return
	}

	// 检测是否通过名字或别名提及了阿沐
	mentionByName := a.persona.IsMentioned(msg.Content)

	a.addBuffer(msg)
	a.memory.AddMessage(memory.MessageLog{
		MessageID:  fmt.Sprintf("%d", msg.MessageID),
		GroupID:    msg.GroupID,
		UserID:     msg.UserID,
		Nickname:   msg.Nickname,
		Content:    msg.Content,
		MsgType:    msg.MessageType,
		MentionAmu: msg.MentionAmu || mentionByName,
		CreatedAt:  msg.Time,
	})

	go a.updateMember(msg)
}

func (a *Agent) addBuffer(msg *onebot.GroupMessage) {
	a.buffersMu.Lock()
	defer a.buffersMu.Unlock()
	buf := a.buffers[msg.GroupID]
	buf = append(buf, msg)
	if len(buf) > a.cfg.Agent.MessageBufferSize {
		buf = buf[len(buf)-a.cfg.Agent.MessageBufferSize:]
	}
	a.buffers[msg.GroupID] = buf
}

func (a *Agent) getBuffer(groupID int64) []*onebot.GroupMessage {
	a.buffersMu.RLock()
	defer a.buffersMu.RUnlock()
	buf := a.buffers[groupID]
	result := make([]*onebot.GroupMessage, len(buf))
	copy(result, buf)
	return result
}

func (a *Agent) clearBuffer(groupID int64) {
	a.buffersMu.Lock()
	a.buffers[groupID] = a.buffers[groupID][:0]
	a.buffersMu.Unlock()
}

func (a *Agent) updateMember(msg *onebot.GroupMessage) {
	p, err := a.memory.GetOrCreateMemberProfile(msg.GroupID, msg.UserID, msg.Nickname)
	if err != nil {
		zap.L().Warn("获取成员画像失败", zap.Error(err))
		return
	}
	p.MsgCount++
	p.LastSpeak = msg.Time
	p.Nickname = msg.Nickname
	if err := a.memory.UpdateMemberProfile(p); err != nil {
		zap.L().Warn("更新成员画像失败", zap.Error(err))
	}
}

func (a *Agent) thinkLoop() {
	defer a.wg.Done()
	ticker := time.NewTicker(time.Duration(a.cfg.Agent.ThinkInterval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.thinkCycle()
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func (a *Agent) thinkCycle() {
	for _, gc := range a.cfg.Groups {
		if !gc.Enabled {
			continue
		}
		msgs := a.getBuffer(gc.GroupID)
		if len(msgs) == 0 {
			continue
		}
		if time.Since(msgs[len(msgs)-1].Time) > time.Duration(a.cfg.Agent.ObserveWindow)*time.Second {
			continue
		}
		// 获取当前的发言概率（考虑时段规则）
		speakProb := a.getSpeakProbability(gc.GroupID)
		if !a.canSpeak(gc.GroupID) || rand.Float64() > speakProb {
			continue
		}
		a.think(gc.GroupID, false)
	}
}

// getSpeakProbability 获取发言概率（考虑时段规则）
func (a *Agent) getSpeakProbability(groupID int64) float64 {
	baseProb := a.cfg.Chat.TalkFrequency
	if !a.cfg.Chat.EnableTimeRules || len(a.cfg.Chat.TimeRules) == 0 {
		return baseProb
	}

	now := time.Now()
	hour := now.Hour()
	minute := now.Minute()
	currentMinutes := hour*60 + minute

	for _, rule := range a.cfg.Chat.TimeRules {
		// 检查是否适用于当前群（0表示全局）
		if rule.GroupID != 0 && rule.GroupID != groupID {
			continue
		}
		// 解析时间范围
		var startHour, startMin, endHour, endMin int
		if _, err := fmt.Sscanf(rule.TimeRange, "%d:%d-%d:%d", &startHour, &startMin, &endHour, &endMin); err != nil {
			continue
		}
		startMinutes := startHour*60 + startMin
		endMinutes := endHour*60 + endMin

		// 检查当前时间是否在范围内
		if startMinutes <= endMinutes {
			// 正常时间范围
			if currentMinutes >= startMinutes && currentMinutes < endMinutes {
				return rule.TalkValue
			}
		} else {
			// 跨午夜的时间范围
			if currentMinutes >= startMinutes || currentMinutes < endMinutes {
				return rule.TalkValue
			}
		}
	}

	return baseProb
}

func (a *Agent) canSpeak(groupID int64) bool {
	a.lastSpeakMu.RLock()
	t, ok := a.lastSpeak[groupID]
	a.lastSpeakMu.RUnlock()
	return !ok || time.Since(t) >= time.Duration(a.cfg.Agent.SpeakCooldown)*time.Second
}

func (a *Agent) recordSpeak(groupID int64) {
	a.lastSpeakMu.Lock()
	a.lastSpeak[groupID] = time.Now()
	a.lastSpeakMu.Unlock()
}

// think 进行思考和决策
func (a *Agent) think(groupID int64, isMention bool) {
	ctx := tools.WithToolContext(context.Background(), &tools.ToolContext{
		GroupID:   groupID,
		MemoryMgr: a.memory,
		Bot:       a.bot,
		SpeakCallback: func(gid int64, content string, replyTo int64) {
			a.doSpeak(gid, content, replyTo)
		},
	})

	// 构建对话上下文
	chatContext := a.buildChatContext(groupID)
	if chatContext == "" {
		return
	}

	// 构建动态 prompt 上下文
	promptCtx := a.buildPromptContext(ctx, groupID, chatContext)

	// 获取说话者信息
	memberInfo := a.getMemberInfo(groupID)

	// 构建消息
	systemPrompt := a.persona.GetSystemPrompt(promptCtx)

	// 添加群专属额外提示词
	if gc := a.cfg.GetGroupConfig(groupID); gc != nil && gc.ExtraPrompt != "" {
		systemPrompt += "\n\n## 群特殊说明\n" + gc.ExtraPrompt
	}

	thinkPrompt := a.persona.GetThinkPrompt(chatContext, memberInfo)
	if isMention {
		thinkPrompt += "\n\n注意：有人@你了，可能在找你说话，你可以看情况回复。"
	}

	// 调试：显示系统提示词
	if a.cfg.Debug.ShowPrompt {
		zap.L().Debug("系统提示词", zap.String("prompt", systemPrompt))
	}
	if a.cfg.Debug.ShowThinking {
		zap.L().Debug("思考提示词", zap.String("prompt", thinkPrompt))
	}

	msgs := []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(thinkPrompt),
	}

	if _, err := a.react.Generate(ctx, msgs); err != nil {
		zap.L().Error("思考失败", zap.Error(err))
	}
}

// buildChatContext 构建聊天上下文
func (a *Agent) buildChatContext(groupID int64) string {
	msgs := a.getBuffer(groupID)
	if len(msgs) == 0 {
		return ""
	}

	ctx := context.Background()
	var b strings.Builder

	for _, m := range msgs {
		mention := ""
		if m.MentionAmu {
			mention = " [@阿沐]"
		}

		// 构建消息内容（包含图片和表情描述）
		content := m.Content

		// 处理表情
		for _, face := range m.Faces {
			content += " " + llm.DescribeFace(face.ID, face.Name)
		}

		// 处理图片（调用 Vision 模型识别）
		for _, img := range m.Images {
			if a.vision != nil && img.URL != "" {
				desc, err := a.vision.DescribeImage(ctx, img.URL)
				if err == nil {
					content += " " + desc
				} else {
					content += " [图片]"
				}
			} else if img.Summary != "" {
				// 使用图片摘要（商城表情等）
				content += fmt.Sprintf(" [表情包:%s]", img.Summary)
			} else {
				content += " [图片]"
			}
		}

		b.WriteString(fmt.Sprintf("[%s] %s(%d)%s: %s\n",
			m.Time.Format("15:04:05"), m.Nickname, m.UserID, mention, content))
	}
	return b.String()
}

// buildPromptContext 构建动态 prompt 上下文
func (a *Agent) buildPromptContext(ctx context.Context, groupID int64, chatContext string) *persona.PromptContext {
	pc := &persona.PromptContext{GroupID: groupID}

	// 获取表达习惯
	if exps, err := a.memory.GetExpressions(groupID, 5); err == nil && len(exps) > 0 {
		var lines []string
		for _, e := range exps {
			lines = append(lines, fmt.Sprintf("- %s时: %s", e.Situation, e.Style))
		}
		pc.Expressions = strings.Join(lines, "\n")
	}

	// 获取黑话
	if jargons, err := a.memory.GetJargons(groupID, 10); err == nil && len(jargons) > 0 {
		var lines []string
		for _, j := range jargons {
			if j.Meaning != "" {
				lines = append(lines, fmt.Sprintf("- %s: %s", j.Content, j.Meaning))
			}
		}
		pc.Jargons = strings.Join(lines, "\n")
	}

	// 获取相关记忆（使用 TopK 配置）
	topK := a.cfg.Memory.LongTerm.TopK
	if topK <= 0 {
		topK = 5
	}
	if mems, err := a.memory.QueryMemory(ctx, chatContext, groupID, "", topK); err == nil && len(mems) > 0 {
		var lines []string
		for _, m := range mems {
			// 使用 ImportanceThreshold 过滤低重要性记忆
			if m.Importance >= a.cfg.Memory.ImportanceThreshold {
				lines = append(lines, fmt.Sprintf("- [%s] %s", m.Type, m.Content))
			}
		}
		if len(lines) > 0 {
			pc.Memories = strings.Join(lines, "\n")
			// 调试：显示记忆检索结果
			if a.cfg.Debug.ShowMemory {
				zap.L().Debug("检索到相关记忆", zap.Int("count", len(lines)))
			}
		}
	}

	// 获取近期话题
	if topics, err := a.memory.GetRecentTopics(groupID, 3); err == nil && len(topics) > 0 {
		var lines []string
		for _, t := range topics {
			lines = append(lines, fmt.Sprintf("- %s: %s", t.Topic, t.Summary))
		}
		pc.RecentTopics = strings.Join(lines, "\n")
	}

	return pc
}

// getMemberInfo 获取当前说话者信息
func (a *Agent) getMemberInfo(groupID int64) string {
	msgs := a.getBuffer(groupID)
	if len(msgs) == 0 {
		return ""
	}

	// 获取最后一个说话者的信息
	lastMsg := msgs[len(msgs)-1]
	profile, err := a.memory.GetMemberProfile(groupID, lastMsg.UserID)
	if err != nil {
		return ""
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("昵称: %s", profile.Nickname))
	if profile.SpeakStyle != "" {
		parts = append(parts, fmt.Sprintf("说话风格: %s", profile.SpeakStyle))
	}
	if profile.Interests != "" {
		parts = append(parts, fmt.Sprintf("兴趣: %s", profile.Interests))
	}
	return strings.Join(parts, ", ")
}

// doSpeak 执行发言
func (a *Agent) doSpeak(groupID int64, content string, replyTo int64) {
	// 模拟打字延迟
	if a.cfg.Chat.TypingSimulation {
		typingSpeed := a.cfg.Chat.TypingSpeed
		if typingSpeed <= 0 {
			typingSpeed = 10 // 默认每秒10字符
		}
		delay := time.Duration(float64(len([]rune(content)))/float64(typingSpeed)*1000) * time.Millisecond
		if delay > 5*time.Second {
			delay = 5 * time.Second
		}
		if delay < 500*time.Millisecond {
			delay = 500 * time.Millisecond
		}
		time.Sleep(delay)
	}

	var err error
	if replyTo > 0 {
		_, err = a.bot.SendGroupMessageReply(groupID, content, replyTo)
	} else {
		_, err = a.bot.SendGroupMessage(groupID, content)
	}

	if err != nil {
		zap.L().Error("发言失败", zap.Int64("group_id", groupID), zap.Error(err))
		return
	}

	a.recordSpeak(groupID)
	zap.L().Info("发言成功", zap.Int64("group_id", groupID), zap.String("content", content))
	a.clearBuffer(groupID)
}
