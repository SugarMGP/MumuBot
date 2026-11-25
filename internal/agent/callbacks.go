package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/llm"
	"mumu-bot/internal/logger"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelstats"
	"mumu-bot/internal/tools"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var errBudgetExhausted = errors.New("本轮步数用完")

// agentCallbacks 用一个对象承载模型和工具回调，Agent 通过闭包共享本轮运行态
type agentCallbacks struct {
	agent *Agent
}

func (a *Agent) callbacks() *agentCallbacks { return &agentCallbacks{agent: a} }

func (c *agentCallbacks) modelCallbacks() *model.Callbacks {
	return &model.Callbacks{
		BeforeModel: []model.BeforeModelCallbackStructured{c.beforeModel},
		AfterModel:  []model.AfterModelCallbackStructured{c.afterModel},
	}
}

func (c *agentCallbacks) toolCallbacks() *tool.Callbacks {
	return &tool.Callbacks{
		BeforeTool: []tool.BeforeToolCallbackStructured{c.beforeTool},
		AfterTool:  []tool.AfterToolCallbackStructured{c.afterTool},
	}
}

func (c *agentCallbacks) beforeModel(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
	tc := tools.GetToolContext(ctx)
	if tc == nil || tc.Session == nil {
		return nil, nil
	}
	session, ok := tc.Session.(*Session)
	if !ok || session == nil {
		return nil, nil
	}
	if tc.CompactionRemaining != nil {
		generation := generationFrom(ctx)
		if generation == nil {
			return nil, fmt.Errorf("模型运行态未初始化")
		}
		if args != nil && args.Request != nil && len(tc.CompactorMessages) > 0 {
			role := generation.forKind(tc.ConversationKind)
			if !tc.CompactorStarted {
				tc.CompactorStarted = true
			} else {
				// 运行时每步重建消息，把新增的 assistant/tool 轨迹并回压缩器自己的输入
				merged := 0
				for _, item := range args.Request.Messages {
					if item.Role != model.RoleAssistant && item.Role != model.RoleTool {
						continue
					}
					merged++
					if merged > tc.CompactorFlowMerged {
						tc.CompactorMessages = append(tc.CompactorMessages, llm.FromModelMessages(role.provider, []model.Message{item})...)
					}
				}
				tc.CompactorFlowMerged = merged
			}
			args.Request.Messages = llm.ToModelMessages(role.provider, role.modelName, tc.CompactorMessages)
		}
		if attempt := switchAttemptFrom(ctx); attempt != nil && attempt.portable {
			if args != nil && args.Request != nil {
				if tokens := estimateModelMessages(args.Request.Messages); tokens > attempt.window {
					return nil, stop(tc, tools.StopReasonTerminal, &switchWindowError{tokens: tokens, window: attempt.window})
				}
			}
		}
		if *tc.CompactionRemaining <= 0 {
			return nil, stop(tc, tools.StopReasonBudget, errBudgetExhausted)
		}
		*tc.CompactionRemaining--
		return nil, nil
	}

	if session.switchPending() {
		return nil, stop(tc, tools.StopReasonModelSwitch, errModelSwitchPending)
	}
	if tc.EndAfterSleep {
		return nil, stop(tc, tools.StopReasonSleep, errLifeSleepComplete)
	}
	messages, err := c.agent.assembleForRun(ctx, session)
	if err != nil {
		return nil, err
	}
	var flowMessages []model.Message
	if args != nil && args.Request != nil {
		flowMessages = args.Request.Messages
	}
	messages, err = c.agent.syncToolChains(ctx, session, flowMessages, messages)
	if err != nil {
		return nil, stop(tc, tools.StopReasonTerminal, err)
	}
	tc.Assembled = messages
	if session.NeedsCompactionBeforeModel() {
		if session.CompactionCoolingDown(time.Now()) {
			return nil, stop(tc, tools.StopReasonCompaction, errCompactionPending)
		}
		if err := c.agent.compactNow(ctx, session, tc); err != nil {
			if session.NeedsCompactionBeforeModel() {
				return nil, stop(tc, tools.StopReasonCompaction, fmt.Errorf("%w: %v", errCompactionPending, err))
			}
			return nil, err
		}
		messages, err = c.agent.assembleForRun(ctx, session)
		if err != nil {
			return nil, err
		}
		tc.Assembled = messages
	}
	if summarized, sumErr := c.summarizeIfNeeded(ctx, session, messages, tc.ToolDeclarations); sumErr != nil {
		return nil, sumErr
	} else if summarized {
		messages, err = c.agent.assembleForRun(ctx, session)
		if err != nil {
			return nil, err
		}
		tc.Assembled = messages
	}
	if tc.ConversationKind == memory.ConversationKindLife {
		hint, hintErr := injectLifeUsageHint(ctx, session)
		if hintErr != nil {
			return nil, hintErr
		}
		if hint != nil {
			messages = append(messages, hint)
		}
	}
	update, err := session.syncPinnedMemories(ctx, c.agent.memory, messages)
	if err != nil {
		return nil, err
	}
	if update != nil {
		messages = append(messages, update)
	}
	session.RecordRequestBoundary(messages)

	if state := runStateFrom(ctx); state != nil && state.steps != nil && !state.steps.Consume() {
		return nil, stop(tc, tools.StopReasonBudget, errBudgetExhausted)
	}
	tc.ModelRequested = true
	tc.Assembled = messages

	selected := generationFrom(ctx)
	if selected == nil {
		return nil, fmt.Errorf("模型运行态未初始化")
	}
	role := selected.forKind(session.kind)
	if args != nil && args.Request != nil {
		args.Request.Messages = llm.ToModelMessages(role.provider, role.modelName, messages)
	}
	ctx = c.logModelRequest(ctx, tc, role)
	return &model.BeforeModelResult{Context: ctx}, nil
}

