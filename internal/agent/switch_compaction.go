package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"mumu-bot/internal/llm"
	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

var errSwitchBudgetExhausted = errors.New("模型切换摘要步数预算已用尽")

func (a *Agent) compactSwitchSession(ctx context.Context, session *Session, target ModelTarget, original, destination roleModel, chatTools []tool.BaseTool, unavailable map[uint]bool, report *ModelSwitchReport) error {
	oldDeadline := time.Now().Add(modelSwitchWait)
	steps := configFor(ctx).Compactor.MaxSteps
	if steps <= 0 {
		steps = 12
	}
	oldAttempt := &switchAttempt{remaining: steps, window: target.ContextWindow, report: report}
	newAttempt := &switchAttempt{portable: true, remaining: steps, window: target.ContextWindow, report: report}
	usingTarget := unavailable[original.profileID]
	loaded := original.model != nil
	compacted := false
	toolInfos := modelToolInfos(ctx, chatTools)
	triggerRatio := configFor(ctx).Compactor.TriggerRatio
	previewCtx := withGeneration(ctx, &Generation{roleModel: roleModel{contextWindow: target.ContextWindow}})
	report.Stage = "storage"
	workingSet, err := previewPersistedMessages(previewCtx, a.memory, a.personaFor(ctx), session.kind, session.targetID)
	if err != nil {
		return err
	}
	for {
		messages := workingSet
		incompatible, needsCapacity, tokens := session.switchContextCheck(messages, toolInfos, target, triggerRatio)
		if !incompatible && !needsCapacity {
			return nil
		}
		var candidate *compactionCandidate
		report.Stage = "boundary"
		isQQ := session.kind == memory.ConversationKindGroup || session.kind == memory.ConversationKindPrivate
		if incompatible {
			if isQQ {
				candidate, err = session.BuildProtocolCompaction(target)
			} else {
				messages, err = protocolSummaryPrefix(messages, target)
			}
		} else if isQQ {
			candidate = session.BuildCompactionCandidate(target.ContextWindow, configFor(ctx).Compactor.RecentTailRatio)
		} else {
			messages = contextSummaryPrefix(session, messages)
		}
		if err != nil {
			return err
		}
		if isQQ {
			messages = nil
			if candidate != nil {
				messages = candidate.Messages
			}
		}
		if len(transcriptIDs(messages)) == 0 {
			if incompatible {
				return fmt.Errorf("no closed protocol compaction candidate")
			}
			if tokens > target.ContextWindow {
				stage := "boundary"
				if compacted {
					stage = "remaining_window"
				}
				return report.windowFailure(stage, tokens, target.ContextWindow)
			}
			return nil
		}
		if !usingTarget && !loaded {
			report.Stage = "storage"
			profile, loadErr := a.modelStore.Get(ctx, original.profileID)
			if loadErr != nil {
				return loadErr
			}
			key, loadErr := a.modelStore.DecryptKey(profile)
			if loadErr == nil {
				original, loadErr = buildRoleModel(profile, key)
			}
			if loadErr != nil {
				usingTarget = true
				unavailable[profile.ID] = true
			}
			loaded = true
		}
		for {
			if !usingTarget && !time.Now().Before(oldDeadline) {
				usingTarget = true
			}
			attempt, selected := oldAttempt, original
			attemptCtx, cancelAttempt := context.WithCancel(ctx)
			if usingTarget {
				attempt, selected = newAttempt, destination
				selected.model = &requestModel{AgenticModel: selected.model, idleTimeout: switchTargetIdleTimeout}
				report.Fallback = true
			} else {
				attemptCtx = llm.WithRequestTracker(attemptCtx, modelRequestDeadline(oldDeadline))
				selected.model = &requestModel{AgenticModel: selected.model}
			}
			selected.contextWindow = target.ContextWindow
			attemptCtx = context.WithValue(attemptCtx, switchAttemptKey{}, attempt)
			attemptCtx = withGeneration(attemptCtx, &Generation{roleModel: selected, lifeModel: selected, workerModel: selected})
			report.Stage = "storage"
			err = a.compactForWindow(attemptCtx, session, candidate, messages)
			cancelAttempt()
			if err == nil {
				compacted = true
				report.Compacted = true
				zap.L().Info("模型切换上下文已整理", zap.String("session", sessionKey(session.kind, session.targetID)), zap.Bool("fallback", usingTarget), zap.Int("entries", len(transcriptIDs(messages))))
				break
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var windowErr *switchWindowError
			var requestErr *modelRequestError
			localOverflow := errors.As(err, &windowErr)
			requestFailed := errors.As(err, &requestErr)
			if usingTarget && (localOverflow || (requestFailed && isContextOverflowError(err))) {
				if candidate == nil && localOverflow {
					attempt.remaining++
				}
				messages = smallerSwitchPrefix(messages, !isQQ)
				if len(messages) == 0 {
					if localOverflow {
						return report.windowFailure("input_window", windowErr.tokens, windowErr.window)
					}
					report.Stage = "upstream_window"
					return err
				}
				if candidate != nil {
					candidate, err = session.compactionForPrefix(messages)
					if err != nil {
						return err
					}
					messages = candidate.Messages
				}
				continue
			}
			var inputErr *compactionInputError
			var materialErr *switchMaterialError
			if !usingTarget && (requestFailed || errors.As(err, &inputErr) || errors.Is(err, adk.ErrExceedMaxIterations) || errors.Is(err, errSwitchBudgetExhausted)) {
				if unavailableModel(err) {
					unavailable[original.profileID] = true
				}
				zap.L().Warn("原模型整理失败，改用目标模型", zap.String("session", sessionKey(session.kind, session.targetID)), zap.Uint("profile_id", original.profileID), zap.Bool("timeout", !time.Now().Before(oldDeadline)))
				usingTarget = true
				continue
			}
			if errors.Is(err, errModelRequestIdle) {
				report.Stage = "target_timeout"
			} else if requestFailed || errors.As(err, &inputErr) || errors.As(err, &materialErr) || errors.Is(err, errSwitchBudgetExhausted) || errors.Is(err, adk.ErrExceedMaxIterations) {
				report.Stage = "target"
			}
			return err
		}
		// 每一批必须有真实 transcript 被检查点覆盖，下一次只读取仍然存在的工作集
		workingSet, err = previewPersistedMessages(previewCtx, a.memory, a.personaFor(ctx), session.kind, session.targetID)
		if err != nil {
			return err
		}
		before := transcriptIDs(messages)
		after := make(map[uint]bool)
		for _, id := range transcriptIDs(workingSet) {
			after[id] = true
		}
		for _, id := range before {
			if after[id] {
				return fmt.Errorf("context compaction did not advance the checkpoint")
			}
		}
	}
}

// smallerSwitchPrefix 只在下一条输入之前拆段，ID 检查点不能越过尚未读取的较早条目
func smallerSwitchPrefix(messages []*schema.AgenticMessage, idCheckpoint bool) []*schema.AgenticMessage {
	suffixMin := make([]uint, len(messages)+1)
	suffixMin[len(messages)] = ^uint(0)
	for index := len(messages) - 1; index >= 0; index-- {
		suffixMin[index] = suffixMin[index+1]
		if id := transcriptEntryID(messages[index]); id > 0 && !isHeadEntry(messages[index]) {
			suffixMin[index] = min(suffixMin[index], id)
		}
	}
	open := map[string]bool{}
	through := uint(0)
	boundaries := make([]int, 0)
	for index, message := range messages {
		if message == nil {
			continue
		}
		if !isHeadEntry(message) {
			through = max(through, transcriptEntryID(message))
		}
		for _, block := range message.ContentBlocks {
			if block == nil {
				continue
			}
			if block.FunctionToolCall != nil {
				open[block.FunctionToolCall.CallID] = true
			}
			if block.FunctionToolResult != nil {
				delete(open, block.FunctionToolResult.CallID)
			}
		}
		if through == 0 || len(open) > 0 || index+1 == len(messages) {
			continue
		}
		next := messages[index+1]
		if next == nil || next.Role != schema.AgenticRoleTypeUser {
			continue
		}
		toolResult := false
		for _, block := range next.ContentBlocks {
			if block != nil && block.FunctionToolResult != nil {
				toolResult = true
			}
		}
		if !toolResult && (!idCheckpoint || suffixMin[index+1] > through) {
			boundaries = append(boundaries, index+1)
		}
	}
	if len(boundaries) == 0 {
		return nil
	}
	return messages[:boundaries[(len(boundaries)-1)/2]]
}

func (s *Session) compactionForPrefix(messages []*schema.AgenticMessage) (*compactionCandidate, error) {
	s.mu.Lock()
	entries := make(map[uint]sessionEntry, len(s.seen))
	for _, entry := range s.seen {
		entries[entry.transcriptID] = entry
	}
	s.mu.Unlock()
	items := make([]sessionEntry, 0, len(messages))
	for _, message := range messages {
		entry, exists := entries[transcriptEntryID(message)]
		if !exists {
			return nil, fmt.Errorf("compaction source entry is missing")
		}
		items = append(items, entry)
	}
	candidate := buildCompactionCandidate(items)
	candidate.ConsumedTranscriptIDs = transcriptIDs(messages)
	return candidate, nil
}
