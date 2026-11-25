package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/agentic"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/logger"
	"mumu-bot/internal/media"
	"mumu-bot/internal/tools"

	"go.uber.org/zap"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/noop"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// errCompactionPending 表示压缩失败或仍在退避，本轮不能继续请求主模型
var errCompactionPending = errors.New("压缩失败或仍在退避")

// runAgent 跑一轮主模型；模型产生的消息由回调统一写回
func (a *Agent) runAgent(ctx context.Context, ag agent.Agent, toolsList []tool.Tool, messages []*agentic.Message) (runErr error) {
	cfg := configFor(ctx)
	generation := generationFrom(ctx)
	if generation == nil {
		return fmt.Errorf("模型运行态未初始化")
	}
	role := generation.forKind("chat")
	task := "react"
	if tc := tools.GetToolContext(ctx); tc != nil {
		role = generation.forKind(tc.ConversationKind)
		task = taskForKind(tc.ConversationKind)
		if session, ok := tc.Session.(*Session); ok && llm.TrackerFrom(ctx) == nil {
			ctx = llm.WithRequestTracker(ctx, session.trackModelRequest)
			defer session.finishModelRequest(nil)
		}
		tc.ToolDeclarations = toolDeclarations(toolsList)
		ctx = logger.WithSession(ctx, tc.ConversationKind, tc.TargetID, tc.RunID, cfg.Debug.LogAgentContent)
	}
	if existing := modelTaskFrom(ctx); existing.task != "" {
		task = existing.task
	}
	observer := &llm.ResponseObserver{}
	ctx = llm.WithResponseObserver(ctx, observer)
	if modelTaskFrom(ctx).modelName == "" {
		ctx = withModelTask(ctx, task, role.modelName)
	}
	log := logger.FromContext(ctx).With(zap.String("task", task))
	started := time.Now()
	log.Info("Agent 运行开始")
	defer func() {
		if runErr != nil {
			log.Error("Agent 运行失败", zap.Duration("duration", time.Since(started)), zap.Error(runErr))
		} else {
			log.Info("Agent 运行完成", zap.Duration("duration", time.Since(started)))
		}
	}()

	seed := llm.ToModelMessages(role.provider, role.modelName, messages)
	runID := "run"
	if tc := tools.GetToolContext(ctx); tc != nil && tc.RunID != "" {
		runID = tc.RunID
	}
	r := runner.NewRunner("mumu", ag, runner.WithSessionService(noop.NewService()))
	defer r.Close()
	events, err := runner.RunWithMessages(ctx, r, "mumu", runID, seed)
	if err != nil {
		return err
	}
	defer func() {
		go func() {
			for range events {
			}
		}()
	}()
	for ev := range events {
		if ev == nil {
			continue
		}
		if ev.IsRunnerCompletion() {
			break
		}
		if !ev.IsError() {
			continue
		}
		if err := a.mapRunError(ctx, ev); err != nil {
			return err
		}
		return nil
	}
	return nil
}

func (a *Agent) mapRunError(ctx context.Context, ev *event.Event) error {
	if tc := tools.GetToolContext(ctx); tc != nil && tc.StopReason != "" {
		switch tc.StopReason {
		case tools.StopReasonSleep:
			return nil
		case tools.StopReasonBudget:
			return fmt.Errorf("本轮步数用完: %w", errBudgetExhausted)
		}
		if tc.StopCause != nil {
			return tc.StopCause
		}
	}
	if err := llm.TrackerError(ctx); err != nil {
		return err
	}
	if ev.Error != nil && ev.Error.Code != nil {
		switch *ev.Error.Code {
		case llm.ErrorCodeIdle:
			return errors.Join(errModelRequestIdle, &modelRequestError{errors.New(ev.Error.Message)})
		case llm.ErrorCodeModel:
			return &modelRequestError{errors.New(ev.Error.Message)}
		}
	}
	// 适配器把服务商错误标成 APIError，切换流程必须按模型故障接管而不是当成本地失败
	if ev.Error != nil && ev.Error.Type == model.ErrorTypeAPIError {
		return &modelRequestError{errors.New(ev.Error.Message)}
	}
	if ev.Error != nil {
		return errors.New(ev.Error.Message)
	}
	return errors.New("模型运行失败")
}