// logModelRequest 在请求真正发出前记录动态上下文并保存开始时间

func (c *agentCallbacks) logModelRequest(ctx context.Context, tc *tools.ToolContext, role roleModel) context.Context {
	info := modelTaskFrom(ctx)
	task, modelName := info.task, info.modelName
	if modelName == "" {
		modelName = role.modelName
	}
	if task == "" {
		task = taskForKind(tc.ConversationKind)
	}
	log := logger.FromContext(ctx).With(zap.String("task", task), zap.String("model", modelName), zap.Int("message_count", len(tc.Assembled)))
	if logger.ContentEnabled(ctx) {
		state := logStateFrom(ctx)
		seen := map[string]int{}
		first := true
		if state != nil {
			if state.seen == nil {
				state.seen = map[string]int{}
			}
			seen = state.seen
			first = !state.started
		}
		entries := dynamicLogContext(tc.Assembled, seen, first)
		if state != nil {
			state.seen = seen
			state.started = true
		}
		if len(entries) > 0 {
			log = log.With(zap.Any("context", entries))
		}
	}
	log.Info("模型请求开始")
	return context.WithValue(ctx, modelStartKey{}, time.Now())
}

func (c *agentCallbacks) afterModel(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
	tc := tools.GetToolContext(ctx)
	if tc == nil || tc.Session == nil {
		return nil, nil
	}
	session, ok := tc.Session.(*Session)
	if !ok || session == nil {
		return nil, nil
	}
	info := modelTaskFrom(ctx)
	generation := generationFrom(ctx)
	if generation == nil {
		return nil, fmt.Errorf("模型运行态未初始化")
	}
	role := generation.forKind(session.kind)
	task := info.task
	if tc.CompactionRemaining != nil {
		duration := time.Since(modelStarted(ctx))
		if args.Error != nil {
			modelstats.Record(task, role.modelName, duration, true, 0, 0, 0, false)
			return nil, nil
		}
		total, prompt, cached, reported := 0, 0, 0, false
		if args.Response != nil && args.Response.Usage != nil {
			usage := args.Response.Usage
			total, prompt, cached, reported = usage.TotalTokens, usage.PromptTokens, usage.PromptTokensDetails.CachedTokens, true
		}
		modelstats.Record(task, role.modelName, duration, false, total, prompt, cached, reported)
		return nil, nil
	}
	if task == "" {
		task = taskForKind(session.kind)
	}
	duration := time.Duration(0)
	if started := modelStarted(ctx); !started.IsZero() {
		duration = time.Since(started)
	}
	if args.Error != nil {
		modelstats.Record(task, role.modelName, duration, true, 0, 0, 0, false)
		logger.FromContext(ctx).With(zap.String("task", task), zap.String("model", role.modelName)).
			Error("模型请求失败", zap.Duration("duration", duration), zap.Error(args.Error))
		return nil, nil
	}

	message := llm.FromModelResponse(role.provider, args.Response)
	if message == nil {
		return nil, nil
	}
	if _, err := session.AppendAssistant(ctx, tc.RunID, message); err != nil {
		return nil, err
	}
	usage := message.ResponseMeta
	if usage != nil && usage.TokenUsage != nil && usage.TokenUsage.TotalTokens > 0 {
		session.ObserveResponseUsage(tc.Assembled, tc.ToolDeclarations, usage.TokenUsage)
		session.SetTokenBase(message, usage.TokenUsage, tc.Assembled, role)
		modelstats.Record(task, role.modelName, duration, false,
			usage.TokenUsage.TotalTokens, usage.TokenUsage.PromptTokens, usage.TokenUsage.PromptTokenDetails.CachedTokens, true)
	} else {
		session.ClearTokenBase()
		modelstats.Record(task, role.modelName, duration, false, 0, 0, 0, false)
	}
	ids := transcriptIDs(tc.Assembled)
	seqs, err := session.memory.MarkVisible(ctx, session.kind, session.targetID, ids)
	if err != nil {
		return nil, err
	}
	session.ApplyVisibleSeqs(seqs)

	fields := []zap.Field{zap.Duration("duration", duration)}
	if usage != nil && usage.TokenUsage != nil {
		fields = append(fields, zap.Any("usage", usage.TokenUsage))
	}
	if logger.ContentEnabled(ctx) {
		if text := modelOutputText(message); text != "" {
			fields = append(fields, zap.String("output", text))
		}
	}
	logger.FromContext(ctx).With(zap.String("task", task), zap.String("model", role.modelName)).Info("模型请求完成", fields...)
	return nil, nil
}

