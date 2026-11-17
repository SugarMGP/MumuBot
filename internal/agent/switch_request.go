package agent

import (
	"context"
	"errors"
	"time"

	"mumu-bot/internal/memory"

	"go.uber.org/zap"
)

const modelSwitchWait = 60 * time.Second

var errModelSwitchPending = errors.New("模型切换正在等待当前回合收尾")

func (s *Session) trackModelRequest(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.switching {
		return ctx, nil, errModelSwitchPending
	}
	requestCtx, cancel := context.WithCancelCause(ctx)
	s.modelRequestSeq++
	seq := s.modelRequestSeq
	s.modelCancel = cancel
	return requestCtx, func() {
		s.mu.Lock()
		if s.modelRequestSeq == seq {
			s.modelCancel = nil
		}
		s.mu.Unlock()
		cancel(nil)
	}, nil
}

func (s *Session) switchPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.switching
}

// lockForModelSwitch 不取消工具；超时只中断当前模型请求，下一模型边界会让出维护锁
func (s *Session) lockForModelSwitch(ctx context.Context, deadline time.Time) (bool, error) {
	s.mu.Lock()
	if s.switching {
		s.mu.Unlock()
		return false, errModelSwitchPending
	}
	s.switching = true
	s.mu.Unlock()
	timer := time.NewTimer(max(time.Until(deadline), 0))
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	canceled := false
	for {
		if s.maintenanceMu.TryLock() {
			return canceled, nil
		}
		select {
		case <-ctx.Done():
			s.endModelSwitch()
			return canceled, ctx.Err()
		case <-timer.C:
			canceled = s.finishModelRequest(errModelSwitchPending)
		case <-ticker.C:
		}
	}
}

// finishModelRequest 也用于回合退出：SDK 提前关闭流时可能仍在等待下一个网络分片
func (s *Session) finishModelRequest(cause error) bool {
	s.mu.Lock()
	cancel := s.modelCancel
	s.modelCancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel(cause)
		return true
	}
	return false
}

func (s *Session) endModelSwitch() {
	s.mu.Lock()
	s.switching = false
	s.mu.Unlock()
}

func (a *Agent) resumeAfterModelSwitch(session *Session) {
	session.ClearFailedBatch()
	pending := session.PendingMessages()
	if !hasActionableMessage(pending) || a.stopping.Load() || a.ctx.Err() != nil {
		return
	}
	if session.kind == memory.ConversationKindWorker {
		worker, err := a.memory.GetWorkerSessionByID(a.ctx, uint(session.targetID))
		if err != nil || worker.Status == memory.WorkerStatusFailed || worker.Status == memory.WorkerStatusCancelled {
			return
		}
	}
	zap.L().Info("模型切换后恢复待处理会话", zap.String("session", sessionKey(session.kind, session.targetID)))
	a.scheduleSessionThink(session.kind, session.targetID, latestReceivedAt(pending))
}
