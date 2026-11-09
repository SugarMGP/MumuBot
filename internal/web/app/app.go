package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"mumu-bot/internal/agent"
	"mumu-bot/internal/config"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelprofiles"
	"mumu-bot/internal/web/assets"
	"mumu-bot/internal/web/auth"
	"mumu-bot/internal/web/services"

	"github.com/go-chi/chi/v5"
)

type runtimeSnapshot struct {
	Connected    bool
	AgentReady   bool
	SelfID       int64
	MCPToolCount int
	MCPStatuses  []mcp.ServerStatus
}

type App struct {
	boot       config.Bootstrap
	admin      *services.AdminService
	auth       *auth.Manager
	memMgr     *memory.Manager
	mumuAgent  atomic.Pointer[agent.Agent]
	botStatus  func() (bool, int64)
	modelStore *modelprofiles.Store
	mcpStore   *mcp.Store
	router     http.Handler
}

const (
	defaultListPageSize = 15
	compactListPageSize = 12
)

func New(boot config.Bootstrap, admin *services.AdminService, memMgr *memory.Manager, mumuAgent *agent.Agent, models *modelprofiles.Store, mcpServers *mcp.Store) *App {
	app := &App{
		boot: boot, admin: admin, auth: auth.NewManager(boot.WebAdminKey, 24*time.Hour),
		memMgr: memMgr, modelStore: models, mcpStore: mcpServers,
	}
	if mumuAgent != nil {
		app.mumuAgent.Store(mumuAgent)
	}
	app.router = app.routes()
	return app
}

// SetAgent 在 QQ 就绪后挂上运行态；后台在此之前已经可用
func (a *App) SetAgent(mumuAgent *agent.Agent) {
	a.mumuAgent.Store(mumuAgent)
}

// currentAgent 并发安全地读取当前运行态
func (a *App) currentAgent() *agent.Agent { return a.mumuAgent.Load() }

// SetBotStatus 让后台在 Agent 尚未建立时也能读到 OneBot 的真实连接状态
func (a *App) SetBotStatus(status func() (bool, int64)) { a.botStatus = status }

func (a *App) Addr() string {
	return fmt.Sprintf(":%d", a.boot.ServerPort)
}

func (a *App) Server() *http.Server {
	return &http.Server{
		Addr:    a.Addr(),
		Handler: a.router,
	}
}