// compactNow 在模型边界同步整理旧交互，成功返回后由调用方重新装配
func (a *Agent) compactNow(ctx context.Context, session *Session, tc *tools.ToolContext) error {
	cfg := configFor(ctx)
	generation := generationFrom(ctx)
	if generation == nil {
		return fmt.Errorf("模型运行态未初始化")
	}
	candidate := session.BuildCompactionCandidate(generation.forKind(session.kind).contextWindow, cfg.Compactor.RecentTailRatio)
	if candidate == nil {
		session.ClearCompactionFailure()
		if requestTokens(tc.Assembled, tc.ToolDeclarations) > generation.forKind(session.kind).contextWindow {
			return fmt.Errorf("当前输入超过模型窗口，且没有可安全压缩的旧交互")
		}
		return nil
	}
	compactionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	stopShutdownWatch := context.AfterFunc(a.ctx, cancel)
	defer stopShutdownWatch()
	remainingSteps := cfg.Compactor.MaxSteps
	if remainingSteps <= 0 {
		remainingSteps = 12
	}
	if err := a.runCompactionWithRepair(compactionCtx, session, candidate, &tc.CompactionAttempts, &remainingSteps); err != nil {
		session.MarkCompactionFailure(time.Now())
		return err
	}
	return nil
}

// compactAfterOverflow 在 provider 明确报告上下文溢出时强制尝试一次安全压缩
func (a *Agent) compactAfterOverflow(ctx context.Context, kind string, targetID int64, toolsList []tool.Tool, session *Session) bool {
	retryCtx, err := a.buildConversationToolContext(ctx, kind, targetID, session)
	if err != nil {
		zap.L().Warn("构建压缩后工具上下文失败", zap.String("conversation_kind", kind), zap.Int64("target_id", targetID), zap.Error(err))
		return false
	}
	tc := tools.GetToolContext(retryCtx)
	hint, hintErr := a.assembleMessages(retryCtx, session)
	if hintErr != nil {
		return false
	}
	tc.Assembled = hint
	tc.ToolDeclarations = toolDeclarations(toolsList)
	if err := a.compactNow(retryCtx, session, tc); err != nil {
		zap.L().Warn("上下文溢出后的强制压缩失败", zap.String("conversation_kind", kind), zap.Int64("target_id", targetID), zap.Error(err))
		return false
	}
	return true
}

func toolDeclarations(toolsList []tool.Tool) []*tool.Declaration {
	declarations := make([]*tool.Declaration, 0, len(toolsList))
	for _, item := range toolsList {
		if item == nil {
			continue
		}
		if declaration := item.Declaration(); declaration != nil {
			declarations = append(declarations, declaration)
		}
	}
	return declarations
}

func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"context length", "context_length_exceeded", "maximum context", "too many tokens",
		"input is too long", "prompt is too long", "token limit exceeded", "context window",
		"input token count", "maximum number of tokens", "request too large",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
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

func (a *Agent) retryAfterOverflow(ctx context.Context, kind string, targetID int64, state *runState, toolsList []tool.Tool, session *Session) error {
	if state == nil || state.steps.Remaining() <= 0 {
		return fmt.Errorf("本轮步数用完")
	}
	retryCtx, err := a.buildConversationToolContext(ctx, kind, targetID, session)
	if err != nil {
		return err
	}
	retryCtx = withRunState(retryCtx, state)
	generation := generationFrom(retryCtx)
	if generation == nil {
		return fmt.Errorf("模型运行态未初始化")
	}
	chatAgent, err := a.newChatAgent(generation, toolsList)
	if err != nil {
		return err
	}
	messages, err := a.assembleMessages(retryCtx, session)
	if err != nil {
		return err
	}
	return a.runAgent(retryCtx, chatAgent, toolsList, messages)
}
