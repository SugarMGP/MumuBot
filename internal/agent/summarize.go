package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/agentic"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/logger"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelstats"
	"mumu-bot/internal/persona"
	"mumu-bot/internal/tools"

	"go.uber.org/zap"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// summarySystemInstruction 与旧摘要中间件的系统提示保持一致
const summarySystemInstruction = "You are a helpful AI assistant tasked with summarizing conversations."

// summarizeIfNeeded 在 life/worker 达到窗口比例时整理上下文
func (c *agentCallbacks) summarizeIfNeeded(ctx context.Context, session *Session, messages []*agentic.Message, declarations []*tool.Declaration) (bool, error) {
	if session.kind != memory.ConversationKindLife && session.kind != memory.ConversationKindWorker {
		return false, nil
	}
	generation := generationFrom(ctx)
	if generation == nil {
		return false, nil
	}
	window := generation.forKind(session.kind).contextWindow
	ratio := configFor(ctx).Compactor.TriggerRatio
	if window <= 0 || ratio <= 0 {
		return false, nil
	}
	trigger := int(float64(window) * ratio)
	if trigger <= 0 || requestTokens(messages, declarations) <= trigger {
		return false, nil
	}
	return true, c.agent.summarizeNow(ctx, session, messages)
}

// summarizeNow 生成摘要并提交检查点；调用方负责重新装配
func (a *Agent) summarizeNow(ctx context.Context, session *Session, messages []*agentic.Message) error {
	generation := generationFrom(ctx)
	if generation == nil {
		return fmt.Errorf("模型运行态未初始化")
	}
	role := generation.forKind(session.kind)
	cfg := configFor(ctx)
	tc := tools.GetToolContext(ctx)
	if tc == nil {
		tc = &tools.ToolContext{ConversationKind: session.kind, TargetID: session.targetID, Session: session}
		ctx = tools.WithToolContext(ctx, tc)
	}
	instruction := persona.RenderFor(workerSummaryInstruction, cfg)
	if session.kind == memory.ConversationKindLife {
		instruction = persona.Render(lifeSummaryInstruction, persona.IdentityFromConfig(cfg.Identity))
	}

	var input []*agentic.Message
	if attempt := switchAttemptFrom(ctx); attempt != nil && attempt.portable {
		built, err := switchSummaryInput(ctx, agentic.SystemAgenticMessage(summarySystemInstruction), agentic.UserAgenticMessage(instruction), messages)
		if err != nil {
			return err
		}
		input = built
	} else {
		contextMessages := splitSystemMessages(messages)
		input = make([]*agentic.Message, 0, len(contextMessages)+2)
		input = append(input, agentic.SystemAgenticMessage(summarySystemInstruction))
		input = append(input, contextMessages...)
		input = append(input, agentic.UserAgenticMessage(instruction))
		if err := checkSwitchInput(ctx, input, tc.ToolDeclarations); err != nil {
			return err
		}
	}

	task := modelTaskFrom(ctx).task
	if task == "" {
		task = taskForKind(session.kind)
	}
	started := time.Now()
	logger.FromContext(ctx).With(zap.String("task", task), zap.String("model", role.modelName)).Info("摘要请求开始")
	record := func(usage *model.Usage, failed bool) {
		total, prompt, cached, reported := 0, 0, 0, false
		if usage != nil {
			total, prompt, cached, reported = usage.TotalTokens, usage.PromptTokens, usage.PromptTokensDetails.CachedTokens, true
		}
		modelstats.Record(task, role.modelName, time.Since(started), failed, total, prompt, cached, reported)
	}
	stream, err := role.model.GenerateContent(ctx, &model.Request{
		Messages:         llm.ToModelMessages(role.provider, role.modelName, input),
		GenerationConfig: role.config,
	})
	if err != nil {
		record(nil, true)
		logger.FromContext(ctx).With(zap.String("task", task), zap.String("model", role.modelName)).
			Error("摘要请求失败", zap.Duration("duration", time.Since(started)), zap.Error(err))
		return err
	}
	text := ""
	var usage *model.Usage
	for response := range stream {
		if response == nil {
			continue
		}
		if response.Usage != nil {
			usage = response.Usage
		}
		if response.Error != nil {
			record(usage, true)
			logger.FromContext(ctx).With(zap.String("task", task), zap.String("model", role.modelName)).
				Error("摘要请求失败", zap.Duration("duration", time.Since(started)), zap.String("message", response.Error.Message))
			return fmt.Errorf("摘要请求失败: %s", response.Error.Message)
		}
		if len(response.Choices) > 0 {
			text = responseText(response.Choices[0].Message)
		}
	}
	record(usage, false)
	fields := []zap.Field{zap.Duration("duration", time.Since(started))}
	if usage != nil {
		fields = append(fields, zap.Any("usage", usage))
	}
	logger.FromContext(ctx).With(zap.String("task", task), zap.String("model", role.modelName)).Info("摘要请求完成", fields...)
	text = strings.TrimSpace(text)
	if text == "" {
		return &compactionInputError{Err: fmt.Errorf("摘要正文为空，保留原上下文")}
	}

	var head []*agentic.Message
	noteKind := kindWorkerSummary
	if session.kind == memory.ConversationKindLife {
		noteKind = kindLifeSummary
		head, err = buildLifeHead(ctx, a.memory.WithContext(ctx), a.lifeInstructionText(ctx))
		if err != nil {
			return err
		}
	} else {
		head = []*agentic.Message{previewSystemMessage(a.personaFor(ctx), memory.ConversationKindWorker, tc.TargetID)}
	}
	note := agentic.UserAgenticMessage(systemNote("上下文摘要", text))
	note.Extra = map[string]any{"kind": noteKind}
	after := append(append(make([]*agentic.Message, 0, len(head)+1), head...), note)

	commitCtx, cancel := switchCommitContext(ctx)
	defer cancel()
	if session.kind == memory.ConversationKindLife {
		if err := a.saveLifeContext(commitCtx, session, messages, after, head); err != nil {
			return err
		}
	} else if err := a.saveWorkerContext(commitCtx, uint(tc.TargetID), session, messages, after, head); err != nil {
		return err
	}
	return nil
}

// splitSystemMessages 去掉消息开头的系统提示，与旧摘要输入一致
func splitSystemMessages(messages []*agentic.Message) []*agentic.Message {
	index := 0
	for index < len(messages) && messages[index] != nil && messages[index].Role == agentic.RoleSystem {
		index++
	}
	return messages[index:]
}

func responseText(message model.Message) string {
	text := message.Content
	for _, part := range message.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			text += *part.Text
		}
	}
	return text
}
