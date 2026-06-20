package app

import (
	"context"
	"errors"
	"fmt"
	"mumu-bot/internal/agent"
	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/assets"
	"mumu-bot/internal/web/auth"
	"mumu-bot/internal/web/services"
	"mumu-bot/internal/web/views"
	"net/http"
	neturl "net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/a-h/templ"
	"github.com/bytedance/sonic"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
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

func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := a.admin.OverviewStats()
	if err != nil {
		http.Error(w, "总览加载失败，请稍后再试。", http.StatusInternalServerError)
		return
	}

	snapshot := a.runtimeSnapshot()
	if snapshot.EnabledGroups == 0 {
		snapshot.EnabledGroups = countEnabledGroups(a.cfg.Groups)
	}
	if snapshot.CurrentMood == nil {
		if mood, err := a.loadCurrentMood(); err == nil {
			snapshot.CurrentMood = mood
		}
	}
	if !snapshot.LearningOn {
		snapshot.LearningOn = a.cfg.Learning.Enabled
	}
	flash := a.flashFromRequest(r)

	a.render(w, views.DashboardPage(views.DashboardPageData{
		BotName:           a.cfg.Persona.Name,
		EnabledGroupCount: snapshot.EnabledGroups,
		MemoryCount:       stats.MemoryCount,
		MemberCount:       stats.MemberCount,
		JargonCount:       stats.JargonCount,
		StyleCardCount:    stats.StyleCardCount,
		StickerCount:      stats.StickerCount,
		OneBotConnected:   snapshot.Connected,
		SelfID:            snapshot.SelfID,
		MCPToolCount:      snapshot.MCPToolCount,
		LearningEnabled:   snapshot.LearningOn,
		CurrentMood:       snapshot.CurrentMood,
		Flash:             flash,
	}, r.URL.Path))
}

func (a *App) handleStyleCards(w http.ResponseWriter, r *http.Request) {
	data, err := a.styleCardPageData(r.URL, a.flashFromRequest(r))
	if err != nil {
		http.Error(w, "风格卡片列表加载失败，请稍后再试。", http.StatusInternalServerError)
		return
	}

	a.renderPageResponse(w, r, views.StyleCardListPage(data, r.URL.Path), views.PageContent(views.StyleCardListBody(data)))
}

func (a *App) handleJargons(w http.ResponseWriter, r *http.Request) {
	data, err := a.jargonPageData(r.URL, a.flashFromRequest(r))
	if err != nil {
		http.Error(w, "黑话列表加载失败，请稍后再试。", http.StatusInternalServerError)
		return
	}

	a.renderPageResponse(w, r, views.JargonListPage(data, r.URL.Path), views.PageContent(views.JargonListBody(data)))
}

func (a *App) handleStickers(w http.ResponseWriter, r *http.Request) {
	data, err := a.stickerPageData(r.URL, a.flashFromRequest(r))
	if err != nil {
		http.Error(w, "表情包列表加载失败，请稍后再试。", http.StatusInternalServerError)
		return
	}

	a.renderPageResponse(w, r, views.StickerListPage(data, r.URL.Path), views.PageContent(views.StickerListBody(data)))
}

func (a *App) handleMemories(w http.ResponseWriter, r *http.Request) {
	data, err := a.memoryPageData(r.URL, a.flashFromRequest(r))
	if err != nil {
		http.Error(w, "记忆列表加载失败，请稍后再试。", http.StatusInternalServerError)
		return
	}

	a.renderPageResponse(w, r, views.MemoryListPage(data, r.URL.Path), views.PageContent(views.MemoryListBody(data)))
}

func (a *App) handleTopics(w http.ResponseWriter, r *http.Request) {
	data, err := a.topicPageData(r.URL, a.flashFromRequest(r))
	if err != nil {
		http.Error(w, "话题列表加载失败，请稍后再试。", http.StatusInternalServerError)
		return
	}

	a.renderPageResponse(w, r, views.TopicListPage(data, r.URL.Path), views.PageContent(views.TopicListBody(data)))
}

func (a *App) handleTopicDetail(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	data, err := a.topicDetailPageData(id, a.flashFromRequest(r))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "话题详情加载失败，请稍后再试。", http.StatusInternalServerError)
		return
	}

	a.render(w, views.TopicDetailPage(data, r.URL.Path))
}

