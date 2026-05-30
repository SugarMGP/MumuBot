package learning

import (
	"context"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/jargon"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/tools"
	"mumu-bot/internal/topic"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	agentflow "github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

type Learner struct {
	memMgr             *memory.Manager
	jargonMgr          *jargon.Manager
	knowledgeAgent     *react.Agent
	memberProfileAgent *react.Agent
	ctx                context.Context
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	isRunning          bool
	mu                 sync.Mutex
}

func New(memMgr *memory.Manager, jargonMgr *jargon.Manager) (*Learner, error) {
	midTierModel, err := llm.NewClientForTier(llm.TierMid)
	if err != nil {
		return nil, err
	}

	var knowledgeTools []tool.BaseTool
	knowledgeToolBuilders := []func() (tool.BaseTool, error){
		func() (tool.BaseTool, error) { return tools.NewSaveJargonTool() },
		func() (tool.BaseTool, error) { return tools.NewSaveStyleCardTool() },
		func() (tool.BaseTool, error) { return tools.NewGetUncheckedStyleCardsTool() },
		func() (tool.BaseTool, error) { return tools.NewReviewStyleCardTool() },
		func() (tool.BaseTool, error) { return tools.NewGetUncheckedJargonsTool() },
		func() (tool.BaseTool, error) { return tools.NewReviewJargonTool() },
	}
	for _, build := range knowledgeToolBuilders {
		t, err := build()
		if err != nil {
			return nil, err
		}
		knowledgeTools = append(knowledgeTools, t)
	}

	cfg := config.Get()
	maxStep := cfg.Learning.MaxStep
	if maxStep <= 0 {
		maxStep = 10
	}

	knowledgeAgent, err := newLearnerAgent(midTierModel, knowledgeTools, maxStep)
	if err != nil {
		return nil, fmt.Errorf("创建学习 Agent 失败: %w", err)
	}

	memberProfileTool, err := tools.NewUpdateMemberProfileTool()
	if err != nil {
		return nil, err
	}

	memberProfileAgent, err := newLearnerAgent(midTierModel, []tool.BaseTool{memberProfileTool}, maxStep)
	if err != nil {
		return nil, fmt.Errorf("创建成员画像学习 Agent 失败: %w", err)
	}

	l := &Learner{
		memMgr:             memMgr,
		jargonMgr:          jargonMgr,
		knowledgeAgent:     knowledgeAgent,
		memberProfileAgent: memberProfileAgent,
	}

	return l, nil
}

func newLearnerAgent(model model.ToolCallingChatModel, toolset []tool.BaseTool, maxStep int) (*react.Agent, error) {
	return react.NewAgent(context.Background(), &react.AgentConfig{
		ToolCallingModel: model,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: toolset,
			ToolArgumentsHandler: func(ctx context.Context, name, arguments string) (string, error) {
				return tools.CanonicalizeToolArguments(arguments)
			},
			ToolCallMiddlewares: []compose.ToolMiddleware{{
				Invokable: tools.ToolDedupMiddleware(),
			}},
		},
		MaxStep: maxStep,
	})
}

func (l *Learner) Start(parent context.Context) {
	l.mu.Lock()
	if l.isRunning {
		l.mu.Unlock()
		return
	}

	if parent == nil {
		parent = context.Background()
	}

	l.ctx, l.cancel = context.WithCancel(parent)
	l.isRunning = true
	l.mu.Unlock()

	l.wg.Add(1)
	go l.runLoop()
	zap.L().Info("后台学习系统已启动")
}

func (l *Learner) Stop() {
	l.mu.Lock()
	if !l.isRunning {
		l.mu.Unlock()
		return
	}

	cancel := l.cancel
	l.isRunning = false
	l.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	l.wg.Wait()
	zap.L().Info("后台学习系统已停止")
}

func (l *Learner) runLoop() {
	defer l.wg.Done()
	cfg := config.Get()
	// Check interval
	intervalMinutes := cfg.Learning.IntervalMinutes
	if intervalMinutes <= 0 {
		intervalMinutes = 10
	}
	interval := time.Duration(intervalMinutes) * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	l.runTask()

	// 启动定期审核任务
	reviewIntervalMinutes := cfg.Learning.ReviewIntervalMinutes
	if reviewIntervalMinutes <= 0 {
		reviewIntervalMinutes = 30
	}
	reviewTicker := time.NewTicker(time.Duration(reviewIntervalMinutes) * time.Minute)
	defer reviewTicker.Stop()

	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
			l.runTask()
		case <-reviewTicker.C:
			l.runReviewTask()
		}
	}
}

func (l *Learner) runTask() {
	cfg := config.Get()
	for _, group := range cfg.Groups {
		if !group.Enabled {
			continue
		}
		if err := l.ctx.Err(); err != nil {
			return
		}
		l.processGroup(group.GroupID)
	}
}

