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
	inQueue        map[int64]bool // 快速去重（群组ID -> 是否在队列中）
	mu             sync.Mutex
	wg             sync.WaitGroup

	handler func(groupID int64, isMention bool) // 执行函数
}

// ThinkTask 思考任务
type ThinkTask struct {
	GroupID   int64
	IsMention bool
}

// NewConcurrencyManager 创建并发管理器
func NewConcurrencyManager(parent context.Context, max int, h func(groupID int64, isMention bool)) *ConcurrencyManager {
	ctx, cancel := context.WithCancel(parent)
	return &ConcurrencyManager{
		ctx:            ctx,
		cancel:         cancel,
		maxConcurrency: max,
		inQueue:        make(map[int64]bool),
		handler:        h,
	}
}

// Submit 提交任务
func (m *ConcurrencyManager) Submit(groupID int64, isMention bool) {
	if err := m.ctx.Err(); err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.inQueue[groupID] {
		zap.L().Debug("任务已在队列中，跳过", zap.Int64("group_id", groupID))
		return
	}

	// 如果设置了最大并发数，且当前运行数已满，则入队
	if m.maxConcurrency > 0 && m.currentRunning >= m.maxConcurrency {
		m.queue = append(m.queue, &ThinkTask{
			GroupID:   groupID,
			IsMention: isMention,
		})
		m.inQueue[groupID] = true
		zap.L().Debug("并发已满，任务进入队列",
			zap.Int64("group_id", groupID),
			zap.Int("current", m.currentRunning),
			zap.Int("queue_len", len(m.queue)))
		return
	}

	m.currentRunning++
	m.wg.Add(1)
	go m.execute(groupID, isMention)
}

// execute 执行任务
func (m *ConcurrencyManager) execute(groupID int64, isMention bool) {
	defer m.wg.Done()
	defer m.Finish()
	if err := m.ctx.Err(); err != nil {
		return
	}
	m.handler(groupID, isMention)
}

// Finish 任务完成回调
func (m *ConcurrencyManager) Finish() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.currentRunning--
	if m.currentRunning < 0 {
		m.currentRunning = 0
	}

	// 调度下一个任务
	if len(m.queue) > 0 && m.ctx.Err() == nil {
		// 取出队首任务
		task := m.queue[0]
		m.queue = m.queue[1:]
		delete(m.inQueue, task.GroupID)

		// 立即启动
		m.currentRunning++
		m.wg.Add(1)
		go m.execute(task.GroupID, task.IsMention)
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
	m.inQueue = make(map[int64]bool)
	m.mu.Unlock()

	m.wg.Wait()
}
