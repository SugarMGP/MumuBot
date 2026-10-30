package agent

import (
	"fmt"
	"time"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/schema"
)

type compactionCandidate struct {
	Messages            []*schema.AgenticMessage
	MessageLogIDs       []uint
	CoverageRefs        []string
	ThroughVisibleSeq   uint64
	ThroughMessageLogID uint
}

// BuildCompactionCandidate 选出最老的闭合交互段，保留最近尾部
func (s *Session) BuildCompactionCandidate(window int, tailRatio float64) (*compactionCandidate, error) {
	s.mu.Lock()
	entries := append([]sessionEntry(nil), s.seen...)
	s.mu.Unlock()
	segments := splitSegments(entries)
	if len(segments) < 2 {
		return nil, fmt.Errorf("没有可安全压缩的完整旧交互")
	}
	tailBudget := int(float64(window) * tailRatio)
	if tailBudget < 0 {
		tailBudget = 0
	}
	cut := len(segments)
	tailCost := 0
	for i := len(segments) - 1; i >= 0; i-- {
		cost := segmentTokens(segments[i])
		if cut < len(segments) && tailCost+cost > tailBudget {
			break
		}
		tailCost += cost
		cut = i
	}
	for i, segment := range segments {
		unvisible := false
		for _, entry := range segment.items {
			if entry.visibleSeq == 0 {
				unvisible = true
				break
			}
		}
		if unvisible && i < cut {
			cut = i
			break
		}
	}
	if cut == 0 {
		return nil, fmt.Errorf("最近工作集尚未进入模型上下文，不能压缩")
	}
	candidate := &compactionCandidate{}
	for _, segment := range segments[:cut] {
		for _, entry := range segment.items {
			if entry.msg != nil {
				candidate.Messages = append(candidate.Messages, entry.msg)
			}
			if entry.visibleSeq > candidate.ThroughVisibleSeq {
				candidate.ThroughVisibleSeq = entry.visibleSeq
			}
			if entry.user != nil {
				if entry.user.LogID > candidate.ThroughMessageLogID {
					candidate.ThroughMessageLogID = entry.user.LogID
				}
				if entry.user.LogID != 0 {
					candidate.MessageLogIDs = append(candidate.MessageLogIDs, entry.user.LogID)
					candidate.CoverageRefs = append(candidate.CoverageRefs, entry.user.Ref)
				}
			}
		}
	}
	if len(candidate.Messages) == 0 || candidate.ThroughVisibleSeq == 0 || candidate.ThroughMessageLogID == 0 {
		return nil, fmt.Errorf("没有可安全压缩的旧交互")
	}
	return candidate, nil
}

// RetainedAfterVisible 返回检查点之后的完整工作集消息
func (s *Session) RetainedAfterVisible(through uint64) []*schema.AgenticMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := make([]*schema.AgenticMessage, 0)
	for _, entry := range s.seen {
		if entry.msg != nil && (entry.visibleSeq == 0 || entry.visibleSeq > through) {
			messages = append(messages, entry.msg)
		}
	}
	for _, user := range s.pending {
		if user.Msg != nil {
			messages = append(messages, user.Msg)
		}
	}
	return messages
}

// ApplyCompaction 丢弃检查点覆盖的工作集，只保留完整尾部
func (s *Session) ApplyCompaction(checkpoint *memory.CompactionCheckpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if checkpoint == nil || checkpoint.ThroughVisibleSeq == nil || *checkpoint.ThroughVisibleSeq == 0 {
		return
	}
	kept := s.seen[:0]
	for _, entry := range s.seen {
		if entry.visibleSeq == 0 && isReferenceEntry(entry.msg) {
			continue
		}
		if entry.visibleSeq == 0 || entry.visibleSeq > *checkpoint.ThroughVisibleSeq {
			kept = append(kept, entry)
		}
	}
	s.seen = kept
	s.pendingReferenceReceipt = nil
	s.compactionRetryAfter = time.Time{}
}

func isReferenceEntry(message *schema.AgenticMessage) bool {
	if message == nil || message.Extra == nil {
		return false
	}
	return message.Extra["kind"] == "reference"
}

// CompactionCoolingDown 判断会话是否仍在压缩失败退避期
func (s *Session) CompactionCoolingDown(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return now.Before(s.compactionRetryAfter)
}

// MarkCompactionFailure 设置 60 秒失败退避
func (s *Session) MarkCompactionFailure(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compactionRetryAfter = now.Add(60 * time.Second)
}

// EstimateRequestTokens 用 provider 实际用量校准后的比例估算请求输入
func (s *Session) EstimateRequestTokens(messages []*schema.AgenticMessage, tools []*schema.ToolInfo) int {
	raw := requestTokens(messages, tools)
	s.mu.Lock()
	scale := s.tokenScale
	s.mu.Unlock()
	if scale <= 0 {
		scale = 1
	}
	return int(float64(raw) * scale)
}

// ObserveTokenUsage 用 provider 返回的 prompt token 校准后续估算
func (s *Session) ObserveTokenUsage(estimated, actual int) {
	if estimated <= 0 || actual <= 0 {
		return
	}
	ratio := float64(actual) / float64(estimated)
	ratio = min(max(ratio, 0.5), 2.0)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokenScale <= 0 {
		s.tokenScale = 1
	}
	if s.tokenSamples == 0 {
		s.tokenScale = ratio
	} else {
		s.tokenScale = s.tokenScale*0.7 + ratio*0.3
	}
	s.tokenSamples++
}
