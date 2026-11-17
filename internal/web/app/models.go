package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"mumu-bot/internal/agent"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/modelprofiles"
	"mumu-bot/internal/secrets"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

func (a *App) handleModels(w http.ResponseWriter, r *http.Request) {
	a.respondModels(w, r, nil)
}

func (a *App) respondModels(w http.ResponseWriter, r *http.Request, warning *warningPayload) {
	profiles, err := a.modelStore.List(r.Context())
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	if profiles == nil {
		profiles = []modelprofiles.Profile{}
	}
	selection, err := a.modelStore.Selection(r.Context())
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, modelsResponse{
		Selection:        selection,
		Items:            profiles,
		SecretConfigured: secrets.Configured(),
	}, warning)
}

type modelInput struct {
	Provider        string `json:"provider"`
	Name            string `json:"name"`
	BaseURL         string `json:"base_url"`
	ModelName       string `json:"model_name"`
	ContextWindow   int    `json:"context_window"`
	ReasoningEffort string `json:"reasoning_effort"`
	ThinkingMode    string `json:"thinking_mode"`
	BodyOverride    string `json:"body_override"`
	APIKey          string `json:"api_key"`
	Version         int64  `json:"version"`
}

func (input modelInput) profile(id uint) (modelprofiles.Profile, error) {
	row := modelprofiles.Profile{
		ID: id, Name: strings.TrimSpace(input.Name), Provider: input.Provider,
		BaseURL: strings.TrimSpace(input.BaseURL), ModelName: strings.TrimSpace(input.ModelName),
		ContextWindow: input.ContextWindow, ReasoningEffort: input.ReasoningEffort,
		ThinkingMode: input.ThinkingMode, BodyOverride: input.BodyOverride, Version: input.Version,
	}
	return row, row.Validate()
}

// prepareModelCapacity 在保存前自动整理超限历史，并把会话锁保留到运行态切换完成
func (a *App) prepareModelCapacity(ctx context.Context, targets map[string]agent.ModelTarget) (func(), *agent.ModelSwitchReport, bool) {
	if len(targets) == 0 {
		return func() {}, nil, true
	}
	currentAgent := a.currentAgent()
	var release func()
	var report *agent.ModelSwitchReport
	var err error
	if currentAgent != nil && currentAgent.Ready() {
		release, report, err = currentAgent.PrepareModelSwitch(ctx, targets)
	} else {
		var mcpManager *mcp.Manager
		if a.mcpStore != nil {
			configs, configErr := a.mcpStore.EnabledConfigs(ctx)
			if configErr != nil {
				zap.L().Warn("读取 MCP 配置失败，容量检查只计内置工具", zap.Error(configErr))
			} else if len(configs) > 0 {
				mcpManager = mcp.NewMCPManager(a.memMgr.RefStore())
				_ = mcpManager.Load(ctx, configs)
				defer mcpManager.Close()
			}
		}
		release, report, err = agent.PrepareModelSwitchFor(ctx, a.memMgr, a.modelStore, targets, mcpManager)
	}
	if err != nil {
		zap.L().Warn("为模型切换整理上下文失败", zap.Error(err))
		return release, report, false
	}
	return release, report, true
}

func (a *App) handleModelCreate(w http.ResponseWriter, r *http.Request) {
	if !secrets.Configured() {
		a.failSecretUnavailable(w)
		return
	}
	var body modelInput
	if err := decodeJSON(r, &body); err != nil {
		a.failBadRequest(w, "请求格式不正确。")
		return
	}
	if strings.TrimSpace(body.APIKey) == "" {
		a.failBadRequest(w, "首次保存模型必须填写 API Key。")
		return
	}
	row, err := body.profile(0)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	created, err := a.modelStore.Create(r.Context(), row, body.APIKey)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusCreated, created, nil)
}

