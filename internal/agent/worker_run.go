package agent

import (
	"context"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/tools"

	"go.uber.org/zap"
)

// thinkWorker 复用现有 Session/transcript，只把工具集换成 worker 工具
func (a *Agent) thinkWorker(workerID int64) {
	generation := a.acquireGeneration()
	if generation == nil {
		_ = a.memory.SetWorkerStatus(a.ctx, uint(workerID), memory.WorkerStatusFailed, "模型未就绪", "")
		return
	}
	defer generation.wg.Done()
	id := uint(workerID)
	session := a.sessions.get(memory.ConversationKindWorker, workerID)
	session.maintenanceMu.Lock()
	defer session.maintenanceMu.Unlock()

	for {
		pending := session.PendingMessages()
		if !hasActionableMessage(pending) {
			return
		}
		runCtx, cancel := context.WithCancel(a.ctx)
		epoch := a.beginWorkerRun(id, cancel)
		if !a.setWorkerStatusIfCurrent(runCtx, id, epoch, memory.WorkerStatusRunning, "", generation.modelName) {
			a.finishWorkerRun(id, epoch)
			cancel()
			return
		}
		ctx := withGeneration(runCtx, generation)
		ctx = a.buildWorkerToolContext(ctx, workerID, session)
		tc := tools.GetToolContext(ctx)
		tc.ThinkStartArrivalSeq = maxPendingSeq(pending)

		messages, err := a.assembleWorkerMessages(ctx, session)
		if err != nil {
			if a.workerRunCurrent(id, epoch) {
				a.failWorker(context.WithoutCancel(ctx), id, epoch, session, err)
			}
			a.finishWorkerRun(id, epoch)
			cancel()
			return
		}
		runErr := a.runAgent(ctx, generation.workerAgent, messages)
		if !a.workerRunCurrent(id, epoch) {
			a.finishWorkerRun(id, epoch)
			cancel()
			return
		}
		if runErr != nil {
			a.failWorker(context.WithoutCancel(ctx), id, epoch, session, runErr)
			a.finishWorkerRun(id, epoch)
			cancel()
			return
		}
		if len(session.PendingMessages()) == 0 {
			a.setWorkerIdle(context.WithoutCancel(ctx), id, epoch, generation.modelName)
			a.finishWorkerRun(id, epoch)
			cancel()
			return
		}
		a.finishWorkerRun(id, epoch)
		cancel()
	}
}

func (a *Agent) scanWorkerThinks() {
	workers, err := a.memory.ListWorkerSessions(a.ctx)
	if err != nil {
		zap.L().Warn("读取 worker 列表失败", zap.Error(err))
		return
	}
	for _, worker := range workers {
		target, err := a.memory.GetConversationTarget(a.ctx, memory.ConversationKindWorker, int64(worker.ID))
		if err != nil || !target.Active {
			continue
		}
		session := a.sessions.get(memory.ConversationKindWorker, int64(worker.ID))
		if !hasActionableMessage(session.PendingMessages()) {
			continue
		}
		a.scheduleWorkerThink(int64(worker.ID))
	}
}
