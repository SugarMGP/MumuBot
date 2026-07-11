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

// IsEmpty 检查缓冲区是否为空
func (rb *RingBuffer[T]) IsEmpty() bool {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count == 0
}
