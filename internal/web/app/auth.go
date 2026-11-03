package app

import (
	"net/http"
	"strings"

	"mumu-bot/internal/version"
	"mumu-bot/internal/web/auth"
)

func (a *App) handleSessionInfo(w http.ResponseWriter, r *http.Request) {
	authenticated := false
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		authenticated = a.auth.IsAuthenticated(cookie.Value)
	}
	respondData(w, http.StatusOK, sessionResponse{
		Enabled:       a.auth.Enabled(),
		Authenticated: authenticated,
		Version:       version.String(),
	}, nil)
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.auth.Enabled() {
		respondError(w, http.StatusServiceUnavailable, "admin_disabled", "后台未启用，请先配置管理员密钥。", nil)
		return
	}
	var body struct {
		AdminKey string `json:"admin_key"`
	}
	if err := decodeJSON(r, &body); err != nil {
		a.failBadRequest(w, "请求格式不正确。")
		return
	}
	if !a.auth.CheckKey(strings.TrimSpace(body.AdminKey)) {
		respondError(w, http.StatusUnauthorized, "invalid_key", "密钥错误", nil)
		return
	}
	token, expiresAt, err := a.auth.CreateSession()
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		a.auth.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) requireAdminEnabled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.auth.Enabled() {
			respondError(w, http.StatusServiceUnavailable, "admin_disabled", "后台未启用，请先配置管理员密钥。", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.SessionCookieName)
		if err != nil || !a.auth.IsAuthenticated(cookie.Value) {
			respondError(w, http.StatusUnauthorized, "unauthorized", "登录已失效，请重新登录。", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOriginWrite 对所有写请求做同源校验，配合 SameSite=Lax 与 JSON 请求体抵御跨站提交
func (a *App) sameOriginWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			// 非浏览器客户端或部分代理不带 Origin，交由 Cookie 与 JSON 约束兜底
			next.ServeHTTP(w, r)
			return
		}
		if !sameHost(origin, r.Host) {
			respondError(w, http.StatusForbidden, "forbidden", "请求来源无效。", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameHost(origin string, host string) bool {
	origin = strings.TrimSpace(origin)
	host = strings.TrimSpace(host)
	if origin == "" || host == "" {
		return false
	}
	return origin == "http://"+host || origin == "https://"+host
}
