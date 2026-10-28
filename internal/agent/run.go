package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/tools"

	"github.com/bytedance/sonic"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// runAgent 跑一轮主模型，模型产生的消息由会话收集器统一写回
func (a *Agent) runAgent(ctx context.Context, agent *adk.TypedChatModelAgent[*schema.AgenticMessage], messages []*schema.AgenticMessage) error {
	if cfg := config.Get(); cfg != nil {
		extra := []callbacks.Handler(nil)
		if cfg.Debug.ShowToolCalls {
			extra = append(extra, tools.NewToolLogHandler())
		}
		ctx = llm.WithTask(ctx, "react", cfg.ModelTiers.High.Model, extra...)
	}
	runner := adk.NewTypedRunner[*schema.AgenticMessage](adk.TypedRunnerConfig[*schema.AgenticMessage]{Agent: agent})
	iter := runner.Run(ctx, messages)
	for {
		event, ok := iter.Next()
		if !ok {
			return nil
		}
		if event.Err != nil {
			return event.Err
		}
	}
}

// sessionInjector 把运行期间到达的新消息插到模型调用边界
type sessionInjector struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	agent *Agent
}

// BeforeModelRewriteState 在每次模型调用前补上本轮运行期间到达的消息
func (h *sessionInjector) BeforeModelRewriteState(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], mc *adk.TypedModelContext[*schema.AgenticMessage]) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	tc := tools.GetToolContext(ctx)
	if tc == nil || tc.Session == nil {
		return ctx, state, nil
	}
	pending := tc.Session.PendingAfter(max(tc.ThinkStartArrivalSeq, tc.ObservedThroughSeq))
	captured, captureErr := h.captureToolResults(ctx, state, tc)
	if captureErr != nil {
		return ctx, state, captureErr
	}
	tc.VisibleTranscriptIDs = append(tc.VisibleTranscriptIDs, captured...)
	if len(pending) == 0 {
		if err := h.tryCompact(ctx, state, tc); err != nil {
			zap.L().Warn("同步话题压缩失败，结束本轮", zap.Error(err))
			return ctx, state, err
		}
		tc.RequestTokenEstimate = sessionEstimate(state, tc)
		return ctx, state, nil
	}
	messages := make([]*schema.AgenticMessage, 0, len(pending))
	observed := tc.ThinkStartArrivalSeq
	for _, item := range pending {
		// 直接复用会话里渲染好的消息，内容进入上下文后不再变化
		messages = append(messages, item.Msg)
		if item.TranscriptID != 0 {
			tc.VisibleTranscriptIDs = append(tc.VisibleTranscriptIDs, item.TranscriptID)
		}
		observed = max(observed, item.ArrivalSeq)
	}
	state.Messages = append(state.Messages, messages...)
	tc.ObservedThroughSeq = max(tc.ObservedThroughSeq, observed)
	if observed > tc.InjectedArrivalSeq {
		tc.InjectedArrivalSeq = observed
	}
	if err := h.tryCompact(ctx, state, tc); err != nil {
		zap.L().Warn("同步话题压缩失败，结束本轮", zap.Error(err))
		return ctx, state, err
	}
	tc.RequestTokenEstimate = sessionEstimate(state, tc)
	return ctx, state, nil
}

func (h *sessionInjector) tryCompact(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], tc *tools.ToolContext) error {
	if h.agent == nil || tc == nil || tc.Session == nil {
		return nil
	}
	cfg := config.Get()
	if cfg == nil || cfg.ModelTiers.High.ContextWindow <= 0 {
		return nil
	}
	session, ok := tc.Session.(*Session)
	if !ok || session == nil {
		return nil
	}
	window := cfg.ModelTiers.High.ContextWindow
	estimated := session.EstimateRequestTokens(state.Messages, state.ToolInfos)
	if estimated < int(float64(window)*cfg.Agent.Compaction.TriggerRatio) {
		return nil
	}
	if tc.CompactionAttempts >= 2 {
		return fmt.Errorf("上下文仍超过触发水位，但本轮压缩尝试已用尽")
	}
	if session.CompactionCoolingDown(time.Now()) {
		return fmt.Errorf("上下文仍超过触发水位，压缩正在退避")
	}
	return h.compactNow(ctx, session, state, tc)
}

func (h *sessionInjector) captureToolResults(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], tc *tools.ToolContext) ([]uint, error) {
	session, ok := tc.Session.(*Session)
	if !ok || session == nil || state == nil {
		return nil, nil
	}
	return session.CaptureStateToolResults(ctx, tc.RunID, state.Messages)
}