func (l *Learner) runReviewTask() {
	cfg := config.Get()
	for _, group := range cfg.Groups {
		if !group.Enabled {
			continue
		}
		if err := l.ctx.Err(); err != nil {
			return
		}
		l.processReview(group.GroupID)
	}
}

func (l *Learner) processReview(groupID int64) {
	prompt := `请检查当前待审核的“黑话/梗”和“群聊风格卡片”。
你需要使用 'getUncheckedJargons' 和 'getUncheckedStyleCards' 工具来获取待审核列表。
然后，根据你的知识库判断这些内容的准确性和健康度。
- 如果内容准确且无害，使用 'reviewJargon' 或 'reviewStyleCard' 通过审核 (approve=true)。
- 如果内容明显错误、垃圾信息或有害，请拒绝 (approve=false)。
- 如果你不确定，请保持待审核状态（不做操作）。

审核风格卡片时重点看：
1. 这是不是群里可复用的说话风格，而不是具体事件内容；
2. trigger_rule 和 avoid_rule 是否明确；
3. 是否容易误伤、过度攻击或强烈阴阳；
4. 例句是否短、自然、像参考味道而不是模板。
5. 如果内容来自机器人的发言、具体人名事件、一次性上下文，或 source_excerpt 不能提供短证据，请拒绝。
6. 普通技术词、通用缩写、无需上下文也能理解的词不应作为黑话通过。

注意：审核工具支持批量操作，请将同一审核结果的 ID 放入列表中一次性提交，尽量减少工具调用次数。`

	// 创建学习上下文
	ctx, cancel := context.WithTimeout(l.ctx, 60*time.Second)
	defer cancel()
	ctx = tools.WithLearningContext(ctx, &tools.LearningContext{
		GroupID:   groupID,
		MemMgr:    l.memMgr,
		JargonMgr: l.jargonMgr,
	})

	// 调用 Agent
	opts := []agentflow.AgentOption{}
	if cfg := config.Get(); cfg != nil && cfg.Debug.ShowToolCalls {
		opts = append(opts, agentflow.WithComposeOptions(compose.WithCallbacks(tools.NewToolLogHandler())))
	}
	_, err := l.knowledgeAgent.Generate(ctx, []*schema.Message{
		schema.UserMessage(prompt),
	}, opts...)
	if err != nil {
		zap.L().Error("后台审核任务失败", zap.Int64("group_id", groupID), zap.Error(err))
		return
	}

	zap.L().Info("后台审核任务完成", zap.Int64("group_id", groupID))
}

func (l *Learner) processGroup(groupID int64) {
	// 获取上次学习进度
	state, err := l.memMgr.GetLearningState(groupID)
	if err != nil {
		zap.L().Error("获取学习进度失败", zap.Int64("group_id", groupID), zap.Error(err))
		return
	}

	// 只读取 learner 连续可消费的“已处理前缀”
	cfg := config.Get()
	batchSize := cfg.Learning.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	msgs, err := l.memMgr.GetProcessableLearningMessages(groupID, cfg.Persona.QQ, state.LastMessageID, batchSize)
	if err != nil {
		zap.L().Error("获取消息失败", zap.Int64("group_id", groupID), zap.Error(err))
		return
	}

	if len(msgs) == 0 {
		return
	}

	learnableMsgs, newLastID, firstLearnableIndex := selectLearnableMessages(msgs)

	if len(learnableMsgs) == 0 {
		if err := l.memMgr.UpdateLearningState(groupID, newLastID); err != nil {
			zap.L().Error("更新学习进度失败", zap.Int64("group_id", groupID), zap.Error(err))
		}
		return
	}

	minMsgCount := cfg.Learning.MinMsgCount
	if minMsgCount <= 0 {
		minMsgCount = 5
	}
	if len(learnableMsgs) < minMsgCount {
		if firstLearnableIndex > 0 {
			if err := l.memMgr.UpdateLearningState(groupID, msgs[firstLearnableIndex-1].ID); err != nil {
				zap.L().Error("更新学习进度失败", zap.Int64("group_id", groupID), zap.Error(err))
			}
		}
		return
	}

	knowledgeErr := l.processKnowledgeExtraction(groupID, learnableMsgs)
	memberErr := l.processMemberProfileExtraction(groupID, learnableMsgs)
	if knowledgeErr != nil || memberErr != nil {
		if knowledgeErr != nil {
			zap.L().Error("后台学习任务失败", zap.Int64("group_id", groupID), zap.Error(knowledgeErr))
		}
		if memberErr != nil {
			zap.L().Error("成员画像学习任务失败", zap.Int64("group_id", groupID), zap.Error(memberErr))
		}
		return
	}

	zap.L().Info("后台学习任务完成", zap.Int64("group_id", groupID))

	// 更新进度
	if err := l.memMgr.UpdateLearningState(groupID, newLastID); err != nil {
		zap.L().Error("更新学习进度失败", zap.Int64("group_id", groupID), zap.Error(err))
	}
}

