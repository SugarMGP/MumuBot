package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
)

// CompleteTurn 在成功完成一次思考后推进维护计数
func (s *Session) CompleteTurn(memoryMaintained, topicMaintained bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnSeq++
	if memoryMaintained {
		s.lastMemoryTurn = s.turnSeq
	}
	if topicMaintained {
		s.lastTopicTurn = s.turnSeq
	}
}

// MaintenanceDue 返回本轮需要追加的维护提醒和对应计数
func (s *Session) MaintenanceDue(memoryTurns, topicTurns int) (string, uint64, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var labels []string
	nextMemory, nextTopic := s.lastMemoryHintTurn, s.lastTopicHintTurn
	if memoryTurns > 0 && s.turnSeq-s.lastMemoryTurn >= uint64(memoryTurns) && s.turnSeq-s.lastMemoryHintTurn >= uint64(max(1, memoryTurns/2)) {
		labels = append(labels, fmt.Sprintf("你已经连续 %d 轮没有更新长期记忆。如果这段对话或刚才的查询里出现了值得留存的新梗/事物知识（object）、费劲查明的结论、群内文化（group）、人物稳定偏好与约定（user）、你自己的新经验与变化（self）、重要共同经历（experience）或需要纠正的旧记忆，先用 searchMemory 查重，再用 manageMemory 新建或更新；没有就继续，不要为了清空提醒而硬写。更新记忆时静默调用工具即可，不要在对外发言里专门汇报自己记了记忆。", memoryTurns))
		nextMemory = s.turnSeq
	}
	if topicTurns > 0 && s.turnSeq-s.lastTopicTurn >= uint64(topicTurns) && s.turnSeq-s.lastTopicHintTurn >= uint64(max(1, topicTurns/2)) {
		labels = append(labels, fmt.Sprintf("你已经连续 %d 轮没有更新话题。如果刚才的对话有实质进展、结论、未完事项或收尾，用 updateTopic 更新摘要和关键消息；没有就继续，不要硬造话题。", topicTurns))
		nextTopic = s.turnSeq
	}
	if len(labels) == 0 {
		return "", nextMemory, nextTopic
	}
	return strings.Join(labels, "\n"), nextMemory, nextTopic
}

// AppendLifeSelfCheck 在 life 成功结束但仍无未来安排时给一次当轮自检机会
func (s *Session) AppendLifeSelfCheck(ctx context.Context, mem *memory.Manager, text string) error {
	if mem == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	msg := schema.UserAgenticMessage(systemNote("生活自检", text))
	msg.Extra = map[string]any{"kind": "life_self_check"}
	payload, err := sonic.Marshal(msg)
	if err != nil {
		return err
	}
	entry := &memory.TranscriptEntry{ConversationKind: s.kind, TargetID: s.targetID, EntryKind: "system_note", Payload: payload}
	if err := mem.AppendTranscript(ctx, entry); err != nil {
		return err
	}
	s.mu.Lock()
	s.pending = append(s.pending, &sessionUser{Msg: msg, Text: text, TranscriptID: entry.ID, IsSelfCheck: true, Order: s.nextOrderLocked()})
	s.mu.Unlock()
	return nil
}

// AppendMaintenanceHint 写入持久化系统提醒并加入待处理输入
func (s *Session) AppendMaintenanceHint(ctx context.Context, mem *memory.Manager, text string, memoryTurn, topicTurn uint64) error {
	if mem == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	msg := schema.UserAgenticMessage(systemNote("维护提醒", text))
	msg.Extra = map[string]any{"kind": "maintenance_hint"}
	payload, err := sonic.Marshal(msg)
	if err != nil {
		return err
	}
	entry := &memory.TranscriptEntry{ConversationKind: s.kind, TargetID: s.targetID, EntryKind: "system_note", Payload: payload}
	if err := mem.AppendTranscript(ctx, entry); err != nil {
		return err
	}
	s.mu.Lock()
	s.pending = append(s.pending, &sessionUser{Msg: msg, Text: text, TranscriptID: entry.ID, IsMaintenanceHint: true, Order: s.nextOrderLocked()})
	s.lastMemoryHintTurn = memoryTurn
	s.lastTopicHintTurn = topicTurn
	s.mu.Unlock()
	return nil
}

// PruneBefore 在重启恢复或新上下文装配前丢弃显式起点之前的内存工作集
func (s *Session) PruneBefore(startID uint) {
	if s == nil || startID == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if startID <= s.contextStartID {
		return
	}
	s.contextStartID = startID
	seen := s.seen[:0]
	for _, entry := range s.seen {
		if entry.transcriptID >= startID {
			seen = append(seen, entry)
		}
	}
	s.seen = seen
	pending := s.pending[:0]
	for _, entry := range s.pending {
		if entry.TranscriptID >= startID {
			pending = append(pending, entry)
		}
	}
	s.pending = pending
	s.media = newMediaIndex()
	s.tokenBase = nil
	s.fullScale = 0
	s.compactionBeforeModel = false
	s.failedBatchThrough = 0
	s.requestActionableThrough = 0
}

// MarkAgentMessageWake 把已落库的静默内部信改成请求立即处理，供精确叫醒使用
func (s *Session) MarkAgentMessageWake(transcriptID uint) {
	if s == nil || transcriptID == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.pending {
		if user == nil || user.TranscriptID != transcriptID || !user.IsAgentMessage {
			continue
		}
		user.Wake = true
		return
	}
}

// IdleAvailable 判断当前空闲周期是否已经尝试过压缩
func (s *Session) IdleAvailable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.idleDoneForActivity
}

// MarkIdleStart 登记空闲压缩取消函数
func (s *Session) MarkIdleStart(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idleCancel = cancel
}

// ClearIdleStart 清除空闲压缩取消函数
func (s *Session) ClearIdleStart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idleCancel = nil
}

// MarkIdleDone 标记当前空闲周期已经处理过
func (s *Session) MarkIdleDone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idleDoneForActivity = true
}

// MarkTopicMaintained 记录话题维护
func (s *Session) MarkTopicMaintained() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTopicTurn = s.turnSeq
}

// LastActivityAt 返回最近真实消息时间
func (s *Session) LastActivityAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActivityAt
}

// SetLastActivity 更新最近真实消息时间
func (s *Session) SetLastActivity(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.After(s.lastActivityAt) {
		s.lastActivityAt = at
	}
}

// StartNewContext 丢弃显式起点之前的内存工作集；边界统一由 PruneBefore 处理
func (s *Session) StartNewContext(startID uint) {
	s.PruneBefore(startID)
}
