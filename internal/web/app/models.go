package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mumu-bot/internal/llm"
	"mumu-bot/internal/modelprofiles"
	"mumu-bot/internal/secrets"
	"mumu-bot/internal/web/views"
)

func (a *App) modelsPageData(r *http.Request, errorText string) views.ModelPageData {
	profiles, err := a.modelStore.List(r.Context())
	if err != nil && errorText == "" {
		errorText = err.Error()
	}
	data := views.ModelPageData{
		Profiles:         profiles,
		SecretConfigured: secrets.Configured(),
		Error:            errorText,
		Flash:            a.flashFromRequest(r),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("edit")); raw != "" {
		id, parseErr := strconv.ParseUint(raw, 10, 64)
		if parseErr == nil {
			profile, getErr := a.modelStore.Get(r.Context(), uint(id))
			if getErr == nil {
				data.Edit = &profile
			}
		}
	}
	return data
}

func (a *App) handleModels(w http.ResponseWriter, r *http.Request) {
	a.render(w, views.ModelsPage(a.modelsPageData(r, ""), r.URL.Path))
}

func (a *App) handleModelAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.renderModelError(w, r, "请求格式不正确。")
		return
	}
	switch strings.TrimSpace(r.FormValue("action")) {
	case "save":
		a.saveModel(w, r)
	case "activate":
		a.activateModel(w, r)
	case "delete":
		a.deleteModel(w, r)
	case "test":
		a.testModel(w, r)
	default:
		a.renderModelError(w, r, "未识别的模型操作。")
	}
}

func (a *App) saveModel(w http.ResponseWriter, r *http.Request) {
	id := uint(parseInt64Query(r.FormValue("id")))
	version := parseInt64Query(r.FormValue("version"))
	row := modelprofiles.Profile{
		ID:              id,
		Name:            strings.TrimSpace(r.FormValue("name")),
		BaseURL:         strings.TrimSpace(r.FormValue("base_url")),
		ModelName:       strings.TrimSpace(r.FormValue("model_name")),
		ContextWindow:   parseIntValue(r.FormValue("context_window")),
		ThinkingType:    strings.TrimSpace(r.FormValue("thinking_type")),
		ReasoningEffort: strings.TrimSpace(r.FormValue("reasoning_effort")),
		Version:         version,
	}
	var err error
	if id == 0 {
		_, err = a.modelStore.Create(r.Context(), row, r.FormValue("api_key"))
	} else {
		var updated modelprofiles.Profile
		updated, err = a.modelStore.Update(r.Context(), row, r.FormValue("api_key"))
		if err == nil && updated.Active && a.mumuAgent != nil && a.mumuAgent.Ready() {
			if reloadErr := a.mumuAgent.Reload(); reloadErr != nil {
				err = reloadErr
			}
		}
	}
	if err != nil {
		a.renderModelError(w, r, err.Error())
		return
	}
	a.redirectFlash(w, r, "/admin/settings/models", "success", "模型已保存", "")
}

func (a *App) activateModel(w http.ResponseWriter, r *http.Request) {
	id := uint(parseInt64Query(r.FormValue("id")))
	if id == 0 {
		a.renderModelError(w, r, "模型编号无效。")
		return
	}
	old, oldErr := a.modelStore.Active(r.Context())
	if oldErr != nil && !errors.Is(oldErr, modelprofiles.ErrNotFound) {
		a.renderModelError(w, r, oldErr.Error())
		return
	}
	if err := a.modelStore.SetActive(r.Context(), id); err != nil {
		a.renderModelError(w, r, err.Error())
		return
	}
	if a.mumuAgent != nil {
		if err := a.mumuAgent.Reload(); err != nil {
			// 回滚 active，保持旧运行态可用
			if oldErr == nil {
				_ = a.modelStore.SetActive(context.Background(), old.ID)
			} else {
				_ = a.modelStore.ClearActive(context.Background(), id)
			}
			a.renderModelError(w, r, "模型切换失败，已继续使用旧配置："+err.Error())
			return
		}
	}
	a.redirectFlash(w, r, "/admin/settings/models", "success", "已在下一轮使用新模型", "")
}

func (a *App) deleteModel(w http.ResponseWriter, r *http.Request) {
	id := uint(parseInt64Query(r.FormValue("id")))
	version := parseInt64Query(r.FormValue("version"))
	if id == 0 {
		a.renderModelError(w, r, "模型编号无效。")
		return
	}
	if err := a.modelStore.Delete(r.Context(), id, version); err != nil {
		a.renderModelError(w, r, err.Error())
		return
	}
	_ = version
	a.redirectFlash(w, r, "/admin/settings/models", "success", "模型已删除", "")
}

func (a *App) testModel(w http.ResponseWriter, r *http.Request) {
	id := uint(parseInt64Query(r.FormValue("id")))
	row, err := a.modelStore.Get(r.Context(), id)
	if err != nil {
		a.renderModelError(w, r, err.Error())
		return
	}
	key, err := a.modelStore.DecryptKey(row)
	if err != nil {
		a.renderModelError(w, r, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	err = llm.Test(ctx, llm.ModelConfig{
		APIKey: key, BaseURL: row.BaseURL, Model: row.ModelName,
		ThinkingType: row.ThinkingType, ReasoningEffort: row.ReasoningEffort,
	})
	if err != nil {
		a.renderModelError(w, r, err.Error())
		return
	}
	a.redirectFlash(w, r, "/admin/settings/models", "success", "模型连接正常", "")
}

func (a *App) renderModelError(w http.ResponseWriter, r *http.Request, message string) {
	a.renderStatus(w, http.StatusBadRequest, views.ModelsPage(a.modelsPageData(r, message), r.URL.Path))
}
