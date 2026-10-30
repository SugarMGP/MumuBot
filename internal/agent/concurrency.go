package agent

import (
	"context"
	"sync"

	"go.uber.org/zap"
)

// ThinkConcurrency 管理一组思考任务的并发、排队和每个目标单任务执行
type ThinkConcurrency struct {
	ctx            context.Context
	cancel         context.CancelFunc
	maxConcurrency int
	currentRunning int
	queue          []int64
	queued         map[int64]bool
	running        map[int64]bool
	mu             sync.Mutex
	wg             sync.WaitGroup

	handler func(targetID int64)
}

// NewThinkConcurrency 创建思考并发管理器
func NewThinkConcurrency(parent context.Context, max int, h func(targetID int64)) *ThinkConcurrency {
	ctx, cancel := context.WithCancel(parent)
	return &ThinkConcurrency{
		ctx:            ctx,
		cancel:         cancel,
		maxConcurrency: max,
		queued:         make(map[int64]bool),
		running:        make(map[int64]bool),
		handler:        h,
	}
}

// IsRunning 判断目标是否正在执行
func (m *ThinkConcurrency) IsRunning(targetID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running[targetID]
}

// SetMax 热更新并发上限；调大时立即调度队列
func (m *ThinkConcurrency) SetMax(max int) {
	if max < 0 {
		max = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxConcurrency = max
	m.drainLocked()
}

// Submit 提交任务
func (m *ThinkConcurrency) Submit(targetID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ctx.Err(); err != nil {
		return
	}
	if m.running[targetID] || m.queued[targetID] {
		return
	}
	if m.maxConcurrency > 0 && m.currentRunning >= m.maxConcurrency {
		m.queue = append(m.queue, targetID)
		m.queued[targetID] = true
		zap.L().Debug("思考并发已满，任务进入队列",
			zap.Int64("target_id", targetID),
			zap.Int("current", m.currentRunning),
			zap.Int("queue_len", len(m.queue)))
		return
	}
	m.startLocked(targetID)
}

func (m *ThinkConcurrency) startLocked(targetID int64) {
	delete(m.queued, targetID)
	m.currentRunning++
	m.running[targetID] = true
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer m.finish(targetID)
		if m.ctx.Err() != nil {
			return
		}
		m.handler(targetID)
	}()
}

func (m *ThinkConcurrency) finish(targetID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.running, targetID)
	m.currentRunning--
	if m.currentRunning < 0 {
		m.currentRunning = 0
	}
	m.drainLocked()
}

func (m *ThinkConcurrency) drainLocked() {
	for len(m.queue) > 0 && m.ctx.Err() == nil && (m.maxConcurrency <= 0 || m.currentRunning < m.maxConcurrency) {
		targetID := m.queue[0]
		m.queue = m.queue[1:]
		m.startLocked(targetID)
	}
}

// Close 停止调度并等待已启动任务退出
func (m *ThinkConcurrency) Close() {
	m.cancel()
	m.mu.Lock()
	m.queue = nil
	m.queued = make(map[int64]bool)
	m.mu.Unlock()
	m.wg.Wait()
}
