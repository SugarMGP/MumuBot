package app

import (
	"errors"
	"net/http"
	"strings"

	"mumu-bot/internal/web/services"
	"mumu-bot/internal/web/views"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

func (a *App) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sortKey, order := services.NormalizeMemorySort(q.Get("sort"), q.Get("order"))
	f := services.KnowledgeFilter{MemoryFilter: services.MemoryFilter{GroupID: parseInt64Query(q.Get("group_id")), Subject: q.Get("subject"), Kind: q.Get("kind"), Status: q.Get("status"), Keyword: strings.TrimSpace(q.Get("keyword")), Sort: sortKey, Order: order, Page: parsePositiveInt(q.Get("page"), 1), PageSize: listPageSize(q.Get("page_size"))}, UserID: parseInt64Query(q.Get("user_id")), AuthorID: parseInt64Query(q.Get("author_id"))}
	result, err := a.admin.ListKnowledge(f)
	if err != nil {
		http.Error(w, "记忆列表加载失败，请稍后再试。", 500)
		return
	}
	data := views.KnowledgeListPageData{GroupID: q.Get("group_id"), Subject: f.Subject, UserID: q.Get("user_id"), AuthorID: q.Get("author_id"), Kind: f.Kind, Status: f.Status, Keyword: f.Keyword, Items: result.Items, SelfID: a.runtimeSnapshot().SelfID, Meta: a.listMeta(r.URL, result.Page, result.PageSize, result.Total), Flash: a.flashFromRequest(r), Sort: buildSortToolbar(r.URL, sortKey, order, []sortOption{{Key: "updated", Label: "最近更新"}, {Key: "created", Label: "创建时间"}})}
	if f.GroupID > 0 {
		data.Note, err = a.admin.WorkingNote(r.Context(), f.GroupID)
		if err != nil {
			http.Error(w, "群便签加载失败，请稍后再试。", 500)
			return
		}
	}
	a.renderPageResponse(w, r, views.KnowledgeListPage(data, r.URL.Path), views.PageContent(views.KnowledgeListBody(data)))
}

func (a *App) handleKnowledgeDetail(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	detail, err := a.admin.KnowledgeDetail(r.Context(), id)
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
		err = a.admin.ClearWorkingNote(r.Context(), groupID)
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
		http.Redirect(w, r, withFlash(target.String(), "error", "操作未完成", "记录可能已变化，或缺少完整有效证据。请刷新后核对。"), http.StatusSeeOther)
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