func (h *sessionInjector) compactNow(ctx context.Context, session *Session, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], tc *tools.ToolContext) error {
	cfg := config.Get()
	window := cfg.ModelTiers.High.ContextWindow
	candidate, err := session.BuildCompactionCandidate(window, cfg.Agent.Compaction.RecentTailRatio)
	if err != nil {
		session.MarkCompactionFailure(time.Now())
		return err
	}
	timeout := time.Duration(cfg.Agent.Compaction.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	compactionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	stopShutdownWatch := context.AfterFunc(h.agent.ctx, cancel)
	defer stopShutdownWatch()
	compactionCtx = withModelTimeout(compactionCtx, timeout)
	remainingSteps := cfg.Agent.Compaction.MaxStep
	if remainingSteps <= 0 {
		remainingSteps = 12
	}
	var lastErr error
	feedback := ""
	for tc.CompactionAttempts < 2 {
		tc.CompactionAttempts++
		lastErr = h.agent.runCompaction(compactionCtx, session, candidate, state, tc.CompactionAttempts, &remainingSteps, feedback)
		if lastErr == nil {
			system := make([]*schema.AgenticMessage, 0, 1)
			if len(state.Messages) > 0 && state.Messages[0] != nil && state.Messages[0].Role == schema.AgenticRoleTypeSystem {
				system = append(system, state.Messages[0])
			}
			rebuilt, buildErr := session.Assemble(compactionCtx, h.agent.memory)
			if buildErr != nil {
				lastErr = buildErr
				break
			}
			state.Messages = append(system, rebuilt...)
			tc.RequestTokenEstimate = session.EstimateRequestTokens(state.Messages, state.ToolInfos)
			return nil
		}
		var inputErr *compactionInputError
		if errors.As(lastErr, &inputErr) {
			feedback = "上一次压缩提交参数无效，请修正后重新提交：" + inputErr.Error()
			if remainingSteps <= 0 {
				break
			}
			continue
		}
		var tooLarge *compactionTooLargeError
		if !errors.As(lastErr, &tooLarge) {
			break
		}
		if tooLarge.Previous != nil {
			feedback = fmt.Sprintf("上一次提交预计 %d token，超过 %d token 上限。请进一步压缩概览和 gist、合并重复话题；上一版概览可参考：%s", tooLarge.Tokens, tooLarge.Maximum, truncateRunes(tooLarge.Previous.Overview, 1200))
		}
		if remainingSteps <= 0 {
			break
		}
	}
	session.MarkCompactionFailure(time.Now())
	return lastErr
}

// compactAfterOverflow 在 provider 明确报告上下文溢出时强制尝试一次安全压缩
func (a *Agent) compactAfterOverflow(ctx context.Context, kind string, targetID int64, toolsList []tool.BaseTool, genInput adk.TypedGenModelInput[*schema.AgenticMessage], session *Session) bool {
	tc := &tools.ToolContext{}
	retryCtx := a.buildConversationToolContext(ctx, kind, targetID, session)
	tc = tools.GetToolContext(retryCtx)
	pending := session.PendingMessages()
	tc.ThinkStartArrivalSeq = maxPendingSeq(pending)
	assembled, err := session.Assemble(retryCtx, a.memory)
	if err != nil {
		return false
	}
	inputMessages, err := genInput(retryCtx, "", &adk.TypedAgentInput[*schema.AgenticMessage]{Messages: assembled, EnableStreaming: false})
	if err != nil {
		return false
	}
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{Messages: inputMessages, ToolInfos: modelToolInfos(retryCtx, toolsList)}
	h := &sessionInjector{TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{}, agent: a}
	if err := h.compactNow(retryCtx, session, state, tc); err != nil {
		zap.L().Warn("上下文溢出后的强制压缩失败", zap.String("conversation_kind", kind), zap.Int64("target_id", targetID), zap.Error(err))
		return false
	}
	return true
}

func modelToolInfos(ctx context.Context, toolsList []tool.BaseTool) []*schema.ToolInfo {
	infos := make([]*schema.ToolInfo, 0, len(toolsList))
	for _, item := range toolsList {
		if item == nil {
			continue
		}
		info, err := item.Info(ctx)
		if err == nil && info != nil {
			infos = append(infos, info)
		}
	}
	return infos
}

func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{"context length", "context_length_exceeded", "maximum context", "too many tokens", "input is too long", "prompt is too long", "token limit exceeded"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func sessionEstimate(state *adk.TypedChatModelAgentState[*schema.AgenticMessage], tc *tools.ToolContext) int {
	if state == nil || tc == nil {
		return 0
	}
	session, ok := tc.Session.(*Session)
	if !ok || session == nil {
		return requestTokens(state.Messages, state.ToolInfos)
	}
	return session.EstimateRequestTokens(state.Messages, state.ToolInfos)
}

// sessionPersister 在每次模型成功后保存 assistant 并把本批可见输入标记为 visible
type sessionPersister struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
}

func (h *sessionPersister) AfterModelRewriteState(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], _ *adk.TypedModelContext[*schema.AgenticMessage]) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	tc := tools.GetToolContext(ctx)
	if tc == nil || tc.Session == nil {
		return ctx, state, nil
	}
	session, ok := tc.Session.(*Session)
	if !ok || session == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}
	last := state.Messages[len(state.Messages)-1]
	if last == nil || last.Role != schema.AgenticRoleTypeAssistant {
		return ctx, state, nil
	}
	if last.ResponseMeta != nil && last.ResponseMeta.TokenUsage != nil {
		session.ObserveTokenUsage(tc.RequestTokenEstimate, last.ResponseMeta.TokenUsage.PromptTokens)
	}
	entry, err := session.AppendAssistant(ctx, tc.RunID, last)
	if err != nil {
		return ctx, state, err
	}
	ids := append([]uint(nil), tc.VisibleTranscriptIDs...)
	if entry != nil {
		ids = append(ids, entry.ID)
	}
	seqs, err := session.memory.MarkVisible(ctx, ids)
	if err != nil {
		return ctx, state, err
	}
	session.ApplyVisibleSeqs(seqs)
	session.MarkInjected(tc.InjectedArrivalSeq)
	tc.VisibleTranscriptIDs = nil
	tc.InjectedArrivalSeq = 0
	return ctx, state, nil
}

