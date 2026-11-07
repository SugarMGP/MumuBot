package app

import (
	"net/http"
	"strings"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"

	"github.com/go-chi/chi/v5"
)

const (
	personaCardLimit = 10000
	personaSimLimit  = 5000
	personaLifeLimit = 12000
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
		SimChars:    len([]rune(preset.Simulation)),
		LifeChars:   len([]rune(preset.LifePrompt)),
		UpdatedAt:   preset.UpdatedAt,
	}
}

func personaDetail(preset memory.PersonaPreset) personaDetailDTO {
	return personaDetailDTO{
		personaSummaryDTO: personaSummary(preset),
		PersonaCard:       preset.PersonaCard,
		SimulationPrompt:  preset.Simulation,
		LifePrompt:        preset.LifePrompt,
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
	card, simulation, lifePrompt := config.BuiltinPersonaTexts()
	respondData(w, http.StatusOK, personaTemplateResponse{
		PersonaCard:      card,
		SimulationPrompt: simulation,
		LifePrompt:       lifePrompt,
	}, nil)
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
	Name             string `json:"name"`
	Description      string `json:"description"`
	PersonaCard      string `json:"persona_card"`
	SimulationPrompt string `json:"simulation_prompt"`
	LifePrompt       string `json:"life_prompt"`
	Version          int64  `json:"version"`
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
	if strings.TrimSpace(body.PersonaCard) == "" || strings.TrimSpace(body.SimulationPrompt) == "" || strings.TrimSpace(body.LifePrompt) == "" {
		a.failBadRequest(w, "人格卡、仿真提示词与生活职责都不能为空。")
		return
	}
	if len([]rune(body.PersonaCard)) > personaCardLimit || len([]rune(body.SimulationPrompt)) > personaSimLimit || len([]rune(body.LifePrompt)) > personaLifeLimit {
		a.failBadRequest(w, "人格预设内容超过长度上限。")
		return
	}
	created, err := a.memMgr.CreatePersonaPreset(r.Context(), memory.PersonaPreset{
		Name:        strings.TrimSpace(body.Name),
		Description: strings.TrimSpace(body.Description),
		PersonaCard: body.PersonaCard,
		Simulation:  body.SimulationPrompt,
		LifePrompt:  body.LifePrompt,
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
	if strings.TrimSpace(body.PersonaCard) == "" || strings.TrimSpace(body.SimulationPrompt) == "" || strings.TrimSpace(body.LifePrompt) == "" {
		a.failBadRequest(w, "人格卡、仿真提示词与生活职责都不能为空。")
		return
	}
	if len([]rune(body.PersonaCard)) > personaCardLimit || len([]rune(body.SimulationPrompt)) > personaSimLimit || len([]rune(body.LifePrompt)) > personaLifeLimit {
		a.failBadRequest(w, "人格预设内容超过长度上限。")
		return
	}
	if err := a.memMgr.UpdatePersonaPreset(r.Context(), id, strings.TrimSpace(body.Name), strings.TrimSpace(body.Description), body.PersonaCard, body.SimulationPrompt, body.LifePrompt, body.Version); err != nil {
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
