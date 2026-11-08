package app

import (
	"net/http"
	"strings"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/logger"
	"mumu-bot/internal/migration"
	"mumu-bot/internal/modelstats"
)

func (a *App) handleSystem(w http.ResponseWriter, r *http.Request) {
	cfg := config.Snapshot()
	snapshot := a.runtimeSnapshot()
	groupIDs := a.enabledGroups()
	if groupIDs == nil {
		groupIDs = []int64{}
	}

	schemaVersion := 0
	if a.memMgr != nil {
		if value, err := a.memMgr.SchemaVersion(r.Context()); err == nil {
			schemaVersion = value
		}
	}

	var activeModel *modelProfileDTO
	contextWindow := 0
	if profile, err := a.modelStore.Active(r.Context()); err == nil {
		activeModel = &modelProfileDTO{
			Name:            profile.Name,
			BaseURL:         profile.BaseURL,
			ModelName:       profile.ModelName,
			ThinkingType:    profile.ThinkingType,
			ReasoningEffort: profile.ReasoningEffort,
		}
		contextWindow = profile.ContextWindow
	}

	respondData(w, http.StatusOK, systemResponse{
		Persona: systemPersonaDTO{
			PresetName:  cfg.Persona.PresetName,
			CardChars:   len([]rune(cfg.Persona.PersonaCard)),
			RulesChars:  len([]rune(cfg.Persona.CommonRules)),
			QQChars:     len([]rune(cfg.Persona.QQPrompt)),
			LifeChars:   len([]rune(cfg.Persona.LifePrompt)),
			WorkerChars: len([]rune(cfg.Persona.WorkerPrompt)),
		},
		Context: systemContextDTO{
			EnabledGroupCount: len(groupIDs),
			EnabledGroupIDs:   groupIDs,
			ThinkIntervalSec:  cfg.Agent.ThinkInterval,
			ThinkDebounceMS:   cfg.Agent.ThinkDebounceMS,
			MaxStep:           cfg.Agent.MaxStep,
			LifeMaxStep:       cfg.Agent.LifeMaxStep,
			WorkerMaxStep:     cfg.Agent.WorkerMaxStep,
			Compactor: compactorDTO{
				TriggerRatio:      cfg.Compactor.TriggerRatio,
				DigestBudgetRatio: cfg.Compactor.DigestBudgetRatio,
				RecentTailRatio:   cfg.Compactor.RecentTailRatio,
				MaxSteps:          cfg.Compactor.MaxSteps,
				IdleMinutes:       cfg.Compactor.IdleMinutes,
				IdleMinRatio:      cfg.Compactor.IdleMinRatio,
			},
		},
		Model: systemModelDTO{
			Active:        activeModel,
			MCPToolCount:  snapshot.MCPToolCount,
			MCPStatuses:   snapshot.MCPStatuses,
			ContextWindow: contextWindow,
		},
		Runtime: systemRuntimeDTO{
			Connected:           snapshot.Connected,
			AgentReady:          snapshot.AgentReady,
			ReconnectSec:        a.boot.OneBotReconnect,
			SchemaVersion:       schemaVersion,
			LatestSchemaVersion: migration.LatestSchemaVersion(),
			SelfID:              snapshot.SelfID,
			MediaMaxBytesMB:     cfg.Media.MaxBytesMB,
		},
	}, nil)
}

func (a *App) handleModelStats(w http.ResponseWriter, r *http.Request) {
	rangeName := queryString(r, "range")
	var since time.Time
	switch rangeName {
	case "7d":
		since = time.Now().Add(-7 * 24 * time.Hour)
	case "30d":
		since = time.Now().Add(-30 * 24 * time.Hour)
	case "all":
	default:
		rangeName = "24h"
		since = time.Now().Add(-24 * time.Hour)
	}
	stats, err := modelstats.Query(r.Context(), a.memMgr.GetDB(), since)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, modelStatsResponse{
		Range:   rangeName,
		Totals:  stats.Totals,
		ByTask:  stats.ByTask,
		ByModel: stats.ByModel,
		Series:  stats.Series,
	}, nil)
}

func (a *App) handleLogDownload(w http.ResponseWriter, r *http.Request) {
	result := logger.Query(queryString(r, "keyword"), queryString(r, "level"))
	lines := make([]string, len(result.Lines))
	for index := range result.Lines {
		lines[index] = result.Lines[index].Text
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="mumubot-logs-`+time.Now().Format("20060102-150405")+`.txt"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(strings.Join(lines, "\n") + "\n"))
}
