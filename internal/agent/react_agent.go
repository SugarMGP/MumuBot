package agent

import (
	"amu-bot/internal/config"
	"amu-bot/internal/llm"
	"amu-bot/internal/mcp"
	"amu-bot/internal/memory"
	"amu-bot/internal/onebot"
	"amu-bot/internal/persona"
	"amu-bot/internal/tools"
	"amu-bot/internal/utils"
	fileutils "amu-bot/internal/utils"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
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
	mcpMgr  *mcp.MCPManager // MCP管理器

	// 消息缓冲（使用 ring buffer 避免扩容缩容开销）
	buffers   map[int64]*utils.RingBuffer[*onebot.GroupMessage]
	buffersMu sync.RWMutex // 保护 map 本身的并发访问

	// 发言冷却
	lastSpeak   map[int64]time.Time
	lastSpeakMu sync.RWMutex

	// 正在处理中的群组（防止重复思考）和最后处理时间
	processing        map[int64]bool
	lastProcessedTime map[int64]time.Time
	processingMu      sync.RWMutex

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
		cfg:               cfg,
		persona:           p,
		memory:            mem,
		model:             m,
		vision:            vision,
		bot:               bot,
		buffers:           make(map[int64]*utils.RingBuffer[*onebot.GroupMessage]),
		lastSpeak:         make(map[int64]time.Time),
		processing:        make(map[int64]bool),
		lastProcessedTime: make(map[int64]time.Time),
		stopCh:            make(chan struct{}),
	}

	// 初始化 MCP 管理器
	a.mcpMgr = mcp.NewMCPManager()
	if err := a.mcpMgr.LoadFromConfig("config/mcp.json"); err != nil {
		zap.L().Warn("加载MCP配置失败", zap.Error(err))
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
		func() (tool.BaseTool, error) { return tools.NewGetRecentMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewGetExpressionsTool() },
		func() (tool.BaseTool, error) { return tools.NewSaveExpressionTool() },
		// 审核工具
		func() (tool.BaseTool, error) { return tools.NewGetUncheckedExpressionsTool() },
		func() (tool.BaseTool, error) { return tools.NewReviewExpressionTool() },
		func() (tool.BaseTool, error) { return tools.NewGetUnverifiedJargonsTool() },
		func() (tool.BaseTool, error) { return tools.NewReviewJargonTool() },
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
		// 表情包相关
		func() (tool.BaseTool, error) { return tools.NewSearchStickersTool() },
		func() (tool.BaseTool, error) { return tools.NewSendStickerTool() },
		// 群信息
		func() (tool.BaseTool, error) { return tools.NewGetGroupNoticesTool() },
		func() (tool.BaseTool, error) { return tools.NewGetEssenceMessagesTool() },
		func() (tool.BaseTool, error) { return tools.NewGetMessageReactionsTool() },
	}

	for _, build := range toolBuilders {
		t, err := build()
		if err != nil {
			return err
		}
		a.tools = append(a.tools, t)
	}

	// 添加 MCP 工具
	mcpTools := a.mcpMgr.GetTools()
	if len(mcpTools) > 0 {
		a.tools = append(a.tools, mcpTools...)
		zap.L().Info("已加载MCP工具", zap.Int("count", len(mcpTools)))
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
	// 关闭 MCP 连接
	if a.mcpMgr != nil {
		a.mcpMgr.Close()
	}
	zap.L().Info("Agent已停止")
}

