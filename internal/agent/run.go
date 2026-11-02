package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

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
	cfg := configFor(ctx)
	generation := generationFrom(ctx)
	extra := []callbacks.Handler(nil)
	if cfg.Debug.ShowToolCalls {
		extra = append(extra, tools.NewToolLogHandler())
	}
	modelName := ""
	if generation != nil {
		modelName = generation.modelName
	}
	ctx = llm.WithTask(ctx, "react", modelName, extra...)
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
	visible := map[uint]bool{}
	for _, id := range tc.VisibleTranscriptIDs {
		if id != 0 {
			visible[id] = true
		}
	}
	for _, item := range pending {
		if item.TranscriptID != 0 {
			visible[item.TranscriptID] = true
		}
	}
	for _, item := range tc.Session.PendingAgentMessages() {
		if item.TranscriptID == 0 || !visible[item.TranscriptID] {
			pending = append(pending, item)
			if item.TranscriptID != 0 {
				visible[item.TranscriptID] = true
			}
		}
	}
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].Order < pending[j].Order })
	captured, captureErr := h.captureToolResults(ctx, state, tc)
	if captureErr != nil {
		return ctx, state, captureErr
	}
	tc.VisibleTranscriptIDs = append(tc.VisibleTranscriptIDs, captured...)
	if len(pending) == 0 {
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
	return ctx, state, nil
}

func (h *sessionInjector) captureToolResults(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], tc *tools.ToolContext) ([]uint, error) {
	session, ok := tc.Session.(*Session)
	if !ok || session == nil || state == nil {
		return nil, nil
	}
	return session.CaptureStateToolResults(ctx, tc.RunID, state.Messages)
}

func (h *sessionInjector) compactNow(ctx context.Context, session *Session, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], tc *tools.ToolContext) error {
	cfg := configFor(ctx)
	generation := generationFrom(ctx)
	if generation == nil {
		return fmt.Errorf("模型运行态未初始化")
	}
	window := generation.contextWindow
	candidate, err := session.BuildCompactionCandidate(window, cfg.Compactor.RecentTailRatio)
	if err != nil {
		session.MarkCompactionFailure(time.Now())
		return err
	}
	compactionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	stopShutdownWatch := context.AfterFunc(h.agent.ctx, cancel)
	defer stopShutdownWatch()
	remainingSteps := cfg.Compactor.MaxSteps
	if remainingSteps <= 0 {
		remainingSteps = 12
	}
	var lastErr error
	feedback := ""
	for tc.CompactionAttempts < 2 {
		tc.CompactionAttempts++
		lastErr = h.agent.runCompaction(compactionCtx, session, candidate, tc.CompactionAttempts, &remainingSteps, feedback)
		if lastErr == nil {
			rebuilt, buildErr := h.agent.assembleMessages(compactionCtx, session)
			if buildErr != nil {
				lastErr = buildErr
				break
			}
			state.Messages = rebuilt
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
	assembled, err := a.assembleMessages(retryCtx, session)
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
	if last.ResponseMeta != nil && last.ResponseMeta.TokenUsage != nil && last.ResponseMeta.TokenUsage.TotalTokens > 0 {
		requestMessages := state.Messages[:len(state.Messages)-1]
		session.ObserveResponseUsage(requestMessages, state.ToolInfos, last.ResponseMeta.TokenUsage)
		session.SetTokenBase(last, last.ResponseMeta.TokenUsage)
	} else {
		session.ClearTokenBase()
	}
	entry, err := session.AppendAssistant(ctx, tc.RunID, last)
	if err != nil {
		return ctx, state, err
	}
	ids := append([]uint(nil), tc.VisibleTranscriptIDs...)
	if entry != nil {
		ids = append(ids, entry.ID)
	}
	seqs, err := session.memory.MarkVisible(ctx, session.kind, session.targetID, ids)
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
	user := session.AppendUser(msg, logID, mediaRefs)
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

// passthroughModelInput 原样返回装配结果，人格与群说明已作为 transcript 行写入
func (a *Agent) passthroughModelInput(ctx context.Context, _ string, input *adk.TypedAgentInput[*schema.AgenticMessage]) ([]*schema.AgenticMessage, error) {
	return input.Messages, nil
}

func (a *Agent) retryAfterOverflow(ctx context.Context, kind string, targetID int64, chatAgent *adk.TypedChatModelAgent[*schema.AgenticMessage], session *Session) error {
	retryCtx := a.buildConversationToolContext(context.WithoutCancel(ctx), kind, targetID, session)
	tc := tools.GetToolContext(retryCtx)
	pending := session.PendingMessages()
	tc.ThinkStartArrivalSeq = maxPendingSeq(pending)
	messages, err := a.assembleMessages(retryCtx, session)
	if err != nil {
		return err
	}
	return a.runAgent(retryCtx, chatAgent, messages)
}
