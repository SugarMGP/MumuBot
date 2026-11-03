package app

import (
	"net/http"
)

func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := a.admin.OverviewStats()
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	snapshot := a.runtimeSnapshot()
	response := dashboardResponse{
		EnabledGroupCount: len(a.enabledGroups()),
		Counts: countsDTO{
			Memories:       stats.MemoryCount,
			Members:        stats.MemberCount,
			ActiveMemories: stats.ActiveCount,
			Topics:         stats.TopicCount,
			Stickers:       stats.StickerCount,
		},
		Runtime: runtimeDTO{
			Connected:    snapshot.Connected,
			SelfID:       snapshot.SelfID,
			MCPToolCount: snapshot.MCPToolCount,
		},
		Emotions: make([]emotionDTO, 0, len(snapshot.Emotions)),
	}
	if snapshot.Mood != nil {
		response.Mood = &moodDTO{
			Energy:      snapshot.Mood.Energy,
			Sociability: snapshot.Mood.Sociability,
			LastReason:  snapshot.Mood.LastReason,
			UpdatedAt:   snapshot.Mood.UpdatedAt,
		}
	}
	for _, emotion := range snapshot.Emotions {
		response.Emotions = append(response.Emotions, emotionDTO{
			Kind:       emotion.Kind,
			Strength:   emotion.Strength,
			Reason:     emotion.Reason,
			OccurredAt: emotion.OccurredAt,
		})
	}
	respondData(w, http.StatusOK, response, nil)
}
