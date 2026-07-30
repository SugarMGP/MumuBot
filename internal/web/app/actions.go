package app

import (
	"context"
	"mumu-bot/internal/web/auth"
	"mumu-bot/internal/web/views"
	"net/http"
	neturl "net/url"
	"strings"

	"github.com/a-h/templ"
)

func (a *App) handleAdminAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.respondActionError(w, r, http.StatusBadRequest, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "请求格式不正确。"})
		return
	}

	id, err := parseUintParam(r.FormValue("action_id"))
	if err != nil {
		a.respondActionError(w, r, http.StatusBadRequest, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "记录编号无效。"})
		return
	}

	var (
		fallback string
		flash    *views.FlashMessage
	)

	switch strings.TrimSpace(r.FormValue("action_kind")) {
	case "style-card-status":
		if err := a.admin.UpdateStyleCardStatus(id, r.FormValue("status")); err != nil {
			a.respondActionError(w, r, http.StatusBadRequest, &views.FlashMessage{Kind: "error", Title: "风格卡片状态更新失败", Body: styleCardActionErrorText(err)})
			return
		}
		fallback = "/admin/style-cards"
		flash = &views.FlashMessage{Kind: "success", Title: "风格卡片状态已更新"}
	case "jargon-status":
		if err := a.admin.UpdateJargonStatus(id, r.FormValue("status")); err != nil {
			a.respondActionError(w, r, http.StatusBadRequest, &views.FlashMessage{Kind: "error", Title: "黑话状态更新失败", Body: jargonActionErrorText(err)})
			return
		}
		fallback = "/admin/jargons"
		flash = &views.FlashMessage{Kind: "success", Title: "黑话状态已更新"}
	case "sticker-delete":
		if err := a.admin.DeleteSticker(id); err != nil {
			a.respondActionError(w, r, http.StatusInternalServerError, &views.FlashMessage{Kind: "error", Title: "表情包删除失败", Body: deleteActionErrorText(err)})
			return
		}
		fallback = "/admin/stickers"
		flash = &views.FlashMessage{Kind: "success", Title: "表情包已删除"}
	case "memory-delete":
		if err := a.admin.DeleteMemory(id); err != nil {
			a.respondActionError(w, r, http.StatusInternalServerError, &views.FlashMessage{Kind: "error", Title: "记忆删除失败", Body: deleteActionErrorText(err)})
			return
		}
		fallback = "/admin/memories"
		flash = &views.FlashMessage{Kind: "success", Title: "记忆已删除"}
	case "memory-archive":
		if err := a.admin.ArchiveMemory(id); err != nil {
			a.respondActionError(w, r, http.StatusInternalServerError, &views.FlashMessage{Kind: "error", Title: "记忆归档失败", Body: deleteActionErrorText(err)})
			return
		}
		fallback = "/admin/memories"
		flash = &views.FlashMessage{Kind: "success", Title: "记忆已归档"}
	case "memory-restore":
		if err := a.admin.RestoreMemoryToCandidate(id); err != nil {
			a.respondActionError(w, r, http.StatusInternalServerError, &views.FlashMessage{Kind: "error", Title: "记忆恢复失败", Body: deleteActionErrorText(err)})
			return
		}
		fallback = "/admin/memories"
		flash = &views.FlashMessage{Kind: "success", Title: "记忆已恢复到待收敛"}
	default:
		a.respondActionError(w, r, http.StatusBadRequest, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "未识别这次操作。"})
		return
	}

	if err := a.respondActionSuccess(w, r, fallback, flash, a.renderActionTarget); err != nil {
		a.respondActionError(w, r, http.StatusInternalServerError, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "列表刷新失败，请稍后再试。"})
	}
}

func (a *App) requireAdminEnabled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.auth.Enabled() {
			a.renderStatus(w, http.StatusServiceUnavailable, views.DisabledPage())
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.SessionCookieName)
		if err != nil || !a.auth.IsAuthenticated(cookie.Value) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) sameOriginPostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin != "" {
				originURL, err := neturl.Parse(origin)
				if err != nil || originURL.Host != r.Host || (originURL.Scheme != "http" && originURL.Scheme != "https") {
					http.Error(w, "请求来源无效。", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) render(w http.ResponseWriter, component templ.Component) {
	a.renderStatus(w, http.StatusOK, component)
}

func (a *App) renderStatus(w http.ResponseWriter, status int, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = component.Render(context.Background(), w)
}

func (a *App) renderPageResponse(w http.ResponseWriter, r *http.Request, full templ.Component, body templ.Component) {
	if isHTMXRequest(r) {
		a.render(w, body)
		return
	}
	a.render(w, full)
}

func (a *App) runtimeSnapshot() RuntimeSnapshot {
	snapshot := RuntimeSnapshot{}
	if a.cfg != nil {
		snapshot.LearningOn = a.cfg.Learning.Enabled
		snapshot.EnabledGroups = countEnabledGroups(a.cfg.Groups)
	}
	if a.mumuAgent != nil {
		snapshot.Connected = a.mumuAgent.OneBotConnected()
		snapshot.SelfID = a.mumuAgent.BotSelfID()
		snapshot.MCPToolCount = a.mumuAgent.MCPToolCount()
	}
	if a.memMgr != nil {
		if mood, err := a.memMgr.GetMoodState(); err == nil {
			snapshot.CurrentMood = mood
		}
	}
	return snapshot
}

func (a *App) listMeta(current *neturl.URL, page, pageSize int, total int64) views.ListMeta {
	meta := views.ListMeta{
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}
	if page > 1 {
		meta.PrevURL = withPage(current, page-1)
	}
	if int64(page*pageSize) < total {
		meta.NextURL = withPage(current, page+1)
	}
	return meta
}

func isHTMXRequest(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("HX-Request")), "true")
}

