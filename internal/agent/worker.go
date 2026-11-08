package agent

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/tools"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

func (a *Agent) updateWorkerTitle(ctx context.Context, workerID uint, title string) error {
	return a.memory.UpdateWorkerTitle(ctx, workerID, title)
}

type workerRun struct {
	epoch    uint64
	cancel   context.CancelFunc
	canceled bool
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

func (a *Agent) workerRunCanceled(workerID uint, epoch uint64) bool {
	a.workerCancelMu.Lock()
	defer a.workerCancelMu.Unlock()
	run, ok := a.workerRuns[workerID]
	return ok && run.epoch == epoch && run.canceled
}

// cancelWorkerTurn 只中断当前执行上下文，不删除槽位、历史或文件
func (a *Agent) cancelWorkerTurn(ctx context.Context, workerID uint, reason string) (bool, error) {
	if workerID == 0 {
		return false, fmt.Errorf("worker 编号无效")
	}
	if _, err := a.memory.GetWorkerSessionByID(ctx, workerID); err != nil {
		return false, fmt.Errorf("worker 会话不存在")
	}
	a.workerCancelMu.Lock()
	run, ok := a.workerRuns[workerID]
	if !ok {
		a.workerCancelMu.Unlock()
		return false, nil
	}
	run.canceled = true
	a.workerRuns[workerID] = run
	cancel := run.cancel
	a.workerCancelMu.Unlock()
	zap.L().Info("取消 worker 当前回合", zap.Uint("worker_id", workerID), zap.String("reason", reason))
	if cancel != nil {
		cancel()
	}
	return true, nil
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
		ResolvePendingFile:     a.resolvePendingFileURL,
		Session:                session,
		RunID:                  newRunID(),
		MemoryReadVersions:     map[uint]uint64{},
		SessionMessageCallback: a.deliverSessionMessage,
		SessionRunningCallback: a.sessionRunning,
		BashCallback:           a.workerBashCallback(id),
		PersonalFiles:          workerFileAdapter{exec: a.workerExec, refs: a.memory.RefStore()},
		SnapshotFiles:          workerFileAdapter{exec: a.workerExec, refs: a.memory.RefStore()},
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

// failWorker 更新失败状态、在 worker transcript 留一条记录，并把程序已知的故障回报 life
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
	zap.L().Error("worker 运行失败", zap.String("session", fmt.Sprintf("worker:%d", workerID)), zap.Error(err))
	a.notifyLifeWorkerFailure(ctx, workerID, message)
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

// notifyLifeWorkerFailure 让 life 知道 worker 在哪一步失败；投递失败只记日志，
// 失败状态仍可从会话目录和 worker 历史查到，不引入无限重试
func (a *Agent) notifyLifeWorkerFailure(ctx context.Context, workerID uint, message string) {
	text := fmt.Sprintf("程序通知：worker:%d（%s）这一轮运行失败。真实失败：%s", workerID, a.workerTitle(ctx, workerID), message)
	if _, err := a.deliverSessionMessage(ctx, memory.ConversationKindWorker, int64(workerID),
		memory.ConversationKindLife, memory.LifeTargetID, text, true); err != nil {
		zap.L().Warn("回报 worker 故障给 life 失败", zap.Uint("worker_id", workerID), zap.Error(err))
	}
}

// notifyLifeWorkerCancelled 让 life 知道缩池取消，不用之后主动查目录才发现
func (a *Agent) notifyLifeWorkerCancelled(ctx context.Context, workerID uint) {
	text := fmt.Sprintf("程序通知：worker:%d（%s）已从池中停用，正在进行的任务被取消。", workerID, a.workerTitle(ctx, workerID))
	if _, err := a.deliverSessionMessage(ctx, memory.ConversationKindWorker, int64(workerID),
		memory.ConversationKindLife, memory.LifeTargetID, text, true); err != nil {
		zap.L().Warn("回报 worker 停用给 life 失败", zap.Uint("worker_id", workerID), zap.Error(err))
	}
}

// workerTitle 读取 worker 当前标题，只用于程序通知的可读来源
func (a *Agent) workerTitle(ctx context.Context, workerID uint) string {
	if row, err := a.memory.GetWorkerSessionByID(ctx, workerID); err == nil {
		return strings.TrimSpace(row.Title)
	}
	return ""
}

func (a *Agent) setWorkerIdle(ctx context.Context, workerID uint, epoch uint64, modelName string) bool {
	return a.setWorkerStatusIfCurrent(ctx, workerID, epoch, memory.WorkerStatusIdle, "", modelName)
}
