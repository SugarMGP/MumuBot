package agent

import (
	"context"
	"time"

	"go.uber.org/zap"
)

const retentionInterval = 5 * time.Minute

// startRetentionLoop 启动独立的保留维护循环，不占用模型和 worker
func (a *Agent) startRetentionLoop() {
	a.wg.Add(1)
	go a.retentionLoop()
}

func (a *Agent) retentionLoop() {
	defer a.wg.Done()
	ticker := time.NewTicker(retentionInterval)
	defer ticker.Stop()
	a.runRetentionCycle()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.runRetentionCycle()
		}
	}
}

func (a *Agent) runRetentionCycle() {
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	now := time.Now()
	if err := a.memory.SettleExpired(ctx, now); err != nil {
		zap.L().Warn("结算到期记忆或话题失败", zap.Error(err))
	}
	if err := a.memory.SettleEmotions(ctx, now); err != nil {
		zap.L().Warn("结算自然平复的情感失败", zap.Error(err))
	}
	deletedTotal := 0
	removedMedia := 0
	for batch := 0; batch < 5; batch++ {
		refs, deleted, err := a.memory.CleanupExpiredMessages(ctx, now, 500)
		if err != nil {
			zap.L().Warn("清理到期原文失败", zap.Error(err))
			break
		}
		if deleted == 0 {
			break
		}
		deletedTotal += deleted
		if len(refs) > 0 {
			removed, mediaErr := a.memory.ReleaseMedia(ctx, refs)
			if mediaErr != nil {
				zap.L().Warn("回收到期原文媒体失败", zap.Error(mediaErr))
			}
			removedMedia += removed
		}
		if deleted < 500 {
			break
		}
	}
	orphanRemoved := 0
	if a.lastOrphanCleanup.IsZero() || now.Sub(a.lastOrphanCleanup) >= 24*time.Hour {
		removed, err := a.memory.CleanupOrphanMedia(ctx, now)
		if err != nil {
			zap.L().Warn("补扫孤立媒体失败", zap.Error(err))
		} else {
			orphanRemoved = removed
			a.lastOrphanCleanup = now
		}
	}
	if a.lastTrajectoryCleanup.IsZero() || now.Sub(a.lastTrajectoryCleanup) >= 24*time.Hour {
		trajectoryCandidates, deleted, err := a.memory.CleanupTrajectories(ctx, now)
		if err != nil {
			zap.L().Warn("清理 life/worker 旧轨迹失败", zap.Error(err))
		} else {
			a.lastTrajectoryCleanup = now
			a.releaseTranscriptCandidates(ctx, trajectoryCandidates)
			if deleted > 0 {
				zap.L().Info("清理 life/worker 旧轨迹", zap.Int("deleted", deleted))
			}
		}
	}
	if a.lastToolCleanup.IsZero() || now.Sub(a.lastToolCleanup) >= 24*time.Hour {
		if err := a.memory.CleanupToolResults(ctx, 24*time.Hour); err != nil {
			zap.L().Warn("清理孤立工具结果失败", zap.Error(err))
		} else {
			a.lastToolCleanup = now
		}
	}
	if a.lastSnapshotCleanup.IsZero() || now.Sub(a.lastSnapshotCleanup) >= 24*time.Hour {
		if _, _, err := a.memory.CleanupSnapshotRefs(ctx, 24*time.Hour); err != nil {
			zap.L().Warn("清理无引用文件快照失败", zap.Error(err))
		} else {
			a.lastSnapshotCleanup = now
		}
	}
	if deletedTotal > 0 || removedMedia > 0 || orphanRemoved > 0 {
		zap.L().Info("保留维护完成",
			zap.Int("deleted_messages", deletedTotal),
			zap.Int("removed_media", removedMedia),
			zap.Int("orphan_media", orphanRemoved))
	}
}
