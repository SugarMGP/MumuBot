package agent

import (
	"go.uber.org/zap"
)

// reorderBuffer 按会话内到达序号重排提交项，保证落库、撤回、入缓冲和思考调度按到达顺序执行
// 等待窗口有上限：超限时丢弃最接近水位的等待项并把水位推进越过它，
// 被越过的序号后续到达时自然跳过，不记录、不留下永久缺口、内存有界；
// 若到达项本身最接近水位，则越过中间缺口直接提交到达项，等待项保持不变
type reorderBuffer[T any] struct {
	logField func(int64) zap.Field
	next     uint64
	pending  map[uint64]T
}

func newReorderBuffer[T any](logField func(int64) zap.Field, next uint64) *reorderBuffer[T] {
	if next == 0 {
		next = 1
	}
	return &reorderBuffer[T]{logField: logField, pending: make(map[uint64]T), next: next}
}

// push 处理一个到达项；commit 负责真正提交，调用方保证 commit 串行执行
func (b *reorderBuffer[T]) push(seq uint64, targetID int64, item T, commit func(T)) {
	for {
		switch {
		case seq < b.next:
			// 水位已越过该项，继续推进后面已就绪的等待项
			b.drain(commit)
			return
		case seq == b.next:
			commit(item)
			b.next++
			b.drain(commit)
			return
		default: // seq > b.next：等待窗口有空位时直接排队
			if len(b.pending) < pendingCommitSize {
				b.pending[seq] = item
				b.drain(commit)
				return
			}
			minSeq := seq
			for pendingSeq := range b.pending {
				if pendingSeq < minSeq {
					minSeq = pendingSeq
				}
			}
			if seq < minSeq {
				// 到达项比所有等待项更接近水位：越过中间缺口直接提交到达项，等待项保持
				zap.L().Warn("提交重排等待队列超限，跳过缺口提交最接近水位的到达项", b.logField(targetID), zap.Uint64("skipped_from", b.next), zap.Uint64("seq", seq), zap.Int("pending", len(b.pending)))
				b.next = seq
			} else {
				delete(b.pending, minSeq)
				b.next = minSeq + 1
				zap.L().Error("提交重排等待队列超限，丢弃最旧等待项并推进水位", b.logField(targetID), zap.Uint64("dropped_seq", minSeq), zap.Uint64("watermark", b.next), zap.Int("pending", len(b.pending)))
			}
			// 水位推进后回到循环顶部，当前项直接提交、跳过或重新排队
		}
	}
}

// drain 从当前水位起连续提交已就绪的等待项
func (b *reorderBuffer[T]) drain(commit func(T)) {
	for {
		queued, ok := b.pending[b.next]
		if !ok {
			return
		}
		delete(b.pending, b.next)
		commit(queued)
		b.next++
	}
}

func (b *reorderBuffer[T]) warnLeftover() {
	if len(b.pending) > 0 {
		zap.L().Warn("停机排空结束，乱序等待项未提交", zap.Int("pending", len(b.pending)))
	}
}
