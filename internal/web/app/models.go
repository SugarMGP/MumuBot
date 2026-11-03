package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"mumu-bot/internal/llm"
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
	respondData(w, http.StatusOK, modelsResponse{
		Items:            profiles,
		SecretConfigured: secrets.Configured(),
	}, nil)
}

type modelInput struct {
	Name            string `json:"name"`
	BaseURL         string `json:"base_url"`
	ModelName       string `json:"model_name"`
	ContextWindow   int    `json:"context_window"`
	ThinkingType    string `json:"thinking_type"`
	ReasoningEffort string `json:"reasoning_effort"`
	APIKey          string `json:"api_key"`
	Version         int64  `json:"version"`
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
	created, err := a.modelStore.Create(r.Context(), modelprofiles.Profile{
		Name:            strings.TrimSpace(body.Name),
		BaseURL:         strings.TrimSpace(body.BaseURL),
		ModelName:       strings.TrimSpace(body.ModelName),
		ContextWindow:   body.ContextWindow,
		ThinkingType:    strings.TrimSpace(body.ThinkingType),
		ReasoningEffort: strings.TrimSpace(body.ReasoningEffort),
	}, body.APIKey)
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
	updated, err := a.modelStore.Update(r.Context(), modelprofiles.Profile{
		ID:              id,
		Name:            strings.TrimSpace(body.Name),
		BaseURL:         strings.TrimSpace(body.BaseURL),
		ModelName:       strings.TrimSpace(body.ModelName),
		ContextWindow:   body.ContextWindow,
		ThinkingType:    strings.TrimSpace(body.ThinkingType),
		ReasoningEffort: strings.TrimSpace(body.ReasoningEffort),
		Version:         body.Version,
	}, body.APIKey)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	var warning *warningPayload
	if updated.Active && a.agentReady() {
		if err := a.mumuAgent.Reload(); err != nil {
			zap.L().Warn("模型已保存但运行态应用失败", zap.Error(err))
			warning = &warningPayload{Code: "runtime_reload_failed"}
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

func (a *App) handleModelActivate(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil || id == 0 {
		respondError(w, http.StatusBadRequest, "validation_failed", "模型编号无效。", nil)
		return
	}
	old, oldErr := a.modelStore.Active(r.Context())
	if oldErr != nil && !errors.Is(oldErr, modelprofiles.ErrNotFound) {
		a.failFromError(w, r, oldErr)
		return
	}
	if err := a.modelStore.SetActive(r.Context(), id); err != nil {
		a.failFromError(w, r, err)
		return
	}
	if a.mumuAgent != nil {
		if err := a.mumuAgent.Reload(); err != nil {
			if oldErr == nil {
				_ = a.modelStore.SetActive(context.Background(), old.ID)
			} else {
				_ = a.modelStore.ClearActive(context.Background(), id)
			}
			zap.L().Warn("模型切换失败，已回滚", zap.Error(err))
			respondError(w, http.StatusBadRequest, "model_activate_failed", "模型切换失败，已继续使用旧配置。", nil)
			return
		}
	}
	respondData(w, http.StatusOK, map[string]bool{"ok": true}, nil)
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
		APIKey:          key,
		BaseURL:         row.BaseURL,
		Model:           row.ModelName,
		ThinkingType:    row.ThinkingType,
		ReasoningEffort: row.ReasoningEffort,
	}); err != nil {
		respondError(w, http.StatusBadRequest, "model_test_failed", "模型连接测试失败", map[string]any{
			"reason": err.Error(),
		})
		return
	}
	respondData(w, http.StatusOK, map[string]bool{"ok": true}, nil)
}