func (a *App) handleMembers(w http.ResponseWriter, r *http.Request) {
	sortKey, order := services.NormalizeMemberSort(r.URL.Query().Get("sort"), r.URL.Query().Get("order"))
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	pageSize := listPageSize(r.URL.Query().Get("page_size"))
	filter := services.MemberFilter{
		Keyword:  strings.TrimSpace(r.URL.Query().Get("keyword")),
		Sort:     sortKey,
		Order:    order,
		Page:     page,
		PageSize: pageSize,
	}

	result, err := a.admin.ListMemberProfiles(filter)
	if err != nil {
		http.Error(w, "成员列表加载失败，请稍后再试。", http.StatusInternalServerError)
		return
	}

	data := views.MemberListPageData{
		Keyword: filter.Keyword,
		Sort:    buildSortToolbar(r.URL, sortKey, order, []sortOption{{Key: "messages", Label: "发言数"}, {Key: "activity", Label: "活跃度"}, {Key: "intimacy", Label: "亲密度"}, {Key: "recent", Label: "最近发言"}, {Key: "updated", Label: "最近更新"}}),
		Items:   result.Items,
		Meta:    a.listMeta(r.URL, result.Page, result.PageSize, result.Total),
		Flash:   a.flashFromRequest(r),
	}

	a.renderPageResponse(w, r, views.MemberListPage(data, r.URL.Path), views.PageContent(views.MemberListBody(data)))
}

func (a *App) handleSystem(w http.ResponseWriter, r *http.Request) {
	a.render(w, views.SystemPage(views.SystemPageData{
		Sections: a.systemSections(),
		Flash:    a.flashFromRequest(r),
	}, r.URL.Path))
}

func (a *App) handleActionDialogFragment(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(r.URL.Query().Get("action_id"))
	if err != nil {
		a.renderStatus(w, http.StatusOK, views.DialogErrorContent("admin-action-dialog", "操作无法继续", "记录编号无效，请刷新列表后再试。"))
		return
	}

	kind := strings.TrimSpace(r.URL.Query().Get("action_kind"))
	returnTo := a.dialogReturnTo(r, "/admin")

	switch kind {
	case "style-card-status":
		item, err := a.admin.GetStyleCard(id)
		if err != nil {
			a.renderStatus(w, http.StatusOK, views.DialogErrorContent("admin-action-dialog", "风格卡片无法加载", "这条记录可能已经被处理。"))
			return
		}
		data, ok := views.StyleCardActionDialogData(item, r.URL.Query().Get("status"), returnTo)
		if !ok {
			a.renderStatus(w, http.StatusOK, views.DialogErrorContent("admin-action-dialog", "操作无法继续", "当前状态下不能执行这次操作。"))
			return
		}
		a.render(w, views.AdminActionDialogContent(data))
	case "jargon-status":
		item, err := a.admin.GetJargon(id)
		if err != nil {
			a.renderStatus(w, http.StatusOK, views.DialogErrorContent("admin-action-dialog", "黑话记录无法加载", "这条记录可能已经被处理。"))
			return
		}
		data, ok := views.JargonActionDialogData(item, r.URL.Query().Get("status"), returnTo)
		if !ok {
			a.renderStatus(w, http.StatusOK, views.DialogErrorContent("admin-action-dialog", "操作无法继续", "当前状态下不能执行这次操作。"))
			return
		}
		a.render(w, views.AdminActionDialogContent(data))
	case "sticker-delete":
		item, err := a.admin.GetSticker(id)
		if err != nil {
			a.renderStatus(w, http.StatusOK, views.DialogErrorContent("admin-action-dialog", "表情包无法加载", "这张图片可能已经被删除。"))
			return
		}
		a.render(w, views.AdminActionDialogContent(views.StickerDeleteDialogData(item, returnTo)))
	case "memory-delete":
		item, err := a.admin.GetMemory(id)
		if err != nil {
			a.renderStatus(w, http.StatusOK, views.DialogErrorContent("admin-action-dialog", "记忆无法加载", "这条记忆可能已经被删除。"))
			return
		}
		a.render(w, views.AdminActionDialogContent(views.MemoryDeleteDialogData(item, returnTo)))
	case "memory-archive":
		item, err := a.admin.GetMemory(id)
		if err != nil {
			a.renderStatus(w, http.StatusOK, views.DialogErrorContent("admin-action-dialog", "记忆无法加载", "这条记忆可能已经不存在。"))
			return
		}
		a.render(w, views.AdminActionDialogContent(views.MemoryArchiveDialogData(item, returnTo)))
	case "memory-restore":
		item, err := a.admin.GetMemory(id)
		if err != nil {
			a.renderStatus(w, http.StatusOK, views.DialogErrorContent("admin-action-dialog", "记忆无法加载", "这条记忆可能已经不存在。"))
			return
		}
		a.render(w, views.AdminActionDialogContent(views.MemoryRestoreDialogData(item, returnTo)))
	default:
		a.renderStatus(w, http.StatusOK, views.DialogErrorContent("admin-action-dialog", "操作无法继续", "未识别这次操作。"))
	}
}

