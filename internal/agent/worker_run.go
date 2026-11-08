package agent

import (
	"context"
	"fmt"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/tools"

	"go.uber.org/zap"
)

// thinkWorker 复用现有 Session/transcript，只把工具集换成 worker 工具
func (a *Agent) thinkWorker(workerID int64) {
	generation := a.acquireGeneration()
	if generation == nil {
		zap.L().Error("worker 无法开始运行", zap.String("session", fmt.Sprintf("worker:%d", workerID)), zap.String("error", "模型未就绪"))
		if err := a.memory.SetWorkerStatus(a.ctx, uint(workerID), memory.WorkerStatusFailed, "模型未就绪", ""); err == nil {
			a.notifyLifeWorkerFailure(a.ctx, uint(workerID), "模型未就绪")
		}
		return
	}
	defer generation.wg.Done()
	id := uint(workerID)
	target, err := a.memory.GetConversationTarget(a.ctx, memory.ConversationKindWorker, workerID)
	if err != nil || target == nil || !target.Active {
		return
	}
	session := a.sessions.get(memory.ConversationKindWorker, workerID)
	session.maintenanceMu.Lock()
	defer session.maintenanceMu.Unlock()
	state, stateErr := newRunState("worker")
	if stateErr != nil {
		zap.L().Error("worker 无法开始运行", zap.String("session", fmt.Sprintf("worker:%d", workerID)), zap.Error(stateErr))
		if err := a.memory.SetWorkerStatus(a.ctx, id, memory.WorkerStatusFailed, stateErr.Error(), ""); err == nil {
			a.notifyLifeWorkerFailure(a.ctx, id, stateErr.Error())
		}
		return
	}

	for {
		pending := session.PendingMessages()
		if !hasActionableMessage(pending) {
			return
		}
		runCtx, cancel := context.WithCancel(a.ctx)
		epoch := a.beginWorkerRun(id, cancel)
		if !a.setWorkerStatusIfCurrent(runCtx, id, epoch, memory.WorkerStatusRunning, "", generation.workerModel.modelName) {
			a.finishWorkerRun(id, epoch)
			cancel()
			return
		}
		ctx := withRunState(runCtx, state)
		ctx = withGeneration(ctx, generation)
		ctx = a.buildWorkerToolContext(ctx, workerID, session)
		tc := tools.GetToolContext(ctx)
		tc.ThinkStartArrivalSeq = maxPendingSeq(pending)

		if state.steps.Remaining() <= 0 {
			a.failWorker(context.WithoutCancel(ctx), id, epoch, session, fmt.Errorf("本轮步数用完"))
			a.finishWorkerRun(id, epoch)
			cancel()
			return
		}
		workerAgent, agentErr := a.newWorkerAgent(ctx, generation, generation.workerTools, state.steps.Remaining())
		if agentErr != nil {
			a.failWorker(context.WithoutCancel(ctx), id, epoch, session, agentErr)
			a.finishWorkerRun(id, epoch)
			cancel()
			return
		}
		messages, err := a.assembleWorkerMessages(ctx, session)
		if err != nil {
			if a.workerRunCurrent(id, epoch) {
				a.failWorker(context.WithoutCancel(ctx), id, epoch, session, err)
			}
			a.finishWorkerRun(id, epoch)
			cancel()
			return
		}
		runErr := a.runAgent(ctx, workerAgent, messages)
		if !a.workerRunCurrent(id, epoch) {
			a.finishWorkerRun(id, epoch)
			cancel()
			return
		}
		if a.workerRunCanceled(id, epoch) {
			a.setWorkerStatusIfCurrent(context.WithoutCancel(ctx), id, epoch, memory.WorkerStatusCancelled, "", generation.workerModel.modelName)
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
		if !hasActionableMessage(session.PendingMessages()) {
			a.setWorkerIdle(context.WithoutCancel(ctx), id, epoch, generation.workerModel.modelName)
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
		// failed/cancelled 不因周期扫描自行重跑，等 life 明确投来新指令
		if worker.Status == memory.WorkerStatusFailed || worker.Status == memory.WorkerStatusCancelled {
			continue
		}
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
