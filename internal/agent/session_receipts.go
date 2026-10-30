package agent

import (
	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
)

// PendingReferenceReceipt 返回本轮待提交的自动参考回执。
func (s *Session) PendingReferenceReceipt() *memory.ReferenceReceipt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingReferenceReceipt
}

// ClearPendingReferenceReceipt 在 receipt 成功落库后清空。
func (s *Session) ClearPendingReferenceReceipt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingReferenceReceipt = nil
}

// PendingToolReceipts 汇总尚未成功送入模型的工具回执，重启恢复后仍可推进 tool seen。
func (s *Session) PendingToolReceipts() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for _, entry := range s.seen {
		if entry.visibleSeq != 0 || entry.msg == nil || entry.msg.Extra == nil {
			continue
		}
		raw, ok := entry.msg.Extra["context_receipts"]
		if !ok {
			continue
		}
		data, err := sonic.Marshal(raw)
		if err != nil {
			continue
		}
		var receipts map[string]string
		if sonic.Unmarshal(data, &receipts) != nil {
			continue
		}
		for key, version := range receipts {
			if key != "" && version != "" {
				out[key] = version
			}
		}
	}
	return out
}