func (a *App) handleStickerPreviewDialogFragment(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		a.renderStatus(w, http.StatusOK, views.DialogErrorContent("admin-sticker-preview-dialog", "预览无法加载", "图片编号无效，请刷新列表后再试。"))
		return
	}

	item, err := a.admin.GetSticker(id)
	if err != nil {
		a.renderStatus(w, http.StatusOK, views.DialogErrorContent("admin-sticker-preview-dialog", "预览无法加载", "这张图片可能已经被删除。"))
		return
	}

	a.render(w, views.StickerPreviewDialog(views.StickerPreviewDialogDataForItem(item)))
}

func (a *App) styleCardPageData(current *neturl.URL, flash *views.FlashMessage) (views.StyleCardListPageData, error) {
	sortKey, order := services.NormalizeStyleCardSort(current.Query().Get("sort"), current.Query().Get("order"))
	page := parsePositiveInt(current.Query().Get("page"), 1)
	pageSize := listPageSize(current.Query().Get("page_size"))
	filter := services.StyleCardFilter{
		GroupID:  parseInt64Query(current.Query().Get("group_id")),
		Status:   strings.TrimSpace(current.Query().Get("status")),
		Keyword:  strings.TrimSpace(current.Query().Get("keyword")),
		Sort:     sortKey,
		Order:    order,
		Page:     page,
		PageSize: pageSize,
	}

	result, err := a.admin.ListStyleCards(filter)
	if err != nil {
		return views.StyleCardListPageData{}, err
	}

	return views.StyleCardListPageData{
		GroupID: current.Query().Get("group_id"),
		Status:  filter.Status,
		Keyword: filter.Keyword,
		Sort:    buildSortToolbar(current, sortKey, order, []sortOption{{Key: "updated", Label: "最近更新"}, {Key: "created", Label: "创建时间"}, {Key: "use", Label: "使用次数"}, {Key: "evidence", Label: "证据数"}}),
		Items:   result.Items,
		Meta:    a.listMeta(current, result.Page, result.PageSize, result.Total),
		Flash:   flash,
	}, nil
}

func (a *App) jargonPageData(current *neturl.URL, flash *views.FlashMessage) (views.JargonListPageData, error) {
	sortKey, order := services.NormalizeJargonSort(current.Query().Get("sort"), current.Query().Get("order"))
	page := parsePositiveInt(current.Query().Get("page"), 1)
	pageSize := listPageSize(current.Query().Get("page_size"))
	filter := services.JargonFilter{
		GroupID:  parseInt64Query(current.Query().Get("group_id")),
		Status:   strings.TrimSpace(current.Query().Get("status")),
		Keyword:  strings.TrimSpace(current.Query().Get("keyword")),
		Sort:     sortKey,
		Order:    order,
		Page:     page,
		PageSize: pageSize,
	}

	result, err := a.admin.ListJargons(filter)
	if err != nil {
		return views.JargonListPageData{}, err
	}

	return views.JargonListPageData{
		GroupID: current.Query().Get("group_id"),
		Status:  filter.Status,
		Keyword: filter.Keyword,
		Sort:    buildSortToolbar(current, sortKey, order, []sortOption{{Key: "updated", Label: "最近更新"}, {Key: "created", Label: "创建时间"}, {Key: "group", Label: "群号"}}),
		Items:   result.Items,
		Meta:    a.listMeta(current, result.Page, result.PageSize, result.Total),
		Flash:   flash,
	}, nil
}

