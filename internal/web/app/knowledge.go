package app

import (
	"errors"
	"net/http"
	"strings"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/services"
	"mumu-bot/internal/web/views"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

func (a *App) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("view") != "list" && q.Get("user_id") == "" && q.Get("author_id") == "" && q.Get("subject") == "" {
		a.handleKnowledgeGraph(w, r)
		return
	}
	sortKey, order := services.NormalizeMemorySort(q.Get("sort"), q.Get("order"))
	f := services.KnowledgeFilter{MemoryFilter: services.MemoryFilter{GroupID: parseInt64Query(q.Get("group_id")), Subject: q.Get("subject"), Kind: q.Get("kind"), Status: q.Get("status"), Keyword: strings.TrimSpace(q.Get("keyword")), Sort: sortKey, Order: order, Page: parsePositiveInt(q.Get("page"), 1), PageSize: listPageSize(q.Get("page_size"))}, UserID: parseInt64Query(q.Get("user_id")), AuthorID: parseInt64Query(q.Get("author_id"))}
	result, err := a.admin.ListKnowledge(f)
	if err != nil {
		http.Error(w, "记忆列表加载失败，请稍后再试。", 500)
		return
	}
	data := views.KnowledgeListPageData{GroupID: q.Get("group_id"), Subject: f.Subject, UserID: q.Get("user_id"), AuthorID: q.Get("author_id"), Kind: f.Kind, Status: f.Status, Keyword: f.Keyword, Items: result.Items, SelfID: a.runtimeSnapshot().SelfID, Meta: a.listMeta(r.URL, result.Page, result.PageSize, result.Total), Flash: a.flashFromRequest(r), Sort: buildSortToolbar(r.URL, sortKey, order, []sortOption{{Key: "updated", Label: "最近更新"}, {Key: "created", Label: "创建时间"}})}
	if f.GroupID > 0 {
		data.Note, err = a.memMgr.GetWorkingNote(r.Context(), f.GroupID)
		if err != nil {
			http.Error(w, "群便签加载失败，请稍后再试。", 500)
			return
		}
	}
	a.renderPageResponse(w, r, views.KnowledgeListPage(data, r.URL.Path), views.PageContent(views.KnowledgeListBody(data)))
}

func (a *App) handleKnowledgeGraph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	groups, err := a.admin.KnowledgeGroups(r.Context())
	if err != nil {
		http.Error(w, "群聊加载失败", 500)
		return
	}
	groupID := parseInt64Query(q.Get("group_id"))
	if groupID == 0 && len(groups) > 0 {
		groupID = groups[0]
	}
	f := services.KnowledgeFilter{MemoryFilter: services.MemoryFilter{GroupID: groupID, Keyword: strings.TrimSpace(q.Get("keyword")), Kind: q.Get("kind"), Status: q.Get("status")}}
	focusID, _ := parseUintParam(q.Get("focus_id"))
	graph, err := a.admin.GroupKnowledgeGraph(r.Context(), f, q.Get("focus_kind"), focusID)
	if err != nil {
		http.Error(w, "图谱加载失败，请稍后再试", 500)
		return
	}
	data := views.KnowledgeGraphPageData{Graph: graph, Groups: groups, Filter: f, Flash: a.flashFromRequest(r)}
	kind := q.Get("selected_kind")
	id, _ := parseUintParam(q.Get("selected_id"))
	if id == 0 && focusID > 0 && (q.Get("focus_kind") == "knowledge" || q.Get("focus_kind") == "topic") {
		kind, id = q.Get("focus_kind"), focusID
	}
	if id == 0 && len(graph.Items) > 0 {
		kind = "knowledge"
		id = graph.Items[0].ID
	}
	if id == 0 && len(graph.Topics) > 0 {
		kind = "topic"
		id = graph.Topics[0].TopicID
	}
	if id > 0 {
		selection, e := a.admin.GraphSelection(r.Context(), groupID, kind, id, 0, 0)
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			http.Error(w, "依据加载失败，请稍后再试", 500)
			return
		}
		if e == nil {
			data.Panel = views.GraphPanel(selection, groupID, a.runtimeSnapshot().SelfID, kind, id, 0, 0)
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
	a.render(w, views.KnowledgeGraphPanel(views.GraphPanel(selection, groupID, a.runtimeSnapshot().SelfID, kind, id, related, offset)))
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
	a.render(w, views.KnowledgeDetailPage(views.KnowledgeDetailPageData{Detail: detail, SelfID: a.runtimeSnapshot().SelfID, Flash: a.flashFromRequest(r)}, r.URL.Path))
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
