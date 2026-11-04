package agent

import (
	"context"
	"unicode/utf8"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/tools"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
)

func (a *Agent) updateWorkerTitle(ctx context.Context, workerID uint, title string) error {
	return a.memory.UpdateWorkerTitle(ctx, workerID, title)
}

type workerRun struct {
	epoch  uint64
	cancel context.CancelFunc
}

func (a *Agent) beginWorkerRun(workerID uint, cancel context.CancelFunc) uint64 {
	a.workerCancelMu.Lock()
	defer a.workerCancelMu.Unlock()
	a.workerEpochs[workerID]++
	epoch := a.workerEpochs[workerID]
	a.workerRuns[workerID] = workerRun{epoch: epoch, cancel: cancel}
	return epoch
}

func (a *Agent) workerRunCurrent(workerID uint, epoch uint64) bool {
	a.workerCancelMu.Lock()
	defer a.workerCancelMu.Unlock()
	run, ok := a.workerRuns[workerID]
	return ok && run.epoch == epoch
}

func (a *Agent) finishWorkerRun(workerID uint, epoch uint64) bool {
	a.workerCancelMu.Lock()
	defer a.workerCancelMu.Unlock()
	run, ok := a.workerRuns[workerID]
	if !ok || run.epoch != epoch {
		return false
	}
	delete(a.workerRuns, workerID)
	return true
}

// stopWorkerRun 让当前 run 失效并取消；旧 run 之后不能再写状态或失败记录
func (a *Agent) stopWorkerRun(workerID uint) {
	a.workerCancelMu.Lock()
	run, ok := a.workerRuns[workerID]
	delete(a.workerRuns, workerID)
	a.workerEpochs[workerID]++
	a.workerCancelMu.Unlock()
	if ok && run.cancel != nil {
		run.cancel()
	}
}

// invalidateWorkerRun 无运行也推进 epoch，防止已经结束但还没写状态的旧 run 复活
func (a *Agent) invalidateWorkerRun(workerID uint) {
	a.workerCancelMu.Lock()
	delete(a.workerRuns, workerID)
	a.workerEpochs[workerID]++
	a.workerCancelMu.Unlock()
}

func (a *Agent) setWorkerStatusIfCurrent(ctx context.Context, workerID uint, epoch uint64, status, lastError, modelName string) bool {
	a.workerCancelMu.Lock()
	defer a.workerCancelMu.Unlock()
	run, ok := a.workerRuns[workerID]
	if !ok || run.epoch != epoch {
		return false
	}
	if err := a.memory.SetWorkerStatus(ctx, workerID, status, lastError, modelName); err != nil {
		return false
	}
	return true
}

func (a *Agent) cancelWorkerRuns() {
	a.workerCancelMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(a.workerRuns))
	for id, run := range a.workerRuns {
		delete(a.workerRuns, id)
		a.workerEpochs[id]++
		if run.cancel != nil {
			cancels = append(cancels, run.cancel)
		}
	}
	a.workerCancelMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (a *Agent) buildWorkerToolContext(ctx context.Context, workerID int64, session *Session) context.Context {
	id := uint(workerID)
	tc := &tools.ToolContext{
		ConversationKind:       memory.ConversationKindWorker,
		TargetID:               workerID,
		MemoryMgr:              a.memory.WithContext(ctx),
		Session:                session,
		RunID:                  newRunID(),
		MemoryReadVersions:     map[uint]uint64{},
		SessionMessageCallback: a.deliverSessionMessage,
		BashCallback:           a.workerBashCallback(id),
		UpdateWorkerTitleCallback: func(callCtx context.Context, title string) error {
			return a.updateWorkerTitle(callCtx, id, title)
		},
	}
	return tools.WithToolContext(ctx, tc)
}

func (a *Agent) scheduleWorkerThink(workerID int64) {
	if a.workerConcurrency == nil || a.stopping.Load() || a.ctx.Err() != nil {
		return
	}
	a.workerConcurrency.Submit(workerID)
}

// failWorker 更新失败状态，并在 worker transcript 留一条内部记录；不通知原 session
func (a *Agent) failWorker(ctx context.Context, workerID uint, epoch uint64, session *Session, err error) {
	message := "未知错误"
	if err != nil {
		message = err.Error()
	}
	if utf8.RuneCountInString(message) > 1000 {
		runes := []rune(message)
		message = string(runes[:1000]) + "…"
	}
	if !a.setWorkerStatusIfCurrent(ctx, workerID, epoch, memory.WorkerStatusFailed, message, "") {
		return
	}
	if session == nil {
		return
	}
	note := schema.UserAgenticMessage(systemNote("worker 失败", message))
	note.Extra = map[string]any{"kind": "system_note"}
	payload, marshalErr := sonic.Marshal(note)
	if marshalErr != nil {
		return
	}
	entry := &memory.TranscriptEntry{
		ConversationKind: memory.ConversationKindWorker, TargetID: int64(workerID),
		EntryKind: "system_note", Payload: payload,
	}
	if appendErr := a.memory.AppendTranscript(ctx, entry); appendErr == nil {
		session.AppendSeenMessage(note, entry.ID, 0)
	}
}

func (a *Agent) setWorkerIdle(ctx context.Context, workerID uint, epoch uint64, modelName string) bool {
	return a.setWorkerStatusIfCurrent(ctx, workerID, epoch, memory.WorkerStatusIdle, "", modelName)
}