func (a *Agent) onMessage(msg *onebot.GroupMessage) {
	if !a.cfg.IsGroupEnabled(msg.GroupID) {
		return
	}

	// 自身发送的消息也进入缓冲区和记录，但不触发思考
	isSelf := msg.UserID == a.bot.GetSelfID()

	// 检测是否通过名字或别名提及了阿沐
	mentionByName := a.persona.IsMentioned(msg.Content)
	isMention := msg.MentionAmu || mentionByName

	a.addBuffer(msg)
	_ = a.memory.AddMessage(memory.MessageLog{
		MessageID:  fmt.Sprintf("%d", msg.MessageID),
		GroupID:    msg.GroupID,
		UserID:     msg.UserID,
		Nickname:   msg.Nickname,
		Content:    a.buildContentWithAt(msg),
		MsgType:    msg.MessageType,
		MentionAmu: isMention,
		CreatedAt:  msg.Time,
	})

	if isSelf {
		return
	}

	go a.updateMember(msg)

	// 如果被 @ 了，立即触发一次思考（跳过等待）
	if isMention {
		go a.think(msg.GroupID, true)
	}
}

// buildContentWithAt 构建包含 @ 信息的消息内容（用于存储到 MessageLog）
func (a *Agent) buildContentWithAt(msg *onebot.GroupMessage) string {
	var atParts []string

	// @全体成员
	if msg.MentionAll {
		atParts = append(atParts, "@全体成员")
	}

	// @其他用户
	for _, uid := range msg.AtList {
		if uid != a.bot.GetSelfID() {
			atParts = append(atParts, fmt.Sprintf("@%d", uid))
		} else {
			atParts = append(atParts, "@"+a.persona.GetName())
		}
	}

	if len(atParts) == 0 {
		return msg.Content
	}

	return strings.Join(atParts, " ") + " " + msg.Content
}

func (a *Agent) addBuffer(msg *onebot.GroupMessage) {
	a.buffersMu.Lock()
	buf, ok := a.buffers[msg.GroupID]
	if !ok {
		// 确保缓冲区大小有效
		bufSize := a.cfg.Agent.MessageBufferSize
		if bufSize <= 0 {
			bufSize = 50 // 默认缓冲区大小
		}
		buf = utils.NewRingBuffer[*onebot.GroupMessage](bufSize)
		a.buffers[msg.GroupID] = buf
	}
	a.buffersMu.Unlock()

	buf.Push(msg)
}