func (c *agentCallbacks) beforeTool(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
	tc := tools.GetToolContext(ctx)
	arguments := canonicalizeToolArguments(args.Declaration, args.Arguments)
	if tc != nil && tc.CompactionRemaining != nil {
		logToolStart(ctx, args.ToolName, args.ToolCallID, arguments)
		return &tool.BeforeToolResult{ModifiedArguments: arguments}, nil
	}
	if tc == nil || tc.Session == nil || args.ToolCallID == "" {
		return &tool.BeforeToolResult{ModifiedArguments: arguments}, nil
	}
	session, ok := tc.Session.(*Session)
	if !ok || session == nil {
		return &tool.BeforeToolResult{ModifiedArguments: arguments}, nil
	}
	id, existing, resumed, err := session.BeginToolTranscript(ctx, tc.RunID, args.ToolName, args.ToolCallID, string(arguments))
	if err != nil {
		return nil, stop(tc, tools.StopReasonTerminal, err)
	}
	if resumed {
		tc.ToolTranscriptID = 0
		return &tool.BeforeToolResult{ModifiedArguments: arguments, CustomResult: existing}, nil
	}
	tc.ToolTranscriptID = id
	logToolStart(ctx, args.ToolName, args.ToolCallID, arguments)
	return &tool.BeforeToolResult{ModifiedArguments: arguments}, nil
}

func (c *agentCallbacks) afterTool(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
	tc := tools.GetToolContext(ctx)
	if tc == nil || tc.Session == nil {
		return nil, nil
	}
	session, ok := tc.Session.(*Session)
	if !ok || session == nil {
		return nil, nil
	}
	terminal := args.Error != nil && terminalToolError(ctx, args.Error)
	if tc.CompactionRemaining != nil {
		status := "succeeded"
		resultText := tools.ResultText(args.Result)
		if args.Error != nil {
			status = "failed"
			resultText = args.Error.Error()
		}
		display := resultText
		if args.Error == nil && !tools.IsWindowReadTool(args.ToolName) && tc.MemoryMgr != nil {
			externalized, _, _, externalizeErr := tools.ExternalizeText(ctx, tc.MemoryMgr, resultText)
			if externalizeErr != nil {
				logToolFinish(ctx, args.ToolName, args.ToolCallID, "failed", externalizeErr)
				return &tool.AfterToolResult{CustomResult: tools.ToolErrorResult(externalizeErr)}, nil
			}
			display = externalized
		}
		logToolFinish(ctx, args.ToolName, args.ToolCallID, status, resultText)
		if terminal {
			return nil, stop(tc, tools.StopReasonTerminal, args.Error)
		}
		if args.Error != nil {
			return &tool.AfterToolResult{CustomResult: tools.ToolErrorResult(args.Error)}, nil
		}
		if display != resultText {
			return &tool.AfterToolResult{CustomResult: display}, nil
		}
		return nil, nil
	}
	id := tc.ToolTranscriptID
	status := "succeeded"
	resultText := ""
	var enhanced *tools.Result
	if args.Error != nil {
		status = "failed"
		if terminal {
			resultText = args.Error.Error()
		} else {
			// 可恢复错误按旧工具契约返回 JSON，运行时收到 CustomResult 后会继续循环
			resultText = tools.ToolErrorResult(args.Error)
		}
	} else {
		if result, ok := tools.ResultOf(args.Result); ok {
			enhanced = result
		}
		resultText = tools.ResultText(args.Result)
		if !tools.ResultSucceeded(args.Result) {
			status = "failed"
		}
		if generation := generationFrom(ctx); generation != nil && generation.mcp != nil {
			sanitized, sanitizeErr := generation.mcp.SanitizeResult(ctx, args.ToolName, args.Result)
			if sanitizeErr == nil && sanitized != nil {
				args.Result = sanitized
				enhanced, _ = tools.ResultOf(sanitized)
				resultText = tools.ResultText(sanitized)
			}
		}
	}
	if id != 0 {
		display, _, finishErr := session.FinishToolTranscript(ctx, id, status, resultText, enhanced, "")
		if finishErr != nil {
			return nil, stop(tc, tools.StopReasonTerminal, finishErr)
		}
		tc.ToolTranscriptID = 0
		if args.ToolName == "sleepUntil" && status == "succeeded" {
			tc.EndAfterSleep = true
		}
		// 日志保留外置前的完整文本，模型继续只看到预览
		logToolFinish(ctx, args.ToolName, args.ToolCallID, status, resultText)
		if terminal {
			return nil, stop(tc, tools.StopReasonTerminal, args.Error)
		}
		return &tool.AfterToolResult{CustomResult: display}, nil
	}
	logToolFinish(ctx, args.ToolName, args.ToolCallID, status, resultText)
	if terminal {
		return nil, stop(tc, tools.StopReasonTerminal, args.Error)
	}
	if args.Error != nil {
		return &tool.AfterToolResult{CustomResult: tools.ToolErrorResult(args.Error)}, nil
	}
	return nil, nil
}

