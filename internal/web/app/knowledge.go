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
	workspace, err := a.knowledgeWorkspace(r)
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
	data := views.KnowledgeListPageData{Workspace: workspace, Items: result.Items, Meta: a.listMeta(r.URL, result.Page, result.PageSize, result.Total), Flash: a.flashFromRequest(r), Sort: buildSortToolbar(r.URL, sortKey, order, []sortOption{{Key: "updated", Label: "最近更新"}, {Key: "created", Label: "创建时间"}})}
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
	a.render(w, views.KnowledgeDetailPage(views.KnowledgeDetailPageData{Detail: detail, ReturnTo: a.knowledgeReturn(r, "/admin/knowledge"), CurrentURL: views.WithQuery(r.URL.RequestURI(), "return_to", a.knowledgeReturn(r, "/admin/knowledge")), Flash: a.flashFromRequest(r)}, r.URL.Path))
}

func (a *App) handleKnowledgeAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "请求格式不正确。", 400)
		return
	}
	var err error
	kind := r.FormValue("action_kind")
	id, e := parseUintParam(r.FormValue("action_id"))
	if e != nil {
		http.Error(w, "记录编号无效。", 400)
		return
	}
	switch kind {
	case "knowledge-status":
		err = a.admin.UpdateKnowledgeStatus(r.Context(), id, r.FormValue("status"))
	default:
		http.Error(w, "未识别这次操作。", 400)
		return
	}
	target := a.actionTargetURL(r, "/admin/knowledge")
	if err != nil {
		http.Redirect(w, r, withFlash(target.String(), "error", "操作未完成", err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, withFlash(target.String(), "success", "记忆状态已更新", ""), http.StatusSeeOther)
}
func (a *App) knowledgeWorkspace(r *http.Request) (views.KnowledgeWorkspaceData, error) {
	q := r.URL.Query()
	status := q.Get("status")
	if status == "all" {
		status = ""
	}
	subjectType := strings.TrimSpace(q.Get("subject_type"))
	subjectTarget := strings.TrimSpace(q.Get("subject_target"))
	memoryType := strings.TrimSpace(q.Get("memory_type"))
	sortKey, order := services.NormalizeMemorySort(q.Get("sort"), q.Get("order"))
	f := services.KnowledgeFilter{
		MemoryFilter: services.MemoryFilter{
			SubjectType: subjectType, SubjectTarget: subjectTarget, MemoryType: memoryType,
			Status: status, Keyword: strings.TrimSpace(q.Get("keyword")), Sort: sortKey, Order: order,
			Page: parsePositiveInt(q.Get("page"), 1), PageSize: listPageSize(q.Get("page_size")),
		},
		UserID: parseInt64Query(q.Get("user_id")),
	}
	data := views.KnowledgeWorkspaceData{Filter: f, CurrentURL: views.WithQuery(r.URL.RequestURI(), "subject_type", subjectType, "subject_target", subjectTarget, "memory_type", memoryType, "status", status, "focus_kind", "", "focus_id", "", "selected_kind", "", "selected_id", "", "selected_related", "", "return_to", "", "flash_kind", "", "flash_title", "", "flash_body", "")}
	return data, nil
}

// parseConversationParam 解析「群聊/好友:目标」形式的筛选值
func parseConversationParam(raw string) (string, int64) {
	kind, target, ok := strings.Cut(strings.TrimSpace(raw), ":")
	if !ok {
		return "", 0
	}
	kind = strings.TrimSpace(kind)
	targetID := parseInt64Query(strings.TrimSpace(target))
	if targetID <= 0 || (kind != memory.ConversationKindGroup && kind != memory.ConversationKindPrivate) {
		return "", 0
	}
	return kind, targetID
}

func (a *App) knowledgeReturn(r *http.Request, fallback string) string {
	if target, ok := normalizeAdminTarget(r.URL.Query().Get("return_to"), r.Host); ok {
		return target.String()
	}
	return fallback
}
