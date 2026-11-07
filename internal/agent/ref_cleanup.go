package agent

import (
	"context"

	"mumu-bot/internal/memory"

	"go.uber.org/zap"
)

// releaseTranscriptCandidates 先展开本批 r 的嵌套快照，再按现存根即时释放候选
func (a *Agent) releaseTranscriptCandidates(ctx context.Context, candidates memory.TranscriptCandidates) {
	if a == nil || a.memory == nil || candidates.Empty() {
		return
	}
	snapshots := append([]string(nil), candidates.SnapshotRefs...)
	if len(candidates.ToolRefs) > 0 {
		nested, err := a.memory.ExpandToolResultSnapshots(ctx, candidates.ToolRefs)
		if err != nil {
			zap.L().Warn("展开已删除工具结果中的文件引用失败", zap.Error(err))
		} else {
			snapshots = append(snapshots, nested...)
		}
		if _, err := a.memory.ReleaseToolResults(ctx, candidates.ToolRefs); err != nil {
			zap.L().Warn("释放已删除工具结果失败", zap.Error(err))
		}
	}
	if len(snapshots) == 0 {
		return
	}
	if _, _, err := a.memory.CleanupSnapshotCandidates(ctx, snapshots); err != nil {
		zap.L().Warn("即时回收文件快照候选失败", zap.Error(err))
	}
}