func (a *App) dialogReturnTo(r *http.Request, fallback string) string {
	candidates := []string{
		strings.TrimSpace(r.Header.Get("HX-Current-URL")),
		strings.TrimSpace(r.Header.Get("Referer")),
		strings.TrimSpace(fallback),
	}

	for _, candidate := range candidates {
		if target, ok := normalizeAdminTarget(candidate, r.Host); ok {
			return target.String()
		}
	}

	return fallback
}

func (a *App) actionTargetURL(r *http.Request, fallback string) *neturl.URL {
	fallbackURL, ok := normalizeAdminTarget(fallback, r.Host)
	if !ok {
		fallbackURL = &neturl.URL{Path: "/admin"}
	}

	candidates := []string{
		strings.TrimSpace(r.FormValue("return_to")),
		strings.TrimSpace(r.Header.Get("Referer")),
		strings.TrimSpace(fallback),
	}

	for _, candidate := range candidates {
		if target, ok := normalizeAdminTarget(candidate, r.Host); ok {
			return target
		}
	}

	return &neturl.URL{Path: fallbackURL.Path, RawQuery: fallbackURL.RawQuery}
}

func (a *App) respondActionSuccess(w http.ResponseWriter, r *http.Request, fallback string, flash *views.FlashMessage, render func(current *neturl.URL) (templ.Component, error)) error {
	target := a.actionTargetURL(r, fallback)
	if !isHTMXRequest(r) {
		if flash == nil {
			http.Redirect(w, r, target.String(), http.StatusSeeOther)
			return nil
		}
		http.Redirect(w, r, withFlash(target.String(), flash.Kind, flash.Title, flash.Body), http.StatusSeeOther)
		return nil
	}

	component, err := render(target)
	if err != nil {
		return err
	}

	if trigger, err := actionTriggerHeader(flash, true); err == nil && trigger != "" {
		w.Header().Set("HX-Trigger", trigger)
	}
	a.renderStatus(w, http.StatusOK, component)
	return nil
}

func (a *App) renderActionTarget(current *neturl.URL) (templ.Component, error) {
	switch current.Path {
	case "/admin/style-cards":
		data, err := a.styleCardPageData(current, nil)
		if err != nil {
			return nil, err
		}
		return views.PageContent(views.StyleCardListBody(data)), nil
	case "/admin/jargons":
		data, err := a.jargonPageData(current, nil)
		if err != nil {
			return nil, err
		}
		return views.PageContent(views.JargonListBody(data)), nil
	case "/admin/stickers":
		data, err := a.stickerPageData(current, nil)
		if err != nil {
			return nil, err
		}
		return views.PageContent(views.StickerListBody(data)), nil
	case "/admin/memories":
		data, err := a.memoryPageData(current, nil)
		if err != nil {
			return nil, err
		}
		return views.PageContent(views.MemoryListBody(data)), nil
	default:
		return views.PageContent(templ.NopComponent), nil
	}
}

func (a *App) respondActionError(w http.ResponseWriter, r *http.Request, status int, flash *views.FlashMessage) {
	if !isHTMXRequest(r) {
		message := "请求失败"
		if flash != nil {
			message = strings.TrimSpace(flash.Title)
			if body := strings.TrimSpace(flash.Body); body != "" {
				message = body
			}
		}
		http.Error(w, message, status)
		return
	}

	if trigger, err := actionTriggerHeader(flash, false); err == nil && trigger != "" {
		w.Header().Set("HX-Trigger", trigger)
	}
	w.Header().Set("HX-Reswap", "none")
	w.WriteHeader(status)
}

func (a *App) flashFromRequest(r *http.Request) *views.FlashMessage {
	title := strings.TrimSpace(r.URL.Query().Get("flash_title"))
	if title == "" {
		return nil
	}
	return &views.FlashMessage{
		Kind:  strings.TrimSpace(r.URL.Query().Get("flash_kind")),
		Title: title,
		Body:  strings.TrimSpace(r.URL.Query().Get("flash_body")),
	}
}