func (a *App) handleModelUpdate(w http.ResponseWriter, r *http.Request) {
	if !secrets.Configured() {
		a.failSecretUnavailable(w)
		return
	}
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	var body modelInput
	if err := decodeJSON(r, &body); err != nil {
		a.failBadRequest(w, "请求格式不正确。")
		return
	}
	selection, err := a.modelStore.Selection(r.Context())
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	current, currentErr := a.modelStore.Get(r.Context(), id)
	if currentErr != nil {
		a.failFromError(w, r, currentErr)
		return
	}
	if body.Version != current.Version {
		a.failFromError(w, r, modelprofiles.ErrVersionConflict)
		return
	}
	row, err := body.profile(id)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	targets := make(map[string]agent.ModelTarget)
	if selection.Uses(id) {
		target, err := a.modelSwitchTarget(current, row, strings.TrimSpace(body.APIKey))
		if err != nil {
			a.failFromError(w, r, err)
			return
		}
		for _, role := range []string{"qq", "life", "worker"} {
			if selection.ID(role) == id {
				targets[role] = target
			}
		}
	}
	release, report, ok := a.prepareModelCapacity(r.Context(), targets)
	defer release()
	if !ok || (report != nil && len(report.Sessions) > 0) {
		a.failModelSwitch(w, report)
		return
	}
	updated, err := a.modelStore.Update(r.Context(), row, body.APIKey)
	if err != nil {
		a.failModelSave(w, r, err, report)
		return
	}
	var warning *warningPayload
	if report != nil && report.Fallback {
		warning = &warningPayload{Code: "model_switch_fallback"}
	}
	if selection.Uses(updated.ID) {
		if agent := a.currentAgent(); agent != nil {
			if err := agent.Reload(); err != nil {
				zap.L().Warn("模型已保存但运行态应用失败", zap.Error(err))
				warning = &warningPayload{Code: "runtime_reload_failed"}
			}
		}
	}
	if report != nil && (warning == nil || warning.Code != "runtime_reload_failed") {
		report.Applied = true
	}
	respondData(w, http.StatusOK, updated, warning)
}

func (a *App) handleModelDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	version := queryInt64(r, "version")
	if err := a.modelStore.Delete(r.Context(), id, version); err != nil {
		a.failFromError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleModelSelection(w http.ResponseWriter, r *http.Request) {
	var body modelprofiles.Selection
	if err := decodeJSON(r, &body); err != nil {
		a.failBadRequest(w, "")
		return
	}
	if body.QQ == 0 || body.Life == 0 || body.Worker == 0 {
		a.failFromError(w, r, modelprofiles.ErrInvalidModel)
		return
	}
	old, err := a.modelStore.Selection(r.Context())
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	if body.Version != old.Version {
		a.failFromError(w, r, modelprofiles.ErrVersionConflict)
		return
	}
	targets := make(map[string]agent.ModelTarget)
	for _, role := range []string{"qq", "life", "worker"} {
		target, err := a.modelStore.Get(r.Context(), body.ID(role))
		if err != nil {
			a.failFromError(w, r, err)
			return
		}
		if err := target.Validate(); err != nil {
			a.failFromError(w, r, err)
			return
		}
		oldID := old.ID(role)
		if target.ID == oldID {
			continue
		}
		if oldID == 0 {
			continue
		}
		current, err := a.modelStore.Get(r.Context(), oldID)
		if err != nil {
			a.failFromError(w, r, err)
			return
		}
		prepared, err := a.modelSwitchTarget(current, target, "")
		if err != nil {
			a.failFromError(w, r, err)
			return
		}
		targets[role] = prepared
	}
	release, report, ok := a.prepareModelCapacity(r.Context(), targets)
	defer release()
	if !ok || (report != nil && len(report.Sessions) > 0) {
		a.failModelSwitch(w, report)
		return
	}
	saved, err := a.modelStore.Select(r.Context(), body)
	if err != nil {
		a.failModelSave(w, r, err, report)
		return
	}
	if agent := a.currentAgent(); agent != nil {
		if err := agent.Reload(); err != nil {
			var rollbackErr error
			rollbackCtx := context.WithoutCancel(r.Context())
			if old.Version == 0 {
				rollbackErr = a.modelStore.ClearSelection(rollbackCtx, saved.Version)
			} else {
				old.Version = saved.Version
				_, rollbackErr = a.modelStore.Select(rollbackCtx, old)
			}
			if rollbackErr != nil {
				zap.L().Error("回滚场合模型失败", zap.Error(rollbackErr))
			}
			zap.L().Warn("场合模型切换失败", zap.Error(err))
			respondError(w, http.StatusBadRequest, "model_activate_failed", "", map[string]any{"compacted": report != nil && report.Compacted})
			return
		}
	}
	var warning *warningPayload
	if report != nil && report.Fallback {
		warning = &warningPayload{Code: "model_switch_fallback"}
	}
	if report != nil {
		report.Applied = true
	}
	a.respondModels(w, r, warning)
}

func (a *App) handleModelTest(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "模型编号无效。", nil)
		return
	}
	row, err := a.modelStore.Get(r.Context(), id)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	key, err := a.modelStore.DecryptKey(row)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := llm.Test(ctx, llm.ModelConfig{
		Provider:        row.Provider,
		APIKey:          key,
		BaseURL:         row.BaseURL,
		Model:           row.ModelName,
		ReasoningEffort: row.ReasoningEffort,
		ThinkingMode:    row.ThinkingMode,
		BodyOverride:    row.BodyOverride,
	}); err != nil {
		respondError(w, http.StatusBadRequest, "model_test_failed", "模型连接测试失败", map[string]any{
			"reason": err.Error(),
		})
		return
	}
	respondData(w, http.StatusOK, map[string]bool{"ok": true}, nil)
}

