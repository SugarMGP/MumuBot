package agent

import (
	"fmt"
	"sort"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"

	"go.uber.org/zap"
)

// SyncWorkers 把 worker 池对齐到后台配置的数量；减少时停用并保留 transcript，增加时优先复用停用 worker
func (a *Agent) SyncWorkers(desired int) error {
	if desired < 0 || desired > config.MaxWorkerCount {
		return fmt.Errorf("worker 数量必须在 0 到 %d 之间", config.MaxWorkerCount)
	}
	a.workerPoolMu.Lock()
	defer func() {
		a.workerPoolMu.Unlock()
		a.scanWorkerThinks()
	}()

	workers, err := a.memory.ListWorkerSessions(a.ctx)
	if err != nil {
		return err
	}
	type poolEntry struct {
		worker memory.WorkerSession
		active bool
	}
	entries := make([]poolEntry, 0, len(workers))
	for _, worker := range workers {
		target, err := a.memory.GetConversationTarget(a.ctx, memory.ConversationKindWorker, int64(worker.ID))
		if err != nil {
			return err
		}
		entries = append(entries, poolEntry{worker: worker, active: target.Active})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].worker.ID < entries[j].worker.ID })

	active := make([]poolEntry, 0, len(entries))
	inactive := make([]poolEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.active {
			active = append(active, entry)
		} else {
			inactive = append(inactive, entry)
		}
	}

	for len(active) > desired {
		entry := active[len(active)-1]
		a.stopWorkerRun(entry.worker.ID)
		if err := a.memory.SetWorkerTargetActive(a.ctx, entry.worker.ID, false); err != nil {
			return err
		}
		if err := a.memory.SetWorkerStatus(a.ctx, entry.worker.ID, memory.WorkerStatusCancelled, "worker 数量已调整", entry.worker.ModelName); err != nil {
			return err
		}
		zap.L().Info("worker 已从池中停用", zap.Uint("worker_id", entry.worker.ID))
		a.notifyLifeWorkerCancelled(a.ctx, entry.worker.ID)
		active = active[:len(active)-1]
		inactive = append(inactive, entry)
	}

	for len(active) < desired && len(inactive) > 0 {
		entry := inactive[0]
		inactive = inactive[1:]
		a.invalidateWorkerRun(entry.worker.ID)
		if err := a.memory.SetWorkerTargetActive(a.ctx, entry.worker.ID, true); err != nil {
			return err
		}
		active = append(active, entry)
		zap.L().Info("worker 已重新加入池", zap.Uint("worker_id", entry.worker.ID))
	}

	for len(active) < desired {
		row, err := a.memory.CreateWorkerSession(a.ctx, "空闲")
		if err != nil {
			return err
		}
		active = append(active, poolEntry{worker: *row, active: true})
		zap.L().Info("worker 已加入池", zap.Uint("worker_id", row.ID))
	}
	workerThinkMax := desired
	if workerThinkMax <= 0 {
		workerThinkMax = 1
	}
	a.workerConcurrency.SetMax(workerThinkMax)
	return nil
}
