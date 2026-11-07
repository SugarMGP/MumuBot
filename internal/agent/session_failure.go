package agent

import "github.com/cloudwego/eino/schema"

// MarkFailedThrough 记录已经失败到的实际请求边界
func (s *Session) MarkFailedThrough(order uint64) {
	if s == nil || order == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if order > s.failedBatchThrough {
		s.failedBatchThrough = order
	}
}

// MarkFailedBatch 记录已经失败的一批本地输入，直到出现更新的可行动输入才重试
func (s *Session) MarkFailedBatch(pending []*sessionUser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if through := maxActionableOrder(pending); through > s.failedBatchThrough {
		s.failedBatchThrough = through
	}
}

// FailedBatchAllowsRetry 判断是否存在比上次失败批次更新的可行动输入
func (s *Session) FailedBatchAllowsRetry(pending []*sessionUser) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	through := maxActionableOrder(pending)
	return through > s.failedBatchThrough
}

// RecordRequestBoundary 记录真正进入模型请求的最后一个可行动输入
func (s *Session) RecordRequestBoundary(messages []*schema.AgenticMessage) {
	included := make(map[*schema.AgenticMessage]bool, len(messages))
	for _, message := range messages {
		included[message] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var pending []*sessionUser
	for _, user := range s.pending {
		if user != nil && included[user.Msg] {
			pending = append(pending, user)
		}
	}
	order := maxActionableOrder(pending)
	if order > s.requestActionableThrough {
		s.requestActionableThrough = order
	}
}

// MarkFailedAfterRun 请求已发出时使用实际请求边界；请求前失败才退回入口快照
func (s *Session) MarkFailedAfterRun(pending []*sessionUser, requestAttempted bool) {
	if s == nil {
		return
	}
	if requestAttempted {
		s.mu.Lock()
		order := s.requestActionableThrough
		s.mu.Unlock()
		s.MarkFailedThrough(order)
		return
	}
	s.MarkFailedBatch(pending)
}

// ClearFailedBatch 在模型请求成功后清除失败批次标记
func (s *Session) ClearFailedBatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failedBatchThrough = 0
}

// NeedsCompactionBeforeModel 表示上一轮压缩失败，下一次模型请求前必须先压缩
func (s *Session) NeedsCompactionBeforeModel() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compactionBeforeModel
}

func maxActionableOrder(pending []*sessionUser) uint64 {
	var through uint64
	for _, user := range pending {
		if user == nil || user.IsMaintenanceHint || user.IsObservation {
			continue
		}
		if user.MessageID != 0 || (user.IsAgentMessage && user.Wake) {
			if user.Order > through {
				through = user.Order
			}
		}
	}
	return through
}
