package app

import (
	"net/http"

	"mumu-bot/internal/memory"

	"go.uber.org/zap"
)

func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	snapshot := a.runtimeSnapshot()
	stats, err := a.admin.OverviewStats(r.Context(), snapshot.SelfID)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	if !snapshot.AgentReady {
		stats.RunningWorkers = 0
	}
	response := dashboardResponse{
		Counts: countsDTO{
			ActiveConversations: stats.ActiveConversations,
			ActiveMemories:      stats.ActiveMemories,
			ActiveTopics:        stats.ActiveTopics,
			RunningWorkers:      stats.RunningWorkers,
			WorkerCount:         stats.WorkerCount,
		},
		Runtime: runtimeDTO{
			Connected:    snapshot.Connected,
			SelfID:       snapshot.SelfID,
			MCPToolCount: snapshot.MCPToolCount,
		},
	}
	var emotions []memory.CurrentEmotion
	if a.memMgr == nil {
		response.EmotionsError = true
	} else {
		if mood, moodErr := a.memMgr.GetMoodState(); moodErr == nil {
			response.Mood = &moodDTO{
				Energy:      mood.Energy,
				Sociability: mood.Sociability,
				LastReason:  mood.LastReason,
				UpdatedAt:   mood.UpdatedAt,
			}
		} else {
			zap.L().Warn("读取精力状态失败", zap.Error(moodErr))
		}
		if emotions, err = a.memMgr.CurrentEmotions(r.Context()); err != nil {
			response.EmotionsError = true
			zap.L().Warn("读取当前情感失败", zap.Error(err))
		}
	}
	response.Emotions = make([]emotionDTO, 0, len(emotions))
	for _, emotion := range emotions {
		response.Emotions = append(response.Emotions, emotionDTO{
			ID: emotion.ID, Kind: emotion.Kind, Fact: emotion.Fact, Reason: emotion.Reason,
			Strength: emotion.Strength, StrengthBand: emotion.StrengthBand,
			OccurredAt: emotion.OccurredAt, UpdatedAt: emotion.UpdatedAt, Version: emotion.Version,
		})
	}
	respondData(w, http.StatusOK, response, nil)
}