func stop(tc *tools.ToolContext, reason string, cause error) error {
	if tc != nil {
		tc.StopReason = reason
		tc.StopCause = cause
	}
	return agent.NewStopError(cause.Error())
}

func terminalToolError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	var terminal *tools.TerminalToolError
	return ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, memory.ErrSnapshotChanged) || errors.Is(err, memory.ErrConversationUnavailable) ||
		errors.As(err, &terminal)
}

func taskForKind(kind string) string {
	switch kind {
	case memory.ConversationKindLife:
		return "life"
	case memory.ConversationKindWorker:
		return "worker"
	default:
		return "react"
	}
}

// canonicalizeToolArguments 按工具 schema 规范数字与布尔字符串，保持旧参数矫正行为
var sortedJSONAPI = sonic.Config{SortMapKeys: true, UseNumber: true}.Froze()

func canonicalizeToolArguments(declaration *tool.Declaration, arguments []byte) []byte {
	trimmed := strings.TrimSpace(string(arguments))
	if trimmed == "" || declaration == nil || declaration.InputSchema == nil {
		return arguments
	}
	var payload any
	if sortedJSONAPI.UnmarshalFromString(trimmed, &payload) != nil {
		return arguments
	}
	payload = coerceToolArgument(payload, declaration.InputSchema)
	canonical, err := sortedJSONAPI.MarshalToString(payload)
	if err != nil {
		return arguments
	}
	return []byte(canonical)
}

func coerceToolArgument(value any, schema *tool.Schema) any {
	if schema == nil {
		return value
	}
	switch schema.Type {
	case "object":
		object, ok := value.(map[string]any)
		if !ok || schema.Properties == nil {
			return value
		}
		for name, fieldValue := range object {
			if fieldSchema, exists := schema.Properties[name]; exists {
				object[name] = coerceToolArgument(fieldValue, fieldSchema)
			}
		}
		return object
	case "array":
		items, ok := value.([]any)
		if !ok || schema.Items == nil {
			return value
		}
		for index := range items {
			items[index] = coerceToolArgument(items[index], schema.Items)
		}
		return items
	}
	text, ok := value.(string)
	if !ok {
		return value
	}
	text = strings.TrimSpace(text)
	if !sonic.ValidString(text) {
		return value
	}
	switch schema.Type {
	case "integer", "number":
		return json.Number(text)
	case "boolean":
		if strings.EqualFold(text, "true") {
			return true
		}
		if strings.EqualFold(text, "false") {
			return false
		}
	}
	return value
}

func logToolStart(ctx context.Context, name, callID string, arguments []byte) {
	log := logger.FromContext(ctx).With(zap.String("tool", name), zap.String("call_id", callID))
	if logger.ContentEnabled(ctx) {
		log.Info("工具调用开始", zap.Any("arguments", logger.Payload(json.RawMessage(arguments))))
	} else {
		log.Info("工具调用开始")
	}
}

func logToolFinish(ctx context.Context, name, callID, status string, result any) {
	log := logger.FromContext(ctx).With(zap.String("tool", name), zap.String("call_id", callID))
	fields := []zap.Field{zap.String("status", status)}
	if logger.ContentEnabled(ctx) {
		fields = append(fields, zap.Any("result", logger.Payload(result)))
	}
	if status == "succeeded" {
		log.Info("工具调用完成", fields...)
	} else {
		log.Warn("工具调用失败", fields...)
	}
}
