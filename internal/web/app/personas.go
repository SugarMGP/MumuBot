package app

import (
	"net/http"
	"strings"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/persona"

	"github.com/go-chi/chi/v5"
)

func personaSummary(preset memory.PersonaPreset) personaSummaryDTO {
	return personaSummaryDTO{
		ID:          preset.ID,
		Name:        preset.Name,
		Description: preset.Description,
		Builtin:     preset.Builtin,
		Active:      preset.Active,
		Version:     preset.Version,
		CardChars:   len([]rune(preset.PersonaCard)),
		RulesChars:  len([]rune(preset.CommonRules)),
		QQChars:     len([]rune(preset.QQPrompt)),
		WorkerChars: len([]rune(preset.WorkerPrompt)),
		LifeChars:   len([]rune(preset.LifePrompt)),
		UpdatedAt:   preset.UpdatedAt,
	}
}

func personaDetail(preset memory.PersonaPreset) personaDetailDTO {
	return personaDetailDTO{
		personaSummaryDTO: personaSummary(preset),
		PersonaTexts:      preset.PersonaTexts,
	}
}

func (a *App) handlePersonas(w http.ResponseWriter, r *http.Request) {
	presets, err := a.memMgr.ListPersonaPresets(r.Context())
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	response := personasResponse{Items: make([]personaSummaryDTO, 0, len(presets))}
	for _, preset := range presets {
		if preset.Active {
			response.ActiveID = preset.ID
		}
		response.Items = append(response.Items, personaSummary(preset))
	}
	respondData(w, http.StatusOK, response, nil)
}

func (a *App) handlePersonaTemplate(w http.ResponseWriter, r *http.Request) {
	respondData(w, http.StatusOK, config.BuiltinPersonaTexts(), nil)
}

func (a *App) handlePersonaDetail(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	preset, err := a.memMgr.GetPersonaPreset(r.Context(), id)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	if preset == nil {
		a.failFromError(w, r, memory.ErrPersonaPresetNotFound)
		return
	}
	respondData(w, http.StatusOK, personaDetail(*preset), nil)
}

type personaInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	config.PersonaTexts
	Version int64 `json:"version"`
}

func (a *App) handlePersonaCreate(w http.ResponseWriter, r *http.Request) {
	var body personaInput
	if err := decodeJSON(r, &body); err != nil {
		a.failBadRequest(w, "请求格式不正确。")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		a.failBadRequest(w, "预设名称不能为空。")
		return
	}
	if err := body.PersonaTexts.Validate(); err != nil {
		respondError(w, http.StatusBadRequest, "persona_content_invalid", "", nil)
		return
	}
	created, err := a.memMgr.CreatePersonaPreset(r.Context(), memory.PersonaPreset{
		Name:         strings.TrimSpace(body.Name),
		Description:  strings.TrimSpace(body.Description),
		PersonaTexts: body.PersonaTexts,
	})
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusCreated, personaDetail(*created), a.reloadPersonaWarning())
}

func (a *App) handlePersonaUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	var body personaInput
	if err := decodeJSON(r, &body); err != nil {
		a.failBadRequest(w, "请求格式不正确。")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		a.failBadRequest(w, "预设名称不能为空。")
		return
	}
	if err := body.PersonaTexts.Validate(); err != nil {
		respondError(w, http.StatusBadRequest, "persona_content_invalid", "", nil)
		return
	}
	if err := a.memMgr.UpdatePersonaPreset(r.Context(), id, strings.TrimSpace(body.Name), strings.TrimSpace(body.Description), body.PersonaTexts, body.Version); err != nil {
		a.failFromError(w, r, err)
		return
	}
	preset, err := a.memMgr.GetPersonaPreset(r.Context(), id)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	if preset == nil {
		a.failFromError(w, r, memory.ErrPersonaPresetNotFound)
		return
	}
	respondData(w, http.StatusOK, personaDetail(*preset), a.reloadPersonaWarning())
}

func (a *App) handlePersonaDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	if err := a.memMgr.DeletePersonaPreset(r.Context(), id); err != nil {
		a.failFromError(w, r, err)
		return
	}
	_ = a.reloadPersonaWarning()
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handlePersonaActivate(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	if err := a.memMgr.ActivatePersonaPreset(r.Context(), id); err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, map[string]bool{"ok": true}, a.reloadPersonaWarning())
}

func (a *App) handlePersonaClone(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	created, err := a.memMgr.ClonePersonaPreset(r.Context(), id)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusCreated, personaDetail(*created), a.reloadPersonaWarning())
}

// reloadPersona 在写入后同步运行态；失败只返回警告，不否定已经落库的结果
func (a *App) reloadPersonaWarning() *warningPayload {
	if err := config.Reload(); err != nil {
		return &warningPayload{Code: "runtime_reload_failed"}
	}
	if agent := a.currentAgent(); agent != nil && agent.Ready() {
		if err := agent.Reload(); err != nil {
			return &warningPayload{Code: "runtime_reload_failed"}
		}
	}
	return nil
}

// handlePersonaPreview 使用草稿和当前全局身份装配，不保存或创建任何会话
func (a *App) handlePersonaPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		config.PersonaTexts
		Mode string `json:"mode"`
	}
	if err := decodeJSON(r, &body); err != nil {
		a.failBadRequest(w, "invalid request")
		return
	}
	if err := body.PersonaTexts.Validate(); err != nil {
		respondError(w, http.StatusBadRequest, "persona_content_invalid", "", nil)
		return
	}
	switch body.Mode {
	case persona.ModeGroup, persona.ModePrivate, persona.ModeLife, persona.ModeWorker:
	default:
		respondError(w, http.StatusBadRequest, "validation_failed", "", nil)
		return
	}
	p, err := persona.NewPersona(&config.PersonaConfig{PersonaTexts: body.PersonaTexts}, config.Snapshot().Identity)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, map[string]string{"text": p.Instruction(body.Mode)}, nil)
}