func (a *App) stickerPageData(current *neturl.URL, flash *views.FlashMessage) (views.StickerListPageData, error) {
	sortKey, order := services.NormalizeStickerSort(current.Query().Get("sort"), current.Query().Get("order"))
	page := parsePositiveInt(current.Query().Get("page"), 1)
	pageSize := listPageSize(current.Query().Get("page_size"))
	filter := services.StickerFilter{
		Keyword:  strings.TrimSpace(current.Query().Get("keyword")),
		Sort:     sortKey,
		Order:    order,
		Page:     page,
		PageSize: pageSize,
	}

	result, err := a.admin.ListStickers(filter)
	if err != nil {
		return views.StickerListPageData{}, err
	}

	return views.StickerListPageData{
		Keyword: filter.Keyword,
		Sort:    buildSortToolbar(current, sortKey, order, []sortOption{{Key: "use", Label: "使用次数"}, {Key: "updated", Label: "最近更新"}, {Key: "created", Label: "创建时间"}}),
		Items:   result.Items,
		Meta:    a.listMeta(current, result.Page, result.PageSize, result.Total),
		Flash:   flash,
	}, nil
}

func (a *App) memoryPageData(current *neturl.URL, flash *views.FlashMessage) (views.MemoryListPageData, error) {
	sortKey, order := services.NormalizeMemorySort(current.Query().Get("sort"), current.Query().Get("order"))
	page := parsePositiveInt(current.Query().Get("page"), 1)
	pageSize := listPageSize(current.Query().Get("page_size"))
	filter := services.MemoryFilter{
		GroupID:       parseInt64Query(current.Query().Get("group_id")),
		Type:          strings.TrimSpace(current.Query().Get("type")),
		Status:        strings.TrimSpace(current.Query().Get("status")),
		CanonicalType: strings.TrimSpace(current.Query().Get("canonical_type")),
		SourceKind:    strings.TrimSpace(current.Query().Get("source_kind")),
		Keyword:       strings.TrimSpace(current.Query().Get("keyword")),
		Sort:          sortKey,
		Order:         order,
		Page:          page,
		PageSize:      pageSize,
	}

	result, err := a.admin.ListMemories(filter)
	if err != nil {
		return views.MemoryListPageData{}, err
	}

	return views.MemoryListPageData{
		GroupID:       current.Query().Get("group_id"),
		Type:          filter.Type,
		Status:        filter.Status,
		CanonicalType: filter.CanonicalType,
		SourceKind:    filter.SourceKind,
		SourceOptions: []views.FilterChoice{
			{Label: "全部来源", Value: ""},
			{Label: "主动记住", Value: "message"},
			{Label: "话题沉淀", Value: "topic"},
		},
		Keyword: filter.Keyword,
		Sort:    buildSortToolbar(current, sortKey, order, []sortOption{{Key: "updated", Label: "最近更新"}, {Key: "created", Label: "创建时间"}, {Key: "access", Label: "访问量"}, {Key: "importance", Label: "重要度"}, {Key: "evidence", Label: "证据数"}}),
		Items:   result.Items,
		Meta:    a.listMeta(current, result.Page, result.PageSize, result.Total),
		Flash:   flash,
	}, nil
}

func (a *App) topicPageData(current *neturl.URL, flash *views.FlashMessage) (views.TopicListPageData, error) {
	sortKey, order := services.NormalizeTopicSort(current.Query().Get("sort"), current.Query().Get("order"))
	page := parsePositiveInt(current.Query().Get("page"), 1)
	pageSize := listPageSize(current.Query().Get("page_size"))
	filter := services.TopicFilter{
		GroupID:  parseInt64Query(current.Query().Get("group_id")),
		Status:   strings.TrimSpace(current.Query().Get("status")),
		Keyword:  strings.TrimSpace(current.Query().Get("keyword")),
		Sort:     sortKey,
		Order:    order,
		Page:     page,
		PageSize: pageSize,
	}

	result, err := a.admin.ListTopicThreads(filter)
	if err != nil {
		return views.TopicListPageData{}, err
	}

	return views.TopicListPageData{
		GroupID: current.Query().Get("group_id"),
		Status:  filter.Status,
		Keyword: filter.Keyword,
		Sort:    buildSortToolbar(current, sortKey, order, []sortOption{{Key: "recent", Label: "最近消息"}, {Key: "updated", Label: "最近更新"}, {Key: "created", Label: "创建时间"}, {Key: "group", Label: "群号"}}),
		Items:   result.Items,
		Meta:    a.listMeta(current, result.Page, result.PageSize, result.Total),
		Flash:   flash,
	}, nil
}

