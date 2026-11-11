package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
)

// ErrSessionBusy 表示会话正在运行，本次后台整理没有执行
var ErrSessionBusy = errors.New("会话正在运行")

// ManualCompactionResult 描述一次后台手动整理的结果
type ManualCompactionResult struct {
	Action   string // compacted：整理了旧消息；prompt：只刷新了当前提示词
	Messages int
}

// CompactConversationNow 后台手动整理一个 QQ 会话：有旧段就压缩，没有就只刷新代头让当前提示词立即生效
func (a *Agent) CompactConversationNow(ctx context.Context, kind string, targetID int64) (ManualCompactionResult, error) {
	if kind != memory.ConversationKindGroup && kind != memory.ConversationKindPrivate {
		return ManualCompactionResult{}, fmt.Errorf("只支持群聊和私聊")
	}
	if targetID <= 0 {
		return ManualCompactionResult{}, fmt.Errorf("会话参数无效")
	}
	generation := a.acquireGeneration()
	if generation == nil || generation.forKind(kind).model == nil {
		return ManualCompactionResult{}, ErrNoModel
	}
	session := a.sessions.get(kind, targetID)
	if !session.maintenanceMu.TryLock() {
		return ManualCompactionResult{}, ErrSessionBusy
	}
	defer session.maintenanceMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	ctx = withGeneration(ctx, generation)
	cfg := config.Snapshot()
	candidate := session.BuildCompactionCandidate(generation.contextWindow, cfg.Compactor.RecentTailRatio)
	if candidate == nil {
		if err := a.refreshSessionHead(ctx, session); err != nil {
			return ManualCompactionResult{}, err
		}
		return ManualCompactionResult{Action: "prompt"}, nil
	}
	attempts, remaining := 0, cfg.Compactor.MaxSteps
	if err := a.runCompactionWithRepair(ctx, session, candidate, &attempts, &remaining); err != nil {
		session.MarkCompactionFailure(time.Now())
		return ManualCompactionResult{}, err
	}
	return ManualCompactionResult{Action: "compacted", Messages: len(candidate.Refs)}, nil
}

// refreshSessionHead 删除本代代头，让下一次装配按当前人格、摘要和置顶记忆重写
func (a *Agent) refreshSessionHead(ctx context.Context, session *Session) error {
	released, err := a.memory.RefreshTranscriptHead(ctx, session.kind, session.targetID)
	if err != nil {
		return err
	}
	session.DropHeads()
	a.releaseTranscriptCandidates(ctx, released)
	return nil
}