func (a *App) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(a.sameOriginWrite)

	r.Get("/health", a.handleHealth)
	r.Get("/favicon.svg", a.handleFavicon)
	r.Handle("/assets/*", http.StripPrefix("/assets/", assets.Handler()))

	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/session", a.handleSessionInfo)
		api.Post("/session", a.handleLogin)
		api.Delete("/session", a.handleLogout)

		api.Group(func(protected chi.Router) {
			protected.Use(a.requireAdminEnabled, a.requireSession)

			protected.Get("/dashboard", a.handleDashboard)
			protected.Get("/stickers", a.handleStickers)
			protected.Delete("/stickers/{id}", a.handleStickerDelete)
			protected.Get("/stickers/files/*", a.handleStickerFile)
			protected.Get("/topics", a.handleTopics)
			protected.Get("/topics/conversations", a.handleTopicConversations)
			protected.Get("/topics/{id}", a.handleTopicDetail)
			protected.Get("/memories", a.handleMemories)
			protected.Get("/memories/{id}", a.handleMemoryDetail)
			protected.Patch("/memories/{id}", a.handleMemoryUpdate)
			protected.Delete("/memories/{id}", a.handleMemoryDelete)
			protected.Get("/members", a.handleMembers)
			protected.Get("/life", a.handleLife)
			protected.Get("/life/workers/{id}/history", a.handleWorkerHistory)
			protected.Get("/life/workers/{id}/entries/{entryID}", a.handleWorkerEntry)
			protected.Post("/life/schedules", a.handleLifeScheduleCreate)
			protected.Patch("/life/schedules/{id}", a.handleLifeScheduleUpdate)
			protected.Post("/life/wake", a.handleLifeWake)
			protected.Post("/life/run", a.handleLifeRun)
			protected.Get("/diaries", a.handleDiaryMonth)
			protected.Get("/diaries/{date}", a.handleDiaryDay)
			protected.Get("/conversations", a.handleConversations)
			protected.Patch("/conversations/blocked", a.handleConversationsBlocked)
			protected.Patch("/conversations/{kind}/{targetID}", a.handleConversationUpdate)
			protected.Get("/identity", a.handleIdentity)
			protected.Get("/identity/friends", a.handleIdentityFriends)
			protected.Patch("/identity", a.handleIdentityUpdate)
			protected.Get("/personas", a.handlePersonas)
			protected.Post("/personas/preview", a.handlePersonaPreview)
			protected.Get("/personas/template", a.handlePersonaTemplate)
			protected.Get("/personas/{id}", a.handlePersonaDetail)
			protected.Post("/personas", a.handlePersonaCreate)
			protected.Patch("/personas/{id}", a.handlePersonaUpdate)
			protected.Delete("/personas/{id}", a.handlePersonaDelete)
			protected.Post("/personas/{id}/activate", a.handlePersonaActivate)
			protected.Post("/personas/{id}/clone", a.handlePersonaClone)
			protected.Get("/settings", a.handleSettings)
			protected.Put("/settings/{section}", a.handleSettingsSave)
			protected.Post("/settings/{section}/reset", a.handleSettingsReset)
			protected.Get("/models", a.handleModels)
			protected.Post("/models", a.handleModelCreate)
			protected.Patch("/models/{id}", a.handleModelUpdate)
			protected.Delete("/models/{id}", a.handleModelDelete)
			protected.Put("/models/selection", a.handleModelSelection)
			protected.Post("/models/{id}/test", a.handleModelTest)
			protected.Get("/mcp-servers", a.handleMCPServers)
			protected.Get("/mcp-servers/{id}", a.handleMCPServerDetail)
			protected.Post("/mcp-servers", a.handleMCPServerCreate)
			protected.Patch("/mcp-servers/{id}", a.handleMCPServerUpdate)
			protected.Delete("/mcp-servers/{id}", a.handleMCPServerDelete)
			protected.Get("/system", a.handleSystem)
			protected.Get("/system/model-stats", a.handleModelStats)
			protected.Get("/system/logs/stream", a.handleLogStream)
			protected.Get("/system/logs/download", a.handleLogDownload)
		})
	})

	r.Get("/", a.handleRoot)
	r.Get("/login", a.handleSPA)
	r.Get("/admin", a.handleSPA)
	r.Get("/admin/*", a.handleSPA)
	r.NotFound(a.handleNotFound)
	return r
}

func (a *App) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"ok","name":"mumu-bot","time":%q}`, time.Now().Format(time.RFC3339))))
}

func (a *App) handleFavicon(w http.ResponseWriter, r *http.Request) {
	favicon, err := assets.Open("favicon.svg")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(favicon)
}

func (a *App) handleSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	index, err := assets.Open("index.html")
	if err != nil {
		http.Error(w, "管理后台资源缺失，请重新构建。", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(index)
}

func (a *App) handleNotFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		respondError(w, http.StatusNotFound, "not_found", messageNotFound, nil)
		return
	}
	http.NotFound(w, r)
}

func (a *App) handleStickerFile(w http.ResponseWriter, r *http.Request) {
	if a.admin == nil {
		http.NotFound(w, r)
		return
	}
	baseDir := strings.TrimSpace(a.admin.StickerDir())
	rawPath := strings.TrimSpace(chi.URLParam(r, "*"))
	if baseDir == "" || rawPath == "" {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	file, err := root.Open(rawPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (a *App) enabledGroups() []int64 {
	if a.memMgr == nil {
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

func (a *App) runtimeSnapshot() runtimeSnapshot {
	snapshot := runtimeSnapshot{}
	if a.botStatus != nil {
		snapshot.Connected, snapshot.SelfID = a.botStatus()
	}
	if agent := a.currentAgent(); agent != nil {
		snapshot.Connected = agent.OneBotConnected()
		snapshot.AgentReady = agent.Ready()
		snapshot.SelfID = agent.BotSelfID()
		snapshot.MCPToolCount = agent.MCPToolCount()
		snapshot.MCPStatuses = agent.MCPStatuses()
	}
	return snapshot
}

func (a *App) agentReady() bool {
	agent := a.currentAgent()
	return agent != nil && agent.Ready()
}