func (a *App) topicDetailPageData(id uint, flash *views.FlashMessage) (views.TopicDetailPageData, error) {
	thread, err := a.admin.GetTopicThread(id)
	if err != nil {
		return views.TopicDetailPageData{}, err
	}
	messages, err := a.admin.ListTopicMessages(id, 80)
	if err != nil {
		return views.TopicDetailPageData{}, err
	}

	return views.TopicDetailPageData{
		Thread:         thread,
		SummaryChanges: views.TopicSummaryChanges(thread),
		Messages:       messages,
		Flash:          flash,
	}, nil
}

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

func (a *App) loadCurrentMood() (*memory.MoodState, error) {
	if a.admin == nil || a.admin.DB() == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	var mood memory.MoodState
	if err := a.admin.DB().First(&mood).Error; err != nil {
		return nil, err
	}
	return &mood, nil
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

func (a *App) systemSections() []views.SystemSection {
	cfg := a.cfg
	snapshot := a.runtimeSnapshot()

	groupIDs := make([]string, 0, len(cfg.Groups))
	for _, group := range cfg.Groups {
		if group.Enabled {
			groupIDs = append(groupIDs, fmt.Sprintf("%d", group.GroupID))
		}
	}
	groupSummary := "暂未启用群聊"
	if len(groupIDs) > 0 {
		groupSummary = strings.Join(groupIDs, "、")
	}

	appendField := func(fields []views.SystemField, label string, value string) []views.SystemField {
		value = strings.TrimSpace(value)
		if value == "" || value == "-" {
			return fields
		}
		return append(fields, views.SystemField{Label: label, Value: value})
	}

	personaFields := make([]views.SystemField, 0, 5)
	personaFields = appendField(personaFields, "名称", emptyDash(cfg.Persona.Name))
	if cfg.Persona.QQ > 0 {
		personaFields = append(personaFields, views.SystemField{Label: "QQ", Value: fmt.Sprintf("%d", cfg.Persona.QQ)})
	}
	personaFields = appendField(personaFields, "别名", joinOrDash(cfg.Persona.AliasNames))

	groupFields := []views.SystemField{
		{Label: "启用群数", Value: fmt.Sprintf("%d / %d", countEnabledGroups(cfg.Groups), len(cfg.Groups))},
		{Label: "已启用群聊", Value: groupSummary},
		{Label: "观察窗口", Value: fmt.Sprintf("%d 秒", cfg.Agent.ObserveWindow)},
		{Label: "思考间隔", Value: fmt.Sprintf("%d 秒", cfg.Agent.ThinkInterval)},
	}
	if cfg.Agent.ThinkDebounceMS > 0 {
		groupFields = append(groupFields, views.SystemField{Label: "聚合窗口", Value: fmt.Sprintf("%d 毫秒", cfg.Agent.ThinkDebounceMS)})
	}
	if cfg.Learning.Enabled {
		groupFields = append(groupFields, views.SystemField{Label: "自动学习", Value: fmt.Sprintf("每 %d 分钟整理 %d 条消息", cfg.Learning.IntervalMinutes, cfg.Learning.BatchSize)})
		if cfg.Learning.ReviewIntervalMinutes > 0 {
			groupFields = append(groupFields, views.SystemField{Label: "审核节奏", Value: fmt.Sprintf("每 %d 分钟整理一次待审内容", cfg.Learning.ReviewIntervalMinutes)})
		}
	}

	modelFields := make([]views.SystemField, 0, 8)
	modelFields = appendField(modelFields, llm.TierDisplayName(llm.TierHigh), cfg.ModelTiers.High.Model)
	modelFields = appendField(modelFields, llm.TierDisplayName(llm.TierMid), cfg.ModelTiers.Mid.Model)
	modelFields = appendField(modelFields, llm.TierDisplayName(llm.TierLow), cfg.ModelTiers.Low.Model)
	modelFields = appendField(modelFields, "记忆检索", cfg.Embedding.Model)
	if cfg.VisionLLM.Enabled {
		modelFields = appendField(modelFields, "图片理解", cfg.VisionLLM.Model)
	}
	if snapshot.MCPToolCount > 0 {
		modelFields = append(modelFields, views.SystemField{Label: "扩展工具", Value: fmt.Sprintf("%d 个", snapshot.MCPToolCount)})
	}

	runtimeFields := []views.SystemField{
		{Label: "当前连接", Value: connectionText(snapshot.Connected)},
		{Label: "重连间隔", Value: fmt.Sprintf("%d 秒", cfg.OneBot.ReconnectInterval)},
	}
	if snapshot.SelfID > 0 {
		runtimeFields = append(runtimeFields, views.SystemField{Label: "机器人 QQ", Value: fmt.Sprintf("%d", snapshot.SelfID)})
	}
	if strings.TrimSpace(cfg.Memory.MySQL.Host) != "" {
		runtimeFields = append(runtimeFields, views.SystemField{Label: "记忆存储", Value: "MySQL"})
	}
	if strings.TrimSpace(cfg.Memory.Milvus.Address) != "" {
		runtimeFields = append(runtimeFields, views.SystemField{Label: "向量检索", Value: "Milvus"})
	}
	if cfg.Memory.Milvus.VectorDim > 0 {
		runtimeFields = append(runtimeFields, views.SystemField{Label: "向量维度", Value: fmt.Sprintf("%d", cfg.Memory.Milvus.VectorDim)})
	}
	runtimeFields = appendField(runtimeFields, "相似度算法", metricTypeText(cfg.Memory.Milvus.MetricType))
	if cfg.Sticker.MaxSizeMB > 0 {
		runtimeFields = append(runtimeFields, views.SystemField{Label: "表情包大小上限", Value: fmt.Sprintf("%d MB", cfg.Sticker.MaxSizeMB)})
	}
	if cfg.Sticker.AutoSave {
		runtimeFields = append(runtimeFields, views.SystemField{Label: "表情包收集", Value: "自动保存"})
	}

	sections := make([]views.SystemSection, 0, 4)
	if len(personaFields) > 0 {
		sections = append(sections, views.SystemSection{Title: "人格设定", Fields: personaFields})
	}
	if len(groupFields) > 0 {
		sections = append(sections, views.SystemSection{Title: "群聊与学习", Fields: groupFields})
	}
	if len(modelFields) > 0 {
		sections = append(sections, views.SystemSection{Title: "模型能力", Fields: modelFields})
	}
	if len(runtimeFields) > 0 {
		sections = append(sections, views.SystemSection{Title: "连接与数据", Fields: runtimeFields})
	}
	return sections
}

func isSafeAdminTarget(target *neturl.URL) bool {
	if target == nil {
		return false
	}
	if target.IsAbs() || strings.TrimSpace(target.Host) != "" {
		return false
	}
	cleanPath := strings.TrimSpace(target.Path)
	if cleanPath == "" {
		return false
	}
	return strings.HasPrefix(cleanPath, "/admin")
}

func normalizeAdminTarget(raw string, requestHost string) (*neturl.URL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}

	target, err := neturl.Parse(raw)
	if err != nil || target.Path == "" {
		return nil, false
	}

	if target.IsAbs() || strings.TrimSpace(target.Host) != "" {
		if !strings.EqualFold(strings.TrimSpace(target.Host), strings.TrimSpace(requestHost)) {
			return nil, false
		}
		if target.Scheme != "" && target.Scheme != "http" && target.Scheme != "https" {
			return nil, false
		}
	}

	normalized := &neturl.URL{Path: target.Path, RawQuery: target.RawQuery}
	if !isSafeAdminTarget(normalized) {
		return nil, false
	}
	return normalized, true
}

