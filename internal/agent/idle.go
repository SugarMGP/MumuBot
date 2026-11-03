package agent

import (
	"context"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"

	"go.uber.org/zap"
)

const idleCompactionTimeout = 15 * time.Minute

// scanIdleCompactions 只做廉价筛选并投递空闲压缩，不在调度循环里执行
func (a *Agent) scanIdleCompactions() {
	cfg := config.Snapshot()
	if cfg.Compactor.IdleMinutes <= 0 || a.stopping.Load() {
		return
	}
	for _, session := range a.sessions.snapshot() {
		if session == nil || session.kind == "" {
			continue
		}
		if session.kind != memory.ConversationKindGroup && session.kind != memory.ConversationKindPrivate {
			continue
		}
		last := session.LastActivityAt()
		if last.IsZero() || time.Since(last) < time.Duration(cfg.Compactor.IdleMinutes)*time.Minute {
			continue
		}
		if !session.IdleAvailable() || session.HasActionablePending() {
			continue
		}
		if session.CompactionCoolingDown(time.Now()) {
			continue
		}
		if session.kind == memory.ConversationKindGroup && a.groupConcurrency.IsRunning(session.targetID) {
			continue
		}
		if session.kind == memory.ConversationKindPrivate && a.privateConcurrency.IsRunning(session.targetID) {
			continue
		}
		select {
		case a.idleSlots <- struct{}{}:
		default:
			return
		}
		a.idleMu.Lock()
		if a.stopping.Load() {
			a.idleMu.Unlock()
			<-a.idleSlots
			return
		}
		a.idleWg.Add(1)
		a.idleMu.Unlock()
		go a.runIdleCompaction(session)
	}
}

func (a *Agent) runIdleCompaction(session *Session) {
	defer a.idleWg.Done()
	defer func() { <-a.idleSlots }()
	if a.stopping.Load() {
		return
	}
	if !session.maintenanceMu.TryLock() {
		return
	}
	defer session.maintenanceMu.Unlock()

	cfg := config.Snapshot()
	if cfg.Compactor.IdleMinutes <= 0 {
		return
	}
	last := session.LastActivityAt()
	if last.IsZero() || time.Since(last) < time.Duration(cfg.Compactor.IdleMinutes)*time.Minute {
		return
	}
	if !session.IdleAvailable() || session.HasActionablePending() {
		return
	}
	generation := a.acquireGeneration()
	if generation == nil || generation.contextWindow <= 0 {
		if !session.LastActivityAt().After(last) {
			session.MarkIdleDone()
		}
		return
	}
	defer generation.wg.Done()
	ctx, cancel := context.WithTimeout(a.ctx, idleCompactionTimeout)
	defer cancel()
	session.MarkIdleStart(cancel)
	defer session.ClearIdleStart()
	if session.HasActionablePending() || session.LastActivityAt().After(last) {
		return
	}
	ctx = withGeneration(ctx, generation)
	total := session.LastResponseTokens()
	if total <= 0 {
		session.MarkIdleDone()
		return
	}
	if total < int(float64(generation.contextWindow)*cfg.Compactor.IdleMinRatio) {
		if !session.LastActivityAt().After(last) {
			session.MarkIdleDone()
		}
		return
	}
	candidate, err := session.BuildCompactionCandidate(generation.contextWindow, cfg.Compactor.RecentTailRatio)
	if err != nil {
		zap.L().Warn("空闲压缩没有可用候选", zap.String("kind", session.kind), zap.Int64("target_id", session.targetID), zap.Error(err))
		if !session.LastActivityAt().After(last) {
			session.MarkIdleDone()
		}
		return
	}
	remaining := cfg.Compactor.MaxSteps
	if remaining <= 0 {
		remaining = 12
	}
	attempts := 0
	if err := a.runCompactionWithRepair(ctx, session, candidate, &attempts, &remaining); err != nil {
		zap.L().Warn("空闲压缩失败", zap.String("kind", session.kind), zap.Int64("target_id", session.targetID), zap.Error(err))
		if !session.LastActivityAt().After(last) {
			session.MarkIdleDone()
		}
		return
	}
	if !session.LastActivityAt().After(last) {
		session.MarkIdleDone()
	}
	session.MarkTopicMaintained()
	zap.L().Info("空闲压缩完成", zap.String("kind", session.kind), zap.Int64("target_id", session.targetID))
}
