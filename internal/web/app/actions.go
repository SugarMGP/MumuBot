package app

import (
	"context"
	"errors"
	"net/http"
	neturl "net/url"
	"strings"

	"mumu-bot/internal/agent"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/auth"
	"mumu-bot/internal/web/views"

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
	case "sticker-delete":
		if err := a.admin.DeleteSticker(r.Context(), id); err != nil {
			a.respondActionError(w, r, http.StatusInternalServerError, &views.FlashMessage{Kind: "error", Title: "表情包删除失败", Body: deleteActionErrorText(err)})
			return
		}
		fallback = "/admin/stickers"
		flash = &views.FlashMessage{Kind: "success", Title: "表情包已删除"}
	default:
		a.respondActionError(w, r, http.StatusBadRequest, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "未识别这次操作。"})
		return
	}

	if err := a.respondActionSuccess(w, r, fallback, flash, a.renderActionTarget); err != nil {
		a.respondActionError(w, r, http.StatusInternalServerError, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "列表刷新失败，请稍后再试。"})
	}
}

func (a *App) handleContactAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.respondActionError(w, r, http.StatusBadRequest, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "请求格式不正确。"})
		return
	}

	var flash *views.FlashMessage
	switch r.FormValue("action") {
	case "block":
		kind := strings.TrimSpace(r.FormValue("kind"))
		targetID := parseInt64Query(r.FormValue("target_id"))
		if targetID <= 0 || (kind != memory.ConversationKindGroup && kind != memory.ConversationKindPrivate) {
			a.respondActionError(w, r, http.StatusBadRequest, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "会话参数无效。"})
			return
		}
		blocked := r.FormValue("blocked") == "true"
		if err := a.admin.SetConversationBlocked(r.Context(), kind, targetID, blocked); err != nil {
			a.respondActionError(w, r, http.StatusInternalServerError, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "更新拉黑状态失败，请稍后再试。"})
			return
		}
		if blocked {
			flash = &views.FlashMessage{Kind: "success", Title: "已拉黑该会话"}
		} else {
			flash = &views.FlashMessage{Kind: "success", Title: "已解除拉黑"}
		}
	case "extra-prompt":
		targetID := parseInt64Query(r.FormValue("target_id"))
		if targetID <= 0 {
			a.respondActionError(w, r, http.StatusBadRequest, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "群聊参数无效。"})
			return
		}
		if err := a.admin.SetConversationExtraPrompt(r.Context(), targetID, strings.TrimSpace(r.FormValue("extra_prompt"))); err != nil {
			a.respondActionError(w, r, http.StatusInternalServerError, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "保存群聊提示失败，请稍后再试。"})
			return
		}
		flash = &views.FlashMessage{Kind: "success", Title: "群聊提示已保存"}
	case "friend-request":
		requestID, err := parseUintParam(r.FormValue("request_id"))
		approve := r.FormValue("approve") == "true"
		if err != nil || requestID == 0 || a.mumuAgent == nil {
			a.respondActionError(w, r, http.StatusBadRequest, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "好友申请参数无效。"})
			return
		}
		if err := a.mumuAgent.HandleFriendRequest(r.Context(), requestID, approve); err != nil {
			flash := &views.FlashMessage{Kind: "error", Title: "好友申请处理失败", Body: truncateErrorDetail(err.Error())}
			// 记录已经被清理时列表已经变化，提示失败的同时刷新页面，避免残留失效行
			if errors.Is(err, agent.ErrFriendRequestGone) || errors.Is(err, agent.ErrFriendRequestMissing) {
				a.respondActionErrorRefresh(w, r, "/admin/contacts", flash)
				return
			}
			a.respondActionError(w, r, http.StatusBadRequest, flash)
			return
		}
		if approve {
			flash = &views.FlashMessage{Kind: "success", Title: "已同意好友申请"}
		} else {
			flash = &views.FlashMessage{Kind: "success", Title: "已拒绝好友申请"}
		}
	default:
		a.respondActionError(w, r, http.StatusBadRequest, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "未识别的操作。"})
		return
	}

	if err := a.respondActionSuccess(w, r, "/admin/contacts", flash, a.renderActionTarget); err != nil {
		a.respondActionError(w, r, http.StatusInternalServerError, &views.FlashMessage{Kind: "error", Title: "操作失败", Body: "列表刷新失败，请稍后再试。"})
	}
}

// truncateErrorDetail 限制错误详情长度，避免超长报错撑爆提示
func truncateErrorDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	runes := []rune(detail)
	if len(runes) > 240 {
		return string(runes[:240]) + "…"
	}
	return detail
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

// enabledGroups 返回当前启用且未被拉黑的群号，由需要群数据的页面各自调用，不混入运行时快照
func (a *App) enabledGroups() []int64 {
	if a.cfg == nil || a.memMgr == nil {
		return nil
	}
	rows, err := a.memMgr.ListConversationTargets(context.Background(), memory.ConversationKindGroup, true)
	if err != nil {
		return nil
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		if !row.Blocked {
			ids = append(ids, row.TargetID)
		}
	}
	return ids
}

func (a *App) runtimeSnapshot() RuntimeSnapshot {
	snapshot := RuntimeSnapshot{}
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
	case "/admin/stickers":
		data, err := a.stickerPageData(current, nil)
		if err != nil {
			return nil, err
		}
		return views.PageContent(views.StickerListBody(data)), nil
	case "/admin/contacts":
		data, err := a.contactsPageData(context.Background(), nil)
		if err != nil {
			return nil, err
		}
		return views.PageContent(views.ContactsBody(data)), nil
	default:
		return views.PageContent(templ.NopComponent), nil
	}
}

// respondActionErrorRefresh 用于失败但列表已经变化的场景：刷新目标页面的同时用 toast 说明失败原因
func (a *App) respondActionErrorRefresh(w http.ResponseWriter, r *http.Request, fallback string, flash *views.FlashMessage) {
	target := a.actionTargetURL(r, fallback)
	component, err := a.renderActionTarget(target)
	if err != nil {
		a.respondActionError(w, r, http.StatusInternalServerError, flash)
		return
	}
	if trigger, err := actionTriggerHeader(flash, false); err == nil && trigger != "" {
		w.Header().Set("HX-Trigger", trigger)
	}
	a.renderStatus(w, http.StatusOK, component)
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