func withPage(current *neturl.URL, page int) string {
	cloned := *current
	query := cloned.Query()
	query.Set("page", strconv.Itoa(page))
	cloned.RawQuery = query.Encode()
	if cloned.RawPath != "" {
		return cloned.RawPath + "?" + cloned.RawQuery
	}
	if cloned.RawQuery == "" {
		return cloned.Path
	}
	return cloned.Path + "?" + cloned.RawQuery
}

func withFlash(target string, kind string, title string, body string) string {
	parsed, err := neturl.Parse(target)
	if err != nil {
		return target
	}
	query := parsed.Query()
	query.Set("flash_kind", kind)
	query.Set("flash_title", title)
	if strings.TrimSpace(body) != "" {
		query.Set("flash_body", body)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func actionTriggerHeader(flash *views.FlashMessage, closeDialog bool) (string, error) {
	if flash == nil && !closeDialog {
		return "", nil
	}

	payload := make(map[string]any)
	if flash != nil {
		payload["admin:toast"] = map[string]string{
			"kind":  strings.TrimSpace(flash.Kind),
			"title": strings.TrimSpace(flash.Title),
			"body":  strings.TrimSpace(flash.Body),
		}
	}
	if closeDialog {
		payload["admin:action-dialog-close"] = true
	}

	encoded, err := sonic.MarshalString(payload)
	if err != nil {
		return "", err
	}
	return asciiHeaderJSON(encoded), nil
}

func asciiHeaderJSON(raw string) string {
	var builder strings.Builder
	builder.Grow(len(raw))

	for _, r := range raw {
		if r <= 127 {
			builder.WriteRune(r)
			continue
		}
		if r <= 0xFFFF {
			fmt.Fprintf(&builder, "\\u%04x", r)
			continue
		}
		for _, unit := range utf16.Encode([]rune{r}) {
			fmt.Fprintf(&builder, "\\u%04x", unit)
		}
	}

	return builder.String()
}

func metricTypeText(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "COSINE":
		return "余弦相似度"
	case "L2":
		return "欧氏距离"
	case "IP":
		return "内积"
	default:
		if strings.TrimSpace(raw) == "" {
			return ""
		}
		return "其他算法"
	}
}

func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func listPageSize(raw string) int {
	return parsePositiveInt(raw, defaultListPageSize)
}

func parseInt64Query(raw string) int64 {
	value, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return value
}

func parseUintParam(raw string) (uint, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	return uint(value), err
}

func countEnabledGroups(groups []config.GroupConfig) int {
	total := 0
	for _, group := range groups {
		if group.Enabled {
			total++
		}
	}
	return total
}

func joinOrDash(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, "、")
}

