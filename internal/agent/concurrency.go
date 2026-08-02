package agent

import (
	"context"
	"sync"

	"go.uber.org/zap"
)

// ConcurrencyManager 并发管理器
type ConcurrencyManager struct {
	ctx            context.Context
	cancel         context.CancelFunc
	maxConcurrency int
	currentRunning int
	queue          []*ThinkTask
	queued         map[int64]*ThinkTask
	running        map[int64]bool
	rerun          map[int64]*ThinkTask
	mu             sync.Mutex
	wg             sync.WaitGroup

	handler func(groupID int64) // 执行函数
}

// ThinkTask 思考任务
type ThinkTask struct {
	GroupID int64
}

// NewConcurrencyManager 创建并发管理器
func NewConcurrencyManager(parent context.Context, max int, h func(groupID int64)) *ConcurrencyManager {
	ctx, cancel := context.WithCancel(parent)
	return &ConcurrencyManager{
		ctx:            ctx,
		cancel:         cancel,
		maxConcurrency: max,
		queued:         make(map[int64]*ThinkTask),
		running:        make(map[int64]bool),
		rerun:          make(map[int64]*ThinkTask),
		handler:        h,
	}
}

// Submit 提交任务
func (m *ConcurrencyManager) Submit(groupID int64) {
	if err := m.ctx.Err(); err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ctx.Err(); err != nil {
		return
	}

	if m.rerun[groupID] != nil {
		return
	}
	if m.running[groupID] {
		m.rerun[groupID] = &ThinkTask{GroupID: groupID}
		return
	}
	if m.queued[groupID] != nil {
		zap.L().Debug("任务已在队列中，跳过", zap.Int64("group_id", groupID))
		return
	}

	// 如果设置了最大并发数，且当前运行数已满，则入队
	if m.maxConcurrency > 0 && m.currentRunning >= m.maxConcurrency {
		task := &ThinkTask{GroupID: groupID}
		m.queue = append(m.queue, task)
		m.queued[groupID] = task
		zap.L().Debug("并发已满，任务进入队列",
			zap.Int64("group_id", groupID),
			zap.Int("current", m.currentRunning),
			zap.Int("queue_len", len(m.queue)))
		return
	}

	m.currentRunning++
	m.running[groupID] = true
	m.wg.Add(1)
	go m.execute(groupID)
}

// execute 执行任务
func (m *ConcurrencyManager) execute(groupID int64) {
	defer m.wg.Done()
	defer m.finish(groupID)
	if err := m.ctx.Err(); err != nil {
		return
	}
	m.handler(groupID)
}

func (m *ConcurrencyManager) finish(groupID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.running, groupID)
	m.currentRunning--
	if m.currentRunning < 0 {
		m.currentRunning = 0
	}

	if task := m.rerun[groupID]; task != nil && m.ctx.Err() == nil {
		delete(m.rerun, groupID)
		m.queue = append(m.queue, task)
		m.queued[groupID] = task
	}

	for len(m.queue) > 0 && m.ctx.Err() == nil && (m.maxConcurrency <= 0 || m.currentRunning < m.maxConcurrency) {
		task := m.queue[0]
		m.queue = m.queue[1:]
		delete(m.queued, task.GroupID)

		m.currentRunning++
		m.running[task.GroupID] = true
		m.wg.Add(1)
		go m.execute(task.GroupID)
		zap.L().Debug("从队列调度任务执行", zap.Int64("group_id", task.GroupID))
	}
}

// Close 停止调度并等待已启动任务退出。
func (m *ConcurrencyManager) Close() {
	if m.cancel != nil {
		m.cancel()
	}

	m.mu.Lock()
	m.queue = nil
	m.queued = make(map[int64]*ThinkTask)
	m.rerun = make(map[int64]*ThinkTask)
	m.mu.Unlock()

	m.wg.Wait()
}
