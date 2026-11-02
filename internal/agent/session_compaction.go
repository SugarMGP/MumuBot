package agent

import (
	"fmt"
	"strings"
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
	heads := make([]sessionEntry, 0)
	work := make([]sessionEntry, 0, len(entries))
	for _, entry := range entries {
		if isHeadEntry(entry.msg) {
			heads = append(heads, entry)
			continue
		}
		work = append(work, entry)
	}
	segments := splitSegments(work)
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
	for _, entry := range heads {
		if entry.visibleSeq > candidate.ThroughVisibleSeq {
			candidate.ThroughVisibleSeq = entry.visibleSeq
		}
	}
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

// ApplyCompaction 丢弃检查点覆盖的工作集，只保留完整尾部
func (s *Session) ApplyCompaction(checkpoint *memory.CompactionCheckpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if checkpoint == nil || checkpoint.ThroughVisibleSeq == nil || *checkpoint.ThroughVisibleSeq == 0 {
		return
	}
	kept := s.seen[:0]
	for _, entry := range s.seen {
		// 代头随换代重建，旧的一律丢弃，避免出现两份人格
		if isHeadEntry(entry.msg) {
			continue
		}
		if entry.visibleSeq == 0 || entry.visibleSeq > *checkpoint.ThroughVisibleSeq {
			kept = append(kept, entry)
		}
	}
	s.seen = kept
	s.tokenBase = nil
	s.compactionRetryAfter = time.Time{}
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

// SetTokenBase 记录最后一条 assistant 的完整 usage，供压缩和空闲判断使用
func (s *Session) SetTokenBase(message *schema.AgenticMessage, usage *schema.TokenUsage) {
	if message == nil || usage == nil {
		return
	}
	total := usage.TotalTokens
	if total <= 0 {
		total = usage.PromptTokens + usage.CompletionTokens
	}
	if total <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenBase = &tokenBase{
		message: message, prompt: usage.PromptTokens, completion: usage.CompletionTokens, total: total,
	}
}

// LastResponseTokens 返回最后一条 assistant 的实际总 token 数
func (s *Session) LastResponseTokens() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokenBase == nil {
		return 0
	}
	return s.tokenBase.total
}

// ClearTokenBase 在压缩或恢复后清空实际用量基线
func (s *Session) ClearTokenBase() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenBase = nil
}

// estimateTextTokens 用 provider 实际用量校准后的比例估算静态文本块
func (s *Session) estimateTextTokens(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	s.mu.Lock()
	scale := s.fullScale
	s.mu.Unlock()
	if scale <= 0 {
		scale = 1
	}
	return int(float64(estimateTokens(text)) * scale)
}

// ObserveResponseUsage 用 provider 的真实 prompt 用量校准文本估算比例
func (s *Session) ObserveResponseUsage(requestMessages []*schema.AgenticMessage, tools []*schema.ToolInfo, usage *schema.TokenUsage) {
	if usage == nil {
		return
	}
	promptTokens := usage.PromptTokens
	if promptTokens <= 0 || len(requestMessages) == 0 {
		return
	}
	s.mu.Lock()
	base := s.tokenBase
	s.mu.Unlock()
	if base != nil {
		for i, message := range requestMessages {
			if message != base.message {
				continue
			}
			// prompt 是精确输入量；上一次 prompt+completion 就是这次输入会带上的前缀
			actualIncrement := promptTokens - (base.prompt + base.completion)
			estimatedIncrement := requestTokens(requestMessages[i+1:], nil)
			if actualIncrement > 0 && estimatedIncrement > 0 {
				s.updateFullScale(float64(actualIncrement) / float64(estimatedIncrement))
				return
			}
			break
		}
	}
	estimatedFull := requestTokens(requestMessages, tools)
	if estimatedFull > 0 {
		s.updateFullScale(float64(promptTokens) / float64(estimatedFull))
	}
}

func (s *Session) updateFullScale(ratio float64) {
	ratio = min(max(ratio, 0.5), 2.0)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fullScale <= 0 {
		s.fullScale = 1
	}
	if s.fullSamples == 0 {
		s.fullScale = ratio
	} else {
		s.fullScale = s.fullScale*0.7 + ratio*0.3
	}
	s.fullSamples++
}
