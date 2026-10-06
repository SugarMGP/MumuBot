package agent

import (
	"context"
	"sync"

	"go.uber.org/zap"
)

// GroupThinkConcurrency 管理群聊思考的并发、排队和每群单任务执行
type GroupThinkConcurrency struct {
	ctx            context.Context
	cancel         context.CancelFunc
	maxConcurrency int
	currentRunning int
	queue          []*GroupThinkTask
	queued         map[int64]*GroupThinkTask
	running        map[int64]bool
	mu             sync.Mutex
	wg             sync.WaitGroup

	handler func(groupID int64, probabilityPassed bool) // 执行函数
}

// GroupThinkTask 群聊思考任务
type GroupThinkTask struct {
	GroupID           int64
	ProbabilityPassed bool
}

// NewGroupThinkConcurrency 创建群聊思考并发管理器
func NewGroupThinkConcurrency(parent context.Context, max int, h func(groupID int64, probabilityPassed bool)) *GroupThinkConcurrency {
	ctx, cancel := context.WithCancel(parent)
	return &GroupThinkConcurrency{
		ctx:            ctx,
		cancel:         cancel,
		maxConcurrency: max,
		queued:         make(map[int64]*GroupThinkTask),
		running:        make(map[int64]bool),
		handler:        h,
	}
}

func (m *GroupThinkConcurrency) IsRunning(groupID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running[groupID]
}

// Submit 提交任务
func (m *GroupThinkConcurrency) Submit(groupID int64, probabilityPassed bool) {
	if err := m.ctx.Err(); err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ctx.Err(); err != nil {
		return
	}

	if m.running[groupID] {
		zap.L().Debug("任务正在执行，忽略新触发", zap.Int64("group_id", groupID))
		return
	}
	if task := m.queued[groupID]; task != nil {
		task.ProbabilityPassed = task.ProbabilityPassed || probabilityPassed
		zap.L().Debug("任务已在队列中，跳过", zap.Int64("group_id", groupID))
		return
	}

	// 如果设置了最大并发数，且当前运行数已满，则入队
	if m.maxConcurrency > 0 && m.currentRunning >= m.maxConcurrency {
		task := &GroupThinkTask{GroupID: groupID, ProbabilityPassed: probabilityPassed}
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
	go m.execute(&GroupThinkTask{GroupID: groupID, ProbabilityPassed: probabilityPassed})
}

// execute 执行任务
func (m *GroupThinkConcurrency) execute(task *GroupThinkTask) {
	defer m.wg.Done()
	defer m.finish(task.GroupID)
	if err := m.ctx.Err(); err != nil {
		return
	}
	m.handler(task.GroupID, task.ProbabilityPassed)
}

func (m *GroupThinkConcurrency) finish(groupID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.running, groupID)
	m.currentRunning--
	if m.currentRunning < 0 {
		m.currentRunning = 0
	}

	for len(m.queue) > 0 && m.ctx.Err() == nil && (m.maxConcurrency <= 0 || m.currentRunning < m.maxConcurrency) {
		task := m.queue[0]
		m.queue = m.queue[1:]
		delete(m.queued, task.GroupID)

		m.currentRunning++
		m.running[task.GroupID] = true
		m.wg.Add(1)
		go m.execute(task)
		zap.L().Debug("从队列调度任务执行", zap.Int64("group_id", task.GroupID))
	}
}

// Close 停止调度并等待已启动任务退出
func (m *GroupThinkConcurrency) Close() {
	if m.cancel != nil {
		m.cancel()
	}

	m.mu.Lock()
	m.queue = nil
	m.queued = make(map[int64]*GroupThinkTask)
	m.mu.Unlock()

	m.wg.Wait()
}
