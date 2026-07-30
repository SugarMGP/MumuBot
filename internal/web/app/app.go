package app

import (
	"fmt"
	"mumu-bot/internal/agent"
	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/assets"
	"mumu-bot/internal/web/auth"
	"mumu-bot/internal/web/services"
	"mumu-bot/internal/web/views"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/go-chi/chi/v5"
)

type RuntimeSnapshot struct {
	Connected     bool
	SelfID        int64
	MCPToolCount  int
	CurrentMood   *memory.MoodState
	LearningOn    bool
	EnabledGroups int
}

type App struct {
	cfg       *config.Config
	admin     *services.AdminService
	auth      *auth.Manager
	memMgr    *memory.Manager
	mumuAgent *agent.Agent
	router    http.Handler
}

type sortOption struct {
	Key   string
	Label string
}

const defaultListPageSize = 15

func New(cfg *config.Config, admin *services.AdminService, memMgr *memory.Manager, mumuAgent *agent.Agent) *App {
	app := &App{
		cfg:       cfg,
		admin:     admin,
		auth:      auth.NewManager(cfg.Web.AdminKey, 24*time.Hour),
		memMgr:    memMgr,
		mumuAgent: mumuAgent,
	}
	app.router = app.routes()
	return app
}

func (a *App) Addr() string {
	return fmt.Sprintf(":%d", a.cfg.Server.Port)
}

func (a *App) Server() *http.Server {
	return &http.Server{
		Addr:    a.Addr(),
		Handler: a.router,
	}
}

func (a *App) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(a.sameOriginPostOnly)

	r.Get("/", a.handleRoot)
	r.Get("/health", a.handleHealth)
	r.Get("/favicon.ico", a.handleFavicon)
	r.Handle("/assets/*", http.StripPrefix("/assets/", assets.Handler()))
	r.Get("/login", a.handleLoginPage)
	r.Post("/login", a.handleLoginSubmit)
	r.Post("/logout", a.handleLogout)

	r.Group(func(protected chi.Router) {
		protected.Use(a.requireAdminEnabled)
		protected.Use(a.requireSession)

		protected.Get("/admin", a.handleDashboard)
		protected.Get("/admin/style-cards", a.handleStyleCards)
		protected.Get("/admin/jargons", a.handleJargons)
		protected.Get("/admin/stickers", a.handleStickers)
		protected.Get("/admin/stickers/files/*", a.handleStickerFile)
		protected.Get("/admin/topics", a.handleTopics)
		protected.Get("/admin/topics/{id}", a.handleTopicDetail)
		protected.Get("/admin/memories", a.handleMemories)
		protected.Get("/admin/members", a.handleMembers)
		protected.Get("/admin/system", a.handleSystem)
		protected.Get("/admin/dialogs/actions", a.handleActionDialogFragment)
		protected.Get("/admin/dialogs/stickers/{id}", a.handleStickerPreviewDialogFragment)

		protected.Post("/admin/actions", a.handleAdminAction)
	})

	return r
}

func (a *App) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = sonic.ConfigDefault.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"name":   "mumu-bot",
		"time":   time.Now().Format(time.RFC3339),
	})
}

func (a *App) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(views.FaviconSVG()))
}

func (a *App) handleStickerFile(w http.ResponseWriter, r *http.Request) {
	if a.admin == nil {
		http.NotFound(w, r)
		return
	}

	baseDir := strings.TrimSpace(a.admin.StickerDir())
	rawPath := strings.TrimSpace(chi.URLParam(r, "*"))
	if baseDir == "" || rawPath == "" || strings.Contains(rawPath, `\`) {
		http.NotFound(w, r)
		return
	}

	cleanPath := path.Clean("/" + rawPath)
	if cleanPath == "/" || strings.HasPrefix(cleanPath, "/../") || strings.HasSuffix(rawPath, "/") {
		http.NotFound(w, r)
		return
	}

	relativePath := strings.TrimPrefix(cleanPath, "/")
	filePath := filepath.Join(baseDir, filepath.FromSlash(relativePath))
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if absFile != absBase && !strings.HasPrefix(absFile, absBase+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}

	info, err := os.Stat(absFile)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, absFile)
}

func (a *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if !a.auth.Enabled() {
		a.renderStatus(w, http.StatusServiceUnavailable, views.DisabledPage())
		return
	}

	a.render(w, views.LoginPage(views.LoginPageData{
		Enabled: true,
		Error:   "",
	}))
}

func (a *App) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if !a.auth.Enabled() {
		a.renderStatus(w, http.StatusServiceUnavailable, views.DisabledPage())
		return
	}

	if err := r.ParseForm(); err != nil {
		a.renderStatus(w, http.StatusBadRequest, views.LoginPage(views.LoginPageData{
			Enabled: true,
			Error:   "请求格式错误",
		}))
		return
	}

	adminKey := strings.TrimSpace(r.FormValue("admin_key"))
	if !a.auth.CheckKey(adminKey) {
		a.renderStatus(w, http.StatusUnauthorized, views.LoginPage(views.LoginPageData{
			Enabled: true,
			Error:   "密钥错误",
		}))
		return
	}

	token, expiresAt, err := a.auth.CreateSession()
	if err != nil {
		http.Error(w, "登录失败，请稍后再试。", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
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
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