func (l *Learner) processKnowledgeExtraction(groupID int64, msgs []memory.MessageLog) error {
	// 构建提示词
	var chatLog strings.Builder
	for _, m := range msgs {
		if m.OriginalContent == "" {
			continue
		}
		chatLog.WriteString(fmt.Sprintf("%s: %s\n", m.Nickname, m.OriginalContent))
	}

	// 注入已知的黑话/梗（避免重复学习）
	knownJargons := ""
	if l.jargonMgr != nil {
		matches := l.jargonMgr.Match(chatLog.String())
		if len(matches) > 0 {
			var b strings.Builder
			b.WriteString("\n【已知黑话】：\n")
			for term, meaning := range matches {
				b.WriteString(fmt.Sprintf("- %s: %s\n", term, meaning))
			}
			knownJargons = b.String()
		}
	}

	prompt := fmt.Sprintf(`请分析以下 QQ 群聊天记录。你的任务是提取“黑话/梗”（该群体特有的术语、缩写、meme）和“群聊风格卡片”（这个群在特定场景下常见的说话方式）。

聊天记录：

%s

%s

要求：
1. 识别**新的**黑话/梗。黑话必须依赖上下文才能理解；普通技术词、通用缩写、无需上下文也能理解的词不要保存。
2. 证据不足时不保存黑话，不要猜测含义。
3. 识别可复用的群聊风格卡片，而不是一次性内容。
4. 风格卡片写成可复用的情境和表达方式，不写具体人名、具体事件、一次性上下文。
5. 风格卡片必须使用以下 intent 枚举之一：%s。
6. 风格卡片必须使用以下 tone 枚举之一：%s。
7. 每张风格卡片必须包含：intent、tone、trigger_rule、avoid_rule、example、source_excerpt。
8. source_excerpt 只保留能证明判断的短证据，不要整段复制聊天。
9. example 必须是短句，只作为语气味道参考，不能写成长模板。
10. 如果无法给出明确的 trigger_rule 和 avoid_rule，就不要保存该卡片。
11. 强攻击性、强冒犯性、强阴阳且容易误伤的表达不要保存为风格卡片。
12. 忽略通用语言或普通词汇，专注于独特的群体文化。
13. 使用提供的工具 'saveJargon' 和 'saveStyleCard' 来保存你的发现。
14. 如果没有发现有价值的内容，请直接回复“无新发现”。
`, chatLog.String(), knownJargons, strings.Join(memory.StyleIntentValues(), "、"), strings.Join(memory.StyleToneValues(), "、"))

	// 创建学习上下文
	ctx, cancel := context.WithTimeout(l.ctx, 90*time.Second)
	defer cancel()
	ctx = tools.WithLearningContext(ctx, &tools.LearningContext{
		GroupID:   groupID,
		MemMgr:    l.memMgr,
		JargonMgr: l.jargonMgr,
	})

	// 调用 Agent
	opts := []agentflow.AgentOption{}
	if cfg := config.Get(); cfg != nil && cfg.Debug.ShowToolCalls {
		opts = append(opts, agentflow.WithComposeOptions(compose.WithCallbacks(tools.NewToolLogHandler())))
	}
	_, err := l.knowledgeAgent.Generate(ctx, []*schema.Message{
		schema.UserMessage(prompt),
	}, opts...)
	if err != nil {
		return err
	}

	return nil
}

func (l *Learner) processMemberProfileExtraction(groupID int64, msgs []memory.MessageLog) error {
	summaries := l.buildMemberProfileSummaries(groupID, msgs)
	if len(summaries) == 0 {
		return nil
	}

	prompt := fmt.Sprintf(`请根据下面这些群友在本批消息里的发言证据，提炼成员画像。
你只能在证据足够时调用 'updateMemberProfile' 工具，不要输出普通文本。

要求：
1. 只处理发言证据明确、样本足够的群友；没有把握就跳过。
2. speak_style 用一句中文概括该人的说话习惯。
3. interests 和 common_words 都要尽量精炼，通常各给 0-5 项即可。
4. aliases 只记录稳定、反复出现、证据足够的别称，不要记录一次性的玩笑叫法。
5. interests 和 common_words 会整体覆盖旧列表，所以不要把不确定的项塞进去。
6. 不要修改 intimacy_delta，保持默认值。
7. 如果某个字段没有把握，就不要传该字段。
8. 如果没有适合更新的人，直接回复“无新发现”。

候选成员：

%s`, strings.Join(summaries, "\n\n"))

	ctx, cancel := context.WithTimeout(l.ctx, 90*time.Second)
	defer cancel()
	ctx = tools.WithToolContext(ctx, &tools.ToolContext{
		GroupID:   groupID,
		MemoryMgr: l.memMgr,
	})

	opts := []agentflow.AgentOption{}
	if cfg := config.Get(); cfg != nil && cfg.Debug.ShowToolCalls {
		opts = append(opts, agentflow.WithComposeOptions(compose.WithCallbacks(tools.NewToolLogHandler())))
	}
	_, err := l.memberProfileAgent.Generate(ctx, []*schema.Message{
		schema.UserMessage(prompt),
	}, opts...)
	return err
}

