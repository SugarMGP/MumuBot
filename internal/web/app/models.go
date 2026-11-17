package app

import (
	"context"
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
	}, nil)
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
func (a *App) prepareModelCapacity(ctx context.Context, targets map[string]agent.ModelTarget) (func(), []string, bool) {
	if len(targets) == 0 {
		return func() {}, nil, true
	}
	currentAgent := a.currentAgent()
	var release func()
	var affected []string
	var err error
	if currentAgent != nil && currentAgent.Ready() {
		release, affected, err = currentAgent.PrepareModelSwitch(ctx, targets)
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
		release, affected, err = agent.PrepareModelSwitchFor(ctx, a.memMgr, a.modelStore, targets, mcpManager)
	}
	if err != nil {
		zap.L().Warn("为模型切换整理上下文失败", zap.Error(err))
		return release, affected, false
	}
	return release, affected, true
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
	for _, role := range []string{"qq", "life", "worker"} {
		if selection.ID(role) == id {
			targets[role] = agent.ModelTarget{Provider: row.Provider, ContextWindow: row.ContextWindow, CheckCapacity: row.ContextWindow < current.ContextWindow}
		}
	}
	release, affected, ok := a.prepareModelCapacity(r.Context(), targets)
	defer release()
	if !ok || len(affected) > 0 {
		respondError(w, http.StatusBadRequest, "model_switch_context_failed", "", map[string]any{"sessions": affected})
		return
	}
	updated, err := a.modelStore.Update(r.Context(), row, body.APIKey)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	var warning *warningPayload
	if selection.Uses(updated.ID) {
		if agent := a.currentAgent(); agent != nil {
			if err := agent.Reload(); err != nil {
				zap.L().Warn("模型已保存但运行态应用失败", zap.Error(err))
				warning = &warningPayload{Code: "runtime_reload_failed"}
			}
		}
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
		targets[role] = agent.ModelTarget{Provider: target.Provider, ContextWindow: target.ContextWindow, CheckCapacity: target.ContextWindow < current.ContextWindow}
	}
	release, affected, ok := a.prepareModelCapacity(r.Context(), targets)
	defer release()
	if !ok || len(affected) > 0 {
		respondError(w, http.StatusBadRequest, "model_switch_context_failed", "", map[string]any{"sessions": affected})
		return
	}
	saved, err := a.modelStore.Select(r.Context(), body)
	if err != nil {
		a.failFromError(w, r, err)
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
			respondError(w, http.StatusBadRequest, "model_activate_failed", "", nil)
			return
		}
	}
	a.handleModels(w, r)
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