func (a *Agent) getBuffer(groupID int64) []*onebot.GroupMessage {
	a.buffersMu.RLock()
	buf, ok := a.buffers[groupID]
	a.buffersMu.RUnlock()

	if !ok || buf.IsEmpty() {
		return nil
	}
	return buf.GetAll()
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

		lastMsg := msgs[len(msgs)-1]

		// 如果该消息的时间不晚于最后处理时间，说明是旧消息，跳过
		a.processingMu.RLock()
		lastTime := a.lastProcessedTime[gc.GroupID]
		a.processingMu.RUnlock()
		if !lastTime.IsZero() && lastMsg.Time.Before(lastTime) {
			continue
		}

		// 如果最后一条消息是自己发的，跳过
		if lastMsg.UserID == a.bot.GetSelfID() {
			continue
		}

		// 如果最后一条消息是 @提及，已经在 onMessage 中触发了即时思考，这里跳过
		if a.persona.IsMentioned(lastMsg.Content) || lastMsg.MentionAmu {
			continue
		}

		if time.Since(lastMsg.Time) > time.Duration(a.cfg.Agent.ObserveWindow)*time.Second {
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
	// 并发锁：确保同一时间一个群只有一个思考进程
	a.processingMu.Lock()
	if a.processing[groupID] {
		a.processingMu.Unlock()
		return
	}
	a.processing[groupID] = true
	a.processingMu.Unlock()

	defer func() {
		a.processingMu.Lock()
		a.processing[groupID] = false
		a.processingMu.Unlock()
	}()

	// 创建可取消的 context，用于 stayQuiet 强制停止思考
	ctxWithCancel, cancelThinking := context.WithCancel(context.Background())
	defer cancelThinking()

	ctx := tools.WithToolContext(ctxWithCancel, &tools.ToolContext{
		GroupID:   groupID,
		MemoryMgr: a.memory,
		Bot:       a.bot,
		SpeakCallback: func(gid int64, content string, replyTo int64) int64 {
			return a.doSpeak(gid, content, replyTo)
		},
		StopThinking: cancelThinking, // 传递取消函数
	})

	// 获取上次处理时间（用于判断哪些是新消息）
	a.processingMu.Lock()
	lastProcessedTime := a.lastProcessedTime[groupID]
	a.lastProcessedTime[groupID] = time.Now()
	a.processingMu.Unlock()

	// 构建对话上下文
	chatContext := a.buildChatContext(groupID, lastProcessedTime)
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

	// 注入上次处理时间到提示词
	if !lastProcessedTime.IsZero() {
		thinkPrompt += fmt.Sprintf("\n\n注意：你上次处理消息的时间是 [%s]，在那之后的消息是新发生的。请结合上下文判断是否需要回复新消息。",
			lastProcessedTime.Format("15:04:05"))
	}

	if isMention {
		thinkPrompt += "\n\n注意：有人提到你了，可能在找你说话，你可以看情况回复。"
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

	// 设置超时时间（默认60秒），防止LLM请求无限阻塞
	timeout := 60 * time.Second
	ctxWithTimeout, cancelTimeout := context.WithTimeout(ctx, timeout)
	defer cancelTimeout()

	if _, err := a.react.Generate(ctxWithTimeout, msgs); err != nil {
		// 区分是超时还是主动取消（stayQuiet）
		if errors.Is(ctxWithTimeout.Err(), context.DeadlineExceeded) {
			zap.L().Warn("思考超时", zap.Int64("group_id", groupID), zap.Duration("timeout", timeout))
		} else if errors.Is(ctxWithCancel.Err(), context.Canceled) {
			// stayQuiet 触发的主动停止，这是正常行为，不记录错误
			zap.L().Debug("思考结束（stayQuiet）", zap.Int64("group_id", groupID))
		} else {
			zap.L().Error("思考失败", zap.Int64("group_id", groupID), zap.Error(err))
		}
	}
}

// buildChatContext 构建聊天上下文
func (a *Agent) buildChatContext(groupID int64, lastProcessedTime time.Time) string {
	msgs := a.getBuffer(groupID)
	if len(msgs) == 0 {
		return ""
	}

	ctx := context.Background()
	var b strings.Builder

	for _, m := range msgs {
		// 判断是否是新消息（用于决定是否自动保存表情包）
		isNewMessage := lastProcessedTime.IsZero() || m.Time.After(lastProcessedTime)
		// 构建@信息
		var mentionParts []string
		if m.MentionAmu {
			mentionParts = append(mentionParts, "@你")
		}
		// 显示@的其他用户
		for _, atUserID := range m.AtList {
			if atUserID != a.bot.GetSelfID() {
				mentionParts = append(mentionParts, fmt.Sprintf("@%d", atUserID))
			}
		}
		mention := ""
		if len(mentionParts) > 0 {
			mention = " [" + strings.Join(mentionParts, ",") + "]"
		}

		// 构建回复信息
		replyInfo := ""
		if m.Reply != nil {
			if m.Reply.Content != "" {
				// 截断过长的内容
				replyContent := m.Reply.Content
				if len(replyContent) > 50 {
					replyContent = replyContent[:50] + "..."
				}
				replyInfo = fmt.Sprintf(" [回复 #%d %s:\"%s\"]", m.Reply.MessageID, m.Reply.Nickname, replyContent)
			} else {
				replyInfo = fmt.Sprintf(" [回复 #%d]", m.Reply.MessageID)
			}
		}

		// 构建消息内容（包含图片和表情描述）
		content := m.Content

		// 处理表情
		for _, face := range m.Faces {
			content += " " + llm.DescribeFace(face.ID, face.Name)
		}

		// 处理图片（调用 Vision 模型识别）
		for _, img := range m.Images {
			if img.SubType == 1 {
				// 表情包类型 - 自动保存
				var desc string
				if a.vision != nil && img.URL != "" {
					if d, err := a.vision.DescribeImage(ctx, img.URL); err == nil {
						desc = d
					}
				}
				if desc == "" && img.Summary != "" {
					desc = img.Summary
				}
				// 自动保存表情包（仅对新消息且开启了自动保存）
				if img.URL != "" && a.cfg.Sticker.AutoSave && isNewMessage {
					go a.autoSaveSticker(img.URL, desc)
				}
				// 展示给 Agent
				if desc != "" {
					content += fmt.Sprintf(" [表情包 描述:%s]", desc)
				} else {
					content += " [表情包]"
				}
			} else {
				// 普通图片
				if a.vision != nil && img.URL != "" {
					desc, err := a.vision.DescribeImage(ctx, img.URL)
					if err == nil {
						content += " " + desc
					} else {
						content += " [图片]"
					}
				} else {
					content += " [图片]"
				}
			}
		}

		// 在消息开头添加消息ID，方便模型引用
		b.WriteString(fmt.Sprintf("[%s] #%d %s(%d):%s%s %s\n",
			m.Time.Format("15:04:05"), m.MessageID, m.Nickname, m.UserID, replyInfo, mention, content))
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
			if m.Importance >= a.cfg.Memory.LongTerm.ImportanceThreshold {
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

// doSpeak 执行发言，返回消息ID
func (a *Agent) doSpeak(groupID int64, content string, replyTo int64) int64 {
	// 模拟打字延迟
	if a.cfg.Chat.TypingSimulation {
		typingSpeed := a.cfg.Chat.TypingSpeed
		if typingSpeed <= 0 {
			typingSpeed = 6
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
	var msgID int64
	if replyTo > 0 {
		msgID, err = a.bot.SendGroupMessageReply(groupID, content, replyTo)
	} else {
		msgID, err = a.bot.SendGroupMessage(groupID, content)
	}

	if err != nil {
		zap.L().Error("发言失败", zap.Int64("group_id", groupID), zap.Error(err))
		return 0
	}

	a.recordSpeak(groupID)
	msg := &onebot.GroupMessage{
		MessageID:   msgID,
		GroupID:     groupID,
		UserID:      a.bot.GetSelfID(),
		Nickname:    a.persona.GetName(),
		Content:     content,
		Time:        time.Now(),
		MessageType: "group",
	}
	a.onMessage(msg)
	zap.L().Info("发言成功", zap.Int64("group_id", groupID), zap.String("content", content))
	return msgID
}

// autoSaveSticker 自动保存表情包（异步执行）
func (a *Agent) autoSaveSticker(url string, description string) {
	if url == "" {
		return
	}

	// 获取配置
	cfg := config.Get()
	storagePath := cfg.Sticker.StoragePath
	if storagePath == "" {
		storagePath = "./stickers"
	}
	maxSizeMB := cfg.Sticker.MaxSizeMB
	if maxSizeMB <= 0 {
		maxSizeMB = 1
	}

	// 下载图片
	result, err := fileutils.DownloadImage(url, storagePath, maxSizeMB)
	if err != nil {
		zap.L().Debug("下载表情包失败", zap.String("url", url), zap.Error(err))
		return
	}

	// 如果没有描述，使用默认描述
	if description == "" {
		description = "未描述的表情包"
	}

	// 保存到数据库
	sticker := &memory.Sticker{
		FileName:    result.FileName,
		OriginalURL: url,
		FileHash:    result.FileHash,
		Description: description,
	}

	isDuplicate, err := a.memory.SaveSticker(sticker)
	if err != nil {
		// 保存失败，删除已下载的文件
		_ = os.Remove(result.FilePath)
		zap.L().Warn("保存表情包失败", zap.Error(err))
		return
	}

	if isDuplicate {
		// 已存在，删除刚下载的文件
		_ = os.Remove(result.FilePath)
		zap.L().Debug("表情包已存在，跳过保存", zap.String("hash", result.FileHash))
		return
	}

	zap.L().Info("自动保存表情包", zap.Uint("id", sticker.ID), zap.String("desc", description))
}