func (l *Learner) buildMemberProfileSummaries(groupID int64, msgs []memory.MessageLog) []string {
	const minEvidenceMessages = 3
	const maxSamplesPerMember = 6

	type memberEvidence struct {
		userID   int64
		nickname string
		messages []string
		msgCount int
	}

	evidenceByUser := make(map[int64]*memberEvidence)
	order := make([]int64, 0)

	for _, msg := range msgs {
		if msg.UserID == 0 {
			continue
		}
		content := strings.TrimSpace(msg.OriginalContent)
		if content == "" {
			continue
		}

		evidence, ok := evidenceByUser[msg.UserID]
		if !ok {
			evidence = &memberEvidence{
				userID:   msg.UserID,
				nickname: msg.Nickname,
				messages: make([]string, 0, maxSamplesPerMember),
			}
			evidenceByUser[msg.UserID] = evidence
			order = append(order, msg.UserID)
		}
		evidence.msgCount++
		if evidence.nickname == "" {
			evidence.nickname = msg.Nickname
		}
		if len(evidence.messages) < maxSamplesPerMember {
			evidence.messages = append(evidence.messages, content)
		}
	}

	summaries := make([]string, 0, len(order))
	for _, userID := range order {
		evidence := evidenceByUser[userID]
		if evidence == nil || evidence.msgCount < minEvidenceMessages || len(evidence.messages) < minEvidenceMessages {
			continue
		}

		summary := &strings.Builder{}
		fmt.Fprintf(summary, "用户 %d（%s）最近发言 %d 条。", evidence.userID, evidence.nickname, evidence.msgCount)
		if profile, err := l.memMgr.GetMemberProfile(userID); err == nil {
			var profileNotes []string
			if card := memory.LatestMemberGroupCard(profile.MemberNameRecords(), groupID); strings.TrimSpace(card) != "" {
				profileNotes = append(profileNotes, "当前群历史群名片："+card)
			}
			if aliases := memory.MemberLearnedAliases(profile.MemberNameRecords()); len(aliases) > 0 {
				profileNotes = append(profileNotes, "已学别称："+strings.Join(aliases, "、"))
			}
			if strings.TrimSpace(profile.SpeakStyle) != "" {
				profileNotes = append(profileNotes, "当前说话风格："+strings.TrimSpace(profile.SpeakStyle))
			}
			if interests := decodeProfileList(profile.Interests); len(interests) > 0 {
				profileNotes = append(profileNotes, "当前兴趣："+strings.Join(interests, "、"))
			}
			if commonWords := decodeProfileList(profile.CommonWords); len(commonWords) > 0 {
				profileNotes = append(profileNotes, "当前常用词："+strings.Join(commonWords, "、"))
			}
			if len(profileNotes) > 0 {
				fmt.Fprintf(summary, "\n已有画像：%s。", strings.Join(profileNotes, "；"))
			}
		}
		summary.WriteString("\n发言样本：")
		for idx, content := range evidence.messages {
			fmt.Fprintf(summary, "\n%d. %s", idx+1, content)
		}
		summaries = append(summaries, summary.String())
	}

	return summaries
}

func selectLearnableMessages(msgs []memory.MessageLog) ([]memory.MessageLog, uint, int) {
	if len(msgs) == 0 {
		return nil, 0, -1
	}
	learnable := make([]memory.MessageLog, 0, len(msgs))
	lastID := msgs[len(msgs)-1].ID
	firstLearnableIndex := -1
	for idx, msg := range msgs {
		if topic.IsAssignmentProcessed(msg) {
			if firstLearnableIndex < 0 {
				firstLearnableIndex = idx
			}
			learnable = append(learnable, msg)
		}
	}
	return learnable, lastID, firstLearnableIndex
}

func decodeProfileList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var items []string
	if err := sonic.UnmarshalString(raw, &items); err == nil && len(items) > 0 {
		return items
	}
	return []string{raw}
}
