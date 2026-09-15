package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/services"
	"mumu-bot/internal/web/views"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

func (a *App) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("view") == "graph" || (q.Get("view") == "" && (q.Get("focus_id") != "" || q.Get("selected_id") != "")) {
		a.handleKnowledgeGraph(w, r)
		return
	}
	workspace, err := a.knowledgeWorkspace(r, "list")
	if err != nil {
		http.Error(w, "记忆筛选加载失败", 500)
		return
	}
	f := workspace.Filter
	sortKey, order := f.Sort, f.Order
	result, err := a.admin.ListKnowledge(f)
	if err != nil {
		http.Error(w, "记忆列表加载失败，请稍后再试。", 500)
		return
	}
	metadata, err := a.admin.KnowledgeMetadata(r.Context(), result.Items)
	if err != nil {
		http.Error(w, "原文依据加载失败", 500)
		return
	}
	data := views.KnowledgeListPageData{Workspace: workspace, Metadata: metadata, Items: result.Items, SelfID: a.runtimeSnapshot().SelfID, Meta: a.listMeta(r.URL, result.Page, result.PageSize, result.Total), Flash: a.flashFromRequest(r), Sort: buildSortToolbar(r.URL, sortKey, order, []sortOption{{Key: "updated", Label: "最近更新"}, {Key: "created", Label: "创建时间"}})}
	a.renderPageResponse(w, r, views.KnowledgeListPage(data, r.URL.Path), views.PageContent(views.KnowledgeListBody(data)))
}

func (a *App) handleKnowledgeGraph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	workspace, err := a.knowledgeWorkspace(r, "graph")
	if err != nil {
		http.Error(w, "记忆筛选加载失败", 500)
		return
	}
	f := workspace.Filter
	groupID := f.GroupID
	focusID, _ := parseUintParam(q.Get("focus_id"))
	graph, err := a.admin.GroupKnowledgeGraph(r.Context(), f, q.Get("focus_kind"), focusID)
	if err != nil {
		http.Error(w, "图谱加载失败，请稍后再试", 500)
		return
	}
	data := views.KnowledgeGraphPageData{Graph: graph, Workspace: workspace, Flash: a.flashFromRequest(r)}
	kind := q.Get("selected_kind")
	id, _ := parseUintParam(q.Get("selected_id"))
	related, _ := parseUintParam(q.Get("selected_related"))
	if id == 0 && focusID > 0 && (q.Get("focus_kind") == "knowledge" || q.Get("focus_kind") == "topic") {
		kind, id = q.Get("focus_kind"), focusID
	}
	if id > 0 {
		selection, e := a.admin.GraphSelection(r.Context(), groupID, kind, id, related, 0)
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			http.Error(w, "依据加载失败，请稍后再试", 500)
			return
		}
		if e == nil {
			data.Panel = views.GraphPanel(selection, groupID, a.runtimeSnapshot().SelfID, kind, id, related, 0)
			preserveGraphReturn(&data.Panel, workspace.CurrentURL)
		}
	}
	a.renderPageResponse(w, r, views.KnowledgeGraphPage(data, r.URL.Path), views.PageContent(views.KnowledgeGraphBody(data)))
}

func (a *App) handleGraphPanel(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	groupID := parseInt64Query(q.Get("group_id"))
	kind := q.Get("kind")
	id, err := parseUintParam(q.Get("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	related, _ := parseUintParam(q.Get("related"))
	offset := max(0, parsePositiveInt(q.Get("offset"), 0))
	selection, err := a.admin.GraphSelection(r.Context(), groupID, kind, id, related, offset)
	if err != nil {
		http.Error(w, "该对象暂不可读取，请刷新后重试", http.StatusNotFound)
		return
	}
	panel := views.GraphPanel(selection, groupID, a.runtimeSnapshot().SelfID, kind, id, related, offset)
	preserveGraphReturn(&panel, a.knowledgeReturn(r, panel.ReturnTo))
	a.render(w, views.KnowledgeGraphPanel(panel))
}

func (a *App) handleKnowledgeDetail(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	detail, err := a.admin.KnowledgeDetail(r.Context(), id, max(1, parsePositiveInt(r.URL.Query().Get("page"), 1)))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "记忆详情加载失败，请稍后再试。", 500)
		}
		return
	}
	a.render(w, views.KnowledgeDetailPage(views.KnowledgeDetailPageData{Detail: detail, ReturnTo: a.knowledgeReturn(r, "/admin/knowledge"), CurrentURL: views.WithQuery(r.URL.RequestURI(), "return_to", a.knowledgeReturn(r, "/admin/knowledge")), SelfID: a.runtimeSnapshot().SelfID, Flash: a.flashFromRequest(r)}, r.URL.Path))
}