func (a *App) modelSwitchTarget(current, target modelprofiles.Profile, apiKey string) (agent.ModelTarget, error) {
	oldKey, keyErr := a.modelStore.DecryptKey(current)
	if apiKey == "" {
		if target.ID == current.ID {
			if keyErr != nil {
				return agent.ModelTarget{}, keyErr
			}
			apiKey = oldKey
		} else {
			var err error
			apiKey, err = a.modelStore.DecryptKey(target)
			if err != nil {
				return agent.ModelTarget{}, err
			}
		}
	}
	return agent.ModelTarget{Profile: target, APIKey: apiKey, CheckCapacity: target.ContextWindow < current.ContextWindow,
		ConnectionChanged: target.Provider != current.Provider || target.BaseURL != current.BaseURL || target.ModelName != current.ModelName,
		KeyChanged:        keyErr != nil || oldKey != apiKey}, nil
}

func (a *App) failModelSwitch(w http.ResponseWriter, report *agent.ModelSwitchReport) {
	code := "model_switch_context_failed"
	if report != nil {
		switch report.Stage {
		case "target":
			code = "model_switch_target_failed"
		case "window":
			code = "model_switch_window_failed"
		case "storage":
			code = "model_switch_storage_failed"
		case "busy":
			code = "model_switch_busy"
		}
	}
	var details map[string]any
	if report != nil {
		details = map[string]any{"sessions": report.Sessions, "stage": report.Stage, "compacted": report.Compacted, "fallback": report.Fallback}
	}
	respondError(w, http.StatusBadRequest, code, "", details)
}

func (a *App) failModelSave(w http.ResponseWriter, r *http.Request, err error, report *agent.ModelSwitchReport) {
	if report == nil || !report.Compacted {
		a.failFromError(w, r, err)
		return
	}
	if errors.Is(err, modelprofiles.ErrVersionConflict) {
		respondError(w, http.StatusConflict, "version_conflict", "", map[string]any{"compacted": true})
		return
	}
	zap.L().Warn("上下文已整理，模型配置保存失败", zap.Error(err))
	report.Stage = "storage"
	a.failModelSwitch(w, report)
}