// appendSessionMessage 把落库后的消息放进会话
func (a *Agent) appendSessionMessage(ctx context.Context, msg *onebot.ConversationMessage, mediaRefs []media.Ref, log *memory.MessageLog) {
	session := a.sessions.get(msg.ConversationKind, msg.TargetID)
	logID := uint(0)
	if log != nil {
		logID = log.ID
	}
	if msg.MessageID != 0 && msg.UserID == a.bot.GetSelfID() {
		// 自身发言已经出现在模型的工具轨迹里，不再额外生成 user transcript
		return
	}
	ref := ""
	if log != nil {
		ref = log.MessageRef
	}
	user := session.AppendUser(msg, logID, mediaRefs, ref)
	if user != nil {
		payload, err := sonic.Marshal(user.Msg)
		if err == nil {
			if entry, err := a.memory.AppendUserTranscript(ctx, msg.ConversationKind, msg.TargetID, logID, payload, ""); err != nil {
				zap.L().Warn("保存输入 transcript 失败", zap.Error(err))
			} else if entry != nil {
				session.SetUserTranscriptEntry(user, entry)
			}
		}
	}
}

// markCollectedStickers 标记哪些表情已经在收藏库，消息里据此显示已收藏
func (a *Agent) markCollectedStickers(ctx context.Context, mediaRefs []media.Ref) {
	hashes := make([]string, 0, len(mediaRefs))
	for _, ref := range mediaRefs {
		if ref.Sticker && ref.Hash != "" {
			hashes = append(hashes, ref.Hash)
		}
	}
	if len(hashes) == 0 {
		return
	}
	collected, err := a.memory.WithContext(ctx).CollectedStickerHashes(hashes)
	if err != nil {
		zap.L().Warn("查询表情收藏状态失败", zap.Error(err))
		return
	}
	for index := range mediaRefs {
		if collected[mediaRefs[index].Hash] {
			mediaRefs[index].Collected = true
		}
	}
}

// markSessionRecalled 标记会话里的消息已撤回，上下文保留原文
func (a *Agent) markSessionRecalled(kind string, targetID, messageID int64) {
	a.sessions.get(kind, targetID).MarkRecalledByMessageID(messageID)
}

// groupGenModelInput 生成群聊模型输入：系统指令在前，会话消息在后
func (a *Agent) groupGenModelInput(ctx context.Context, _ string, input *adk.TypedAgentInput[*schema.AgenticMessage]) ([]*schema.AgenticMessage, error) {
	groupExtra := ""
	if tc := tools.GetToolContext(ctx); tc != nil {
		extra, err := a.memory.WithContext(ctx).GetGroupExtraPrompt(ctx, tc.TargetID)
		if err != nil {
			zap.L().Warn("读取群额外提示词失败", zap.Int64("target_id", tc.TargetID), zap.Error(err))
		} else {
			groupExtra = extra
		}
	}
	messages := make([]*schema.AgenticMessage, 0, len(input.Messages)+1)
	messages = append(messages, schema.SystemAgenticMessage(a.persona.GetGroupInstruction(groupExtra)))
	messages = append(messages, input.Messages...)
	return messages, nil
}

// privateGenModelInput 生成私聊模型输入：系统指令在前，会话消息在后
func (a *Agent) privateGenModelInput(ctx context.Context, _ string, input *adk.TypedAgentInput[*schema.AgenticMessage]) ([]*schema.AgenticMessage, error) {
	messages := make([]*schema.AgenticMessage, 0, len(input.Messages)+1)
	messages = append(messages, schema.SystemAgenticMessage(a.persona.GetPrivateInstruction()))
	messages = append(messages, input.Messages...)
	return messages, nil
}

func (a *Agent) retryAfterOverflow(ctx context.Context, kind string, targetID int64, chatAgent *adk.TypedChatModelAgent[*schema.AgenticMessage], session *Session) error {
	retryCtx := a.buildConversationToolContext(context.WithoutCancel(ctx), kind, targetID, session)
	tc := tools.GetToolContext(retryCtx)
	pending := session.PendingMessages()
	tc.ThinkStartArrivalSeq = maxPendingSeq(pending)
	messages, err := session.Assemble(retryCtx, a.memory)
	if err != nil {
		return err
	}
	return a.runAgent(retryCtx, chatAgent, messages)
}
