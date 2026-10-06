package agent

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// commitWorkerIdleTTL 是提交协程的空闲回收时间。事件解析有 30 秒上限，
// 空闲超过该时间说明该会话没有在途事件，可以安全回收并只保留水位
const commitWorkerIdleTTL = 10 * time.Minute

type queuedCommit[T any] struct {
	seq  uint64
	item T
}

// commitQueueSet 按会话维护到达序号提交队列；空闲后回收协程并记住下一条序号，
// 既保证落库、入缓冲和调度按到达顺序执行，也不为长期静默会话保留常驻协程
type commitQueueSet[T any] struct {
	mu       sync.Mutex
	queues   map[int64]chan queuedCommit[T]
	inflight map[int64]int
	next     map[int64]uint64
	wg       sync.WaitGroup
	handle   func(T)
	logField func(int64) zap.Field
}

func newCommitQueueSet[T any](handle func(T), logField func(int64) zap.Field) *commitQueueSet[T] {
	return &commitQueueSet[T]{
		queues:   make(map[int64]chan queuedCommit[T]),
		inflight: make(map[int64]int),
		next:     make(map[int64]uint64),
		handle:   handle,
		logField: logField,
	}
}

// enqueue 投递一个事件；队列不存在时按记录的水位重建，在途发送者会阻止队列被回收
func (s *commitQueueSet[T]) enqueue(targetID int64, seq uint64, item T) {
	s.mu.Lock()
	queue := s.queues[targetID]
	if queue == nil {
		queue = make(chan queuedCommit[T], commitQueueSize)
		s.queues[targetID] = queue
		next := s.next[targetID]
		delete(s.next, targetID)
		s.wg.Add(1)
		go s.worker(targetID, queue, next)
	}
	s.inflight[targetID]++
	s.mu.Unlock()

	queue <- queuedCommit[T]{seq: seq, item: item}

	s.mu.Lock()
	s.inflight[targetID]--
	if s.inflight[targetID] == 0 {
		delete(s.inflight, targetID)
	}
	s.mu.Unlock()
}

func (s *commitQueueSet[T]) worker(targetID int64, queue chan queuedCommit[T], next uint64) {
	defer s.wg.Done()
	reorder := newReorderBuffer[T](s.logField, next)
	idle := time.NewTimer(commitWorkerIdleTTL)
	defer idle.Stop()
	for {
		select {
		case item, ok := <-queue:
			if !ok {
				reorder.warnLeftover()
				return
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(commitWorkerIdleTTL)
			reorder.push(item.seq, targetID, item.item, s.handle)
		case <-idle.C:
			if s.reap(targetID, queue, reorder) {
				reorder.warnLeftover()
				return
			}
			idle.Reset(commitWorkerIdleTTL)
		}
	}
}

// reap 在队列空闲且没有在途发送者时移除队列并记录水位；等待项未提交时不能回收
func (s *commitQueueSet[T]) reap(targetID int64, queue chan queuedCommit[T], reorder *reorderBuffer[T]) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queues[targetID] != queue || s.inflight[targetID] != 0 || len(queue) != 0 || len(reorder.pending) != 0 {
		return false
	}
	delete(s.queues, targetID)
	s.next[targetID] = reorder.next
	return true
}

// close 关闭全部队列并等待提交协程退出
func (s *commitQueueSet[T]) close() {
	s.mu.Lock()
	for targetID, queue := range s.queues {
		close(queue)
		delete(s.queues, targetID)
	}
	s.mu.Unlock()
	s.wg.Wait()
}