func emptyDash(value string) string {
	return views.EmptyDash(value)
}

func connectionText(value bool) string {
	return views.ConnectionText(value)
}

func styleCardActionErrorText(err error) string {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "这张风格卡片不存在或已经被处理。"
	}
	if strings.Contains(strings.ToLower(err.Error()), "invalid") {
		return "这次状态变更无效，请刷新列表后重试。"
	}
	return "更新失败，请稍后再试。"
}

func jargonActionErrorText(err error) string {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "这条黑话不存在或已经被处理。"
	}
	if strings.Contains(strings.ToLower(err.Error()), "invalid") {
		return "这次状态变更无效，请刷新列表后重试。"
	}
	return "更新失败，请稍后再试。"
}

func deleteActionErrorText(err error) string {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "这条记录不存在或已经被删除。"
	}
	return "删除失败，请稍后再试。"
}

func buildSortToolbar(current *neturl.URL, currentSort string, currentOrder string, options []sortOption) views.SortToolbarData {
	data := views.SortToolbarData{
		CurrentSort:  currentSort,
		CurrentOrder: currentOrder,
		Options:      make([]views.SortToolbarLink, 0, len(options)),
		OrderOptions: make([]views.SortToolbarLink, 0, 2),
	}

	activeSortLabel := ""
	for _, option := range options {
		active := option.Key == currentSort
		if active {
			activeSortLabel = option.Label
		}
		data.Options = append(data.Options, views.SortToolbarLink{
			Label:  option.Label,
			Href:   withQueryValues(current, true, map[string]string{"sort": option.Key}),
			Active: active,
		})
	}
	if activeSortLabel == "" && len(options) > 0 {
		activeSortLabel = options[0].Label
	}

	orderLabel := "倒序"
	for _, option := range []struct {
		value string
		label string
	}{
		{value: "desc", label: "倒序"},
		{value: "asc", label: "正序"},
	} {
		active := option.value == currentOrder
		if active {
			orderLabel = option.label
		}
		data.OrderOptions = append(data.OrderOptions, views.SortToolbarLink{
			Label:  option.label,
			Href:   withQueryValues(current, true, map[string]string{"order": option.value}),
			Active: active,
		})
	}

	data.Summary = fmt.Sprintf("当前按%s%s查看", activeSortLabel, orderLabel)
	return data
}

func withQueryValues(current *neturl.URL, resetPage bool, updates map[string]string) string {
	cloned := *current
	query := cloned.Query()
	if resetPage {
		query.Set("page", "1")
	}
	for key, value := range updates {
		if strings.TrimSpace(value) == "" {
			query.Del(key)
			continue
		}
		query.Set(key, value)
	}
	cloned.RawQuery = query.Encode()
	if cloned.RawPath != "" {
		if cloned.RawQuery == "" {
			return cloned.RawPath
		}
		return cloned.RawPath + "?" + cloned.RawQuery
	}
	if cloned.RawQuery == "" {
		return cloned.Path
	}
	return cloned.Path + "?" + cloned.RawQuery
}
