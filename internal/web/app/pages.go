package app

import (
	"errors"
	"mumu-bot/internal/web/services"
	"mumu-bot/internal/web/views"
	"net/http"
	neturl "net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

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
	filter := services.ListFilter{
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
		Sort:    buildSortToolbar(r.URL, sortKey, order, []sortOption{{Key: "messages", Label: "发言数"}, {Key: "recent", Label: "最近发言"}}),
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
	filter := services.ListFilter{
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
		Sort:    buildSortToolbar(current, sortKey, order, []sortOption{{Key: "updated", Label: "最近更新"}, {Key: "created", Label: "创建时间"}}),
		Items:   result.Items,
		Meta:    a.listMeta(current, result.Page, result.PageSize, result.Total),
		Flash:   flash,
	}, nil
}

func (a *App) jargonPageData(current *neturl.URL, flash *views.FlashMessage) (views.JargonListPageData, error) {
	sortKey, order := services.NormalizeJargonSort(current.Query().Get("sort"), current.Query().Get("order"))
	page := parsePositiveInt(current.Query().Get("page"), 1)
	pageSize := listPageSize(current.Query().Get("page_size"))
	filter := services.ListFilter{
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
	filter := services.ListFilter{
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
		GroupID:  parseInt64Query(current.Query().Get("group_id")),
		Scope:    strings.TrimSpace(current.Query().Get("scope")),
		Status:   strings.TrimSpace(current.Query().Get("status")),
		Kind:     strings.TrimSpace(current.Query().Get("kind")),
		Keyword:  strings.TrimSpace(current.Query().Get("keyword")),
		Sort:     sortKey,
		Order:    order,
		Page:     page,
		PageSize: pageSize,
	}

	result, err := a.admin.ListMemories(filter)
	if err != nil {
		return views.MemoryListPageData{}, err
	}

	return views.MemoryListPageData{
		GroupID: current.Query().Get("group_id"),
		Scope:   filter.Scope,
		Status:  filter.Status,
		Kind:    filter.Kind,
		Keyword: filter.Keyword,
		Sort:    buildSortToolbar(current, sortKey, order, []sortOption{{Key: "updated", Label: "最近更新"}, {Key: "created", Label: "创建时间"}}),
		Items:   result.Items,
		Meta:    a.listMeta(current, result.Page, result.PageSize, result.Total),
		Flash:   flash,
	}, nil
}

func (a *App) topicPageData(current *neturl.URL, flash *views.FlashMessage) (views.TopicListPageData, error) {
	sortKey, order := services.NormalizeTopicSort(current.Query().Get("sort"), current.Query().Get("order"))
	page := parsePositiveInt(current.Query().Get("page"), 1)
	pageSize := listPageSize(current.Query().Get("page_size"))
	filter := services.ListFilter{
		GroupID:  parseInt64Query(current.Query().Get("group_id")),
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
		Status:  "",
		Keyword: filter.Keyword,
		Sort:    buildSortToolbar(current, sortKey, order, []sortOption{{Key: "recent", Label: "最近归属"}, {Key: "created", Label: "创建时间"}, {Key: "group", Label: "群号"}}),
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