func (a *App) handleKnowledgeAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "请求格式不正确。", 400)
		return
	}
	var err error
	kind := r.FormValue("action_kind")
	if kind == "note-clear" {
		groupID := parseInt64Query(r.FormValue("group_id"))
		if groupID <= 0 {
			http.Error(w, "群号无效。", 400)
			return
		}
		err = a.memMgr.SaveWorkingNote(r.Context(), groupID, "")
	} else {
		id, e := parseUintParam(r.FormValue("action_id"))
		if e != nil {
			http.Error(w, "记录编号无效。", 400)
			return
		}
		switch kind {
		case "knowledge-status":
			err = a.admin.UpdateKnowledgeStatus(r.Context(), id, r.FormValue("status"))
		case "relation-status":
			err = a.admin.UpdateRelationStatus(r.Context(), id, r.FormValue("status"))
		default:
			http.Error(w, "未识别这次操作。", 400)
			return
		}
	}
	target := a.actionTargetURL(r, "/admin/knowledge")
	if err != nil {
		message := "记录可能已变化，或缺少完整有效证据。请刷新后核对。"
		var validation *memory.ValidationError
		if errors.As(err, &validation) {
			message = validation.Message
		}
		http.Redirect(w, r, withFlash(target.String(), "error", "操作未完成", message), http.StatusSeeOther)
		return
	}
	title := "记忆状态已更新"
	if kind == "note-clear" {
		title = "群便签已清除"
	}
	if kind == "relation-status" {
		title = "关联状态已更新"
	}
	http.Redirect(w, r, withFlash(target.String(), "success", title, ""), http.StatusSeeOther)
}
func (a *App) knowledgeWorkspace(r *http.Request, view string) (views.KnowledgeWorkspaceData, error) {
	q := r.URL.Query()
	status := q.Get("status")
	if status == "all" {
		status = ""
	}
	kind := q.Get("kind")
	if view == "list" && kind == "topic" {
		kind = ""
	}
	sortKey, order := services.NormalizeMemorySort(q.Get("sort"), q.Get("order"))
	f := services.KnowledgeFilter{MemoryFilter: services.MemoryFilter{GroupID: parseInt64Query(q.Get("group_id")), Kind: kind, Status: status, Keyword: strings.TrimSpace(q.Get("keyword")), Sort: sortKey, Order: order, Page: parsePositiveInt(q.Get("page"), 1), PageSize: listPageSize(q.Get("page_size"))}, UserID: parseInt64Query(q.Get("user_id")), AuthorID: parseInt64Query(q.Get("author_id"))}
	groups, err := a.admin.KnowledgeGroups(r.Context())
	data := views.KnowledgeWorkspaceData{Filter: f, Groups: groups, View: view, CurrentURL: views.WithQuery(r.URL.RequestURI(), "view", view, "status", status, "kind", kind, "subject", "", "evidence", "", "return_to", "", "flash_kind", "", "flash_title", "", "flash_body", "")}
	if err == nil && f.GroupID > 0 {
		data.Note, err = a.memMgr.GetWorkingNote(r.Context(), f.GroupID)
	}
	return data, err
}

func (a *App) knowledgeReturn(r *http.Request, fallback string) string {
	if target, ok := normalizeAdminTarget(r.URL.Query().Get("return_to"), r.Host); ok {
		return target.String()
	}
	return fallback
}

func preserveGraphReturn(panel *views.GraphPanelData, current string) {
	panel.ReturnTo = views.WithQuery(current, "view", "graph", "selected_kind", panel.NodeKind, "selected_id", fmt.Sprint(panel.ID), "selected_related", fmt.Sprint(panel.Related))
	for _, target := range []*string{&panel.DetailURL, &panel.PreviousURL, &panel.NextURL} {
		if *target != "" {
			*target = views.WithQuery(*target, "return_to", panel.ReturnTo)
		}
	}
	if panel.FocusURL != "" {
		focus, _ := url.Parse(panel.FocusURL)
		panel.FocusURL = views.WithQuery(panel.ReturnTo, "focus_kind", focus.Query().Get("focus_kind"), "focus_id", focus.Query().Get("focus_id"))
	}
}
