package app

import (
	"fmt"
	"net/http"
	"strings"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/views"
)

const (
	personaCardLimit = 10000
	personaSimLimit  = 5000
)

// personasPageData 组装人格预设页；submitted 非空时用用户刚提交的内容回填表单
func (a *App) personasPageData(r *http.Request, errorText string, submitted *views.PersonaPresetView) (views.PersonasPageData, error) {
	data := views.PersonasPageData{
		CardLimit: personaCardLimit,
		SimLimit:  personaSimLimit,
		Error:     errorText,
		Flash:     a.flashFromRequest(r),
	}
	if a.memMgr == nil {
		return data, nil
	}
	presets, err := a.memMgr.ListPersonaPresets(r.Context())
	if err != nil {
		return views.PersonasPageData{}, err
	}
	for _, preset := range presets {
		data.Presets = append(data.Presets, personaPresetView(preset))
	}
	if submitted != nil {
		submitted.CardChars = len([]rune(submitted.PersonaCard))
		submitted.SimChars = len([]rune(submitted.Simulation))
		data.Selected = *submitted
		data.Creating = submitted.ID == 0
		return data, nil
	}
	if r.URL.Query().Get("new") == "1" {
		card, simulation := config.BuiltinPersonaTexts()
		data.Creating = true
		data.Selected = views.PersonaPresetView{
			PersonaCard: card,
			Simulation:  simulation,
			CardChars:   len([]rune(card)),
			SimChars:    len([]rune(simulation)),
		}
		return data, nil
	}
	wanted := uint(parseInt64Query(r.URL.Query().Get("id")))
	for _, preset := range data.Presets {
		if preset.ID == wanted || (wanted == 0 && preset.Active) {
			data.Selected = preset
			break
		}
	}
	if data.Selected.ID == 0 && len(data.Presets) > 0 {
		data.Selected = data.Presets[0]
	}
	return data, nil
}

func personaPresetView(preset memory.PersonaPreset) views.PersonaPresetView {
	return views.PersonaPresetView{
		ID:          preset.ID,
		Name:        preset.Name,
		Description: preset.Description,
		PersonaCard: preset.PersonaCard,
		Simulation:  preset.Simulation,
		Builtin:     preset.Builtin,
		Active:      preset.Active,
		Version:     preset.Version,
		UpdatedAt:   preset.UpdatedAt,
		CardChars:   len([]rune(preset.PersonaCard)),
		SimChars:    len([]rune(preset.Simulation)),
	}
}

func (a *App) handlePersonas(w http.ResponseWriter, r *http.Request) {
	data, err := a.personasPageData(r, "", nil)
	if err != nil {
		http.Error(w, "人格预设加载失败，请稍后再试。", http.StatusInternalServerError)
		return
	}
	a.renderPageResponse(w, r, views.PersonasPage(data, r.URL.Path), views.PageContent(views.PersonasBody(data)))
}

func (a *App) handlePersonasAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.renderPersonaError(w, r, "请求格式不正确。", nil)
		return
	}
	action := strings.TrimSpace(r.FormValue("action"))
	id := uint(parseInt64Query(r.FormValue("id")))
	name := strings.TrimSpace(r.FormValue("name"))
	description := strings.TrimSpace(r.FormValue("description"))
	card := r.FormValue("persona_card")
	simulation := r.FormValue("simulation_prompt")
	submitted := &views.PersonaPresetView{
		ID:          id,
		Name:        name,
		Description: description,
		PersonaCard: card,
		Simulation:  simulation,
		Version:     parseInt64Query(r.FormValue("version")),
	}
	redirect := "/admin/personas"
	var err error
	switch action {
	case "activate":
		err = a.memMgr.ActivatePersonaPreset(r.Context(), id)
	case "delete":
		err = a.memMgr.DeletePersonaPreset(r.Context(), id)
	case "clone":
		var created *memory.PersonaPreset
		created, err = a.memMgr.ClonePersonaPreset(r.Context(), id)
		if created != nil {
			redirect = fmt.Sprintf("/admin/personas?id=%d", created.ID)
		}
	case "create":
		var created *memory.PersonaPreset
		created, err = a.memMgr.CreatePersonaPreset(r.Context(), memory.PersonaPreset{
			Name:        name,
			Description: description,
			PersonaCard: card,
			Simulation:  simulation,
		})
		if created != nil {
			redirect = fmt.Sprintf("/admin/personas?id=%d", created.ID)
		}
	case "save":
		err = a.memMgr.UpdatePersonaPreset(r.Context(), id, name, description, card, simulation, submitted.Version)
		if err == nil {
			redirect = fmt.Sprintf("/admin/personas?id=%d", id)
		}
	default:
		err = fmt.Errorf("未知操作")
	}
	if err != nil {
		a.renderPersonaError(w, r, err.Error(), submitted)
		return
	}
	if err := a.reloadPersona(); err != nil {
		a.renderPersonaError(w, r, "已保存，但运行态应用失败："+err.Error(), submitted)
		return
	}
	a.redirectFlash(w, r, redirect, "success", personaActionText(action), "")
}

func (a *App) reloadPersona() error {
	if err := config.Reload(); err != nil {
		return err
	}
	if a.mumuAgent != nil && a.mumuAgent.Ready() {
		return a.mumuAgent.Reload()
	}
	return nil
}

func (a *App) renderPersonaError(w http.ResponseWriter, r *http.Request, text string, submitted *views.PersonaPresetView) {
	data, err := a.personasPageData(r, text, submitted)
	if err != nil {
		http.Error(w, "人格预设加载失败，请稍后再试。", http.StatusInternalServerError)
		return
	}
	a.renderPageResponse(w, r, views.PersonasPage(data, "/admin/personas"), views.PageContent(views.PersonasBody(data)))
}

func personaActionText(action string) string {
	switch action {
	case "activate":
		return "已切换人格预设，下一段对话总结后生效"
	case "delete":
		return "已删除这份预设"
	case "clone":
		return "已克隆一份预设"
	case "create":
		return "已新建预设"
	default:
		return "已保存预设"
	}
}
