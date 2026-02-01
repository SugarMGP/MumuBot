package utils

import "sync"

// RingBuffer 是一个固定大小的环形缓冲区，支持泛型
// 当缓冲区满时，新元素会覆盖最旧的元素
type RingBuffer[T any] struct {
	data  []T
	head  int // 指向最旧元素的位置
	tail  int // 指向下一个写入位置
	count int // 当前元素数量
	cap   int // 缓冲区容量
	mu    sync.RWMutex
}

// NewRingBuffer 创建一个新的环形缓冲区
func NewRingBuffer[T any](capacity int) *RingBuffer[T] {
	if capacity <= 0 {
		capacity = 64 // 默认容量
	}
	return &RingBuffer[T]{
		data: make([]T, capacity),
		cap:  capacity,
	}
}

// Push 向缓冲区添加一个元素
// 如果缓冲区已满，最旧的元素会被覆盖
func (rb *RingBuffer[T]) Push(item T) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.data[rb.tail] = item
	rb.tail = (rb.tail + 1) % rb.cap

	if rb.count < rb.cap {
		rb.count++
	} else {
		// 缓冲区已满，head 跟着移动
		rb.head = (rb.head + 1) % rb.cap
	}
}

// GetAll 获取缓冲区中的所有元素（按时间顺序，从旧到新）
// 返回的是元素的副本切片
func (rb *RingBuffer[T]) GetAll() []T {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 {
		return nil
	}

	result := make([]T, rb.count)
	for i := 0; i < rb.count; i++ {
		idx := (rb.head + i) % rb.cap
		result[i] = rb.data[idx]
	}
	return result
}

// Len 返回缓冲区中的元素数量
func (rb *RingBuffer[T]) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count
}

// Cap 返回缓冲区容量
func (rb *RingBuffer[T]) Cap() int {
	return rb.cap
}

// Clear 清空缓冲区
func (rb *RingBuffer[T]) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.head = 0
	rb.tail = 0
	rb.count = 0
	// 清除引用以便 GC
	var zero T
	for i := range rb.data {
		rb.data[i] = zero
	}
}

// IsEmpty 检查缓冲区是否为空
func (rb *RingBuffer[T]) IsEmpty() bool {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count == 0
}

// IsFull 检查缓冲区是否已满
func (rb *RingBuffer[T]) IsFull() bool {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count == rb.cap
}

// Peek 查看最新的元素但不移除
func (rb *RingBuffer[T]) Peek() (T, bool) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	var zero T
	if rb.count == 0 {
		return zero, false
	}

	// 最新元素在 tail 前一个位置
	idx := (rb.tail - 1 + rb.cap) % rb.cap
	return rb.data[idx], true
}

// PeekOldest 查看最旧的元素但不移除
func (rb *RingBuffer[T]) PeekOldest() (T, bool) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	var zero T
	if rb.count == 0 {
		return zero, false
	}

	return rb.data[rb.head], true
}

// GetLast 获取最后 n 个元素（从旧到新排序）
func (rb *RingBuffer[T]) GetLast(n int) []T {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 || n <= 0 {
		return nil
	}

	if n > rb.count {
		n = rb.count
	}

	result := make([]T, n)
	// 从最新元素往前数 n 个
	startIdx := (rb.head + rb.count - n + rb.cap) % rb.cap
	for i := 0; i < n; i++ {
		idx := (startIdx + i) % rb.cap
		result[i] = rb.data[idx]
	}
	return result
}
