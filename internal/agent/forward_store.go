package agent

import (
	"fmt"
	"sync"
	"time"
)

const (
	// forwardStoreTTL 转发内容的保留时长
	forwardStoreTTL = 30 * time.Minute
	// forwardStoreMaxEntries 转发缓存的最大条目数
	forwardStoreMaxEntries = 128
	// forwardStoreMaxNodes 单份转发保留的最大消息条数
	forwardStoreMaxNodes = 200
)

// forwardNode 转发里的一条消息
type forwardNode struct {
	Sender  string
	Content string
	Nested  string // 嵌套转发的句柄
}

// forwardEntry 一份合并转发的内容
type forwardEntry struct {
	Nodes []forwardNode
	At    time.Time
}

// forwardStore 保存合并转发内容，供 agent 按层翻阅
type forwardStore struct {
	mu      sync.Mutex
	entries map[string]*forwardEntry
	order   []string
	seq     int
}

func newForwardStore() *forwardStore {
	return &forwardStore{entries: map[string]*forwardEntry{}}
}

// add 存下一份转发内容并返回句柄
func (s *forwardStore) add(nodes []forwardNode) string {
	if len(nodes) > forwardStoreMaxNodes {
		nodes = nodes[:forwardStoreMaxNodes]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := fmt.Sprintf("f%d", s.seq)
	s.entries[id] = &forwardEntry{Nodes: nodes, At: time.Now()}
	s.order = append(s.order, id)
	s.pruneLocked()
	return id
}

// get 取一份转发内容，过期的条目按不存在处理
func (s *forwardStore) get(id string) (*forwardEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[id]
	if !ok {
		return nil, false
	}
	if time.Since(entry.At) > forwardStoreTTL {
		return nil, false
	}
	return entry, true
}

// pruneLocked 丢掉过期和超量的条目
func (s *forwardStore) pruneLocked() {
	now := time.Now()
	for len(s.order) > 0 {
		id := s.order[0]
		entry, ok := s.entries[id]
		if ok && now.Sub(entry.At) <= forwardStoreTTL {
			break
		}
		delete(s.entries, id)
		s.order = s.order[1:]
	}
	for len(s.order) > forwardStoreMaxEntries {
		delete(s.entries, s.order[0])
		s.order = s.order[1:]
	}
}
