package app

import (
	"encoding/json"
	"net/http"
	"strings"

	"mumu-bot/internal/config"
	"mumu-bot/internal/secrets"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

func (a *App) settingsPayload() settingsResponse {
	cfg := config.Snapshot()
	return settingsResponse{
		Config:           cfg,
		Versions:         config.SectionVersions(),
		AgentReady:       a.agentReady(),
		SecretConfigured: secrets.Configured(),
	}
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	respondData(w, http.StatusOK, a.settingsPayload(), nil)
}

func (a *App) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	section := strings.TrimSpace(chi.URLParam(r, "section"))
	if !validSettingsSection(section) {
		a.failBadRequest(w, "未识别的设置分类。")
		return
	}
	var body struct {
		Version int64           `json:"version"`
		Values  json.RawMessage `json:"values"`
	}
	if err := decodeJSON(r, &body); err != nil || len(body.Values) == 0 {
		a.failBadRequest(w, "请求格式不正确。")
		return
	}
	value, err := settingsSectionValue(section, body.Values)
	if err != nil {
		a.failBadRequest(w, "设置内容不完整。")
		return
	}
	old := *config.Snapshot()
	if err := config.SaveSection(r.Context(), section, value, body.Version); err != nil {
		a.failFromError(w, r, err)
		return
	}
	warning := a.applySettingsRuntime(section, old)
	respondData(w, http.StatusOK, a.settingsPayload(), warning)
}

func (a *App) handleSettingsReset(w http.ResponseWriter, r *http.Request) {
	section := strings.TrimSpace(chi.URLParam(r, "section"))
	if !validSettingsSection(section) {
		a.failBadRequest(w, "未识别的设置分类。")
		return
	}
	var body struct {
		Version int64 `json:"version"`
	}
	if err := decodeJSON(r, &body); err != nil {
		a.failBadRequest(w, "请求格式不正确。")
		return
	}
	old := *config.Snapshot()
	if err := config.ResetSection(r.Context(), section, body.Version); err != nil {
		a.failFromError(w, r, err)
		return
	}
	warning := a.applySettingsRuntime(section, old)
	respondData(w, http.StatusOK, a.settingsPayload(), warning)
}

func validSettingsSection(section string) bool {
	switch section {
	case config.SectionAgent, config.SectionChat, config.SectionCompactor,
		config.SectionMaintenance, config.SectionMedia, config.SectionDebug:
		return true
	default:
		return false
	}
}

func settingsSectionValue(section string, raw json.RawMessage) (any, error) {
	var target any
	switch section {
	case config.SectionAgent:
		target = &config.AgentConfig{}
	case config.SectionChat:
		target = &config.ChatConfig{}
	case config.SectionCompactor:
		target = &config.CompactorConfig{}
	case config.SectionMaintenance:
		target = &config.MaintenanceConfig{}
	case config.SectionMedia:
		target = &config.MediaConfig{}
	case config.SectionDebug:
		target = &config.DebugConfig{}
	default:
		return nil, nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return nil, err
	}
	return target, nil
}

// applySettingsRuntime 把已保存的配置同步到运行态；失败返回警告而不是回滚
func (a *App) applySettingsRuntime(section string, old config.RuntimeConfig) *warningPayload {
	if a.mumuAgent == nil {
		return nil
	}
	next := config.Snapshot()
	a.mumuAgent.SetConcurrencyLimits(next.Agent.MaxGroupConcurrency, next.Agent.MaxPrivateConcurrency)
	if section == config.SectionAgent && old.Agent.WorkerCount != next.Agent.WorkerCount {
		if err := a.mumuAgent.SyncWorkers(next.Agent.WorkerCount); err != nil {
			zap.L().Warn("设置已保存但 worker 数量应用失败", zap.Error(err))
			return &warningPayload{Code: "runtime_worker_sync_failed"}
		}
	}
	needGeneration := section == config.SectionMedia ||
		(section == config.SectionAgent && old.Agent.MaxStep != next.Agent.MaxStep)
	if !needGeneration || !a.agentReady() {
		return nil
	}
	if err := a.mumuAgent.Reload(); err != nil {
		zap.L().Warn("设置已保存但运行态应用失败", zap.String("section", section), zap.Error(err))
		return &warningPayload{Code: "runtime_reload_failed"}
	}
	return nil
}
