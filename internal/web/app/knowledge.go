package app

import (
	"errors"
	"net/http"
	"strconv"
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
	metadata, err := a.admin.KnowledgeMetadata(r.Context(), result.Items)
	if err != nil {
		http.Error(w, "原文依据加载失败", 500)
		return
	}
	data := views.KnowledgeListPageData{Workspace: workspace, Metadata: metadata, Items: result.Items, SelfID: a.runtimeSnapshot().SelfID, Meta: a.listMeta(r.URL, result.Page, result.PageSize, result.Total), Flash: a.flashFromRequest(r), Sort: buildSortToolbar(r.URL, sortKey, order, []sortOption{{Key: "updated", Label: "最近更新"}, {Key: "created", Label: "创建时间"}})}
	a.renderPageResponse(w, r, views.KnowledgeListPage(data, r.URL.Path), views.PageContent(views.KnowledgeListBody(data)))
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
	http.Redirect(w, r, withFlash(target.String(), "success", title, ""), http.StatusSeeOther)
}
func (a *App) knowledgeWorkspace(r *http.Request) (views.KnowledgeWorkspaceData, error) {
	q := r.URL.Query()
	status := q.Get("status")
	if status == "all" {
		status = ""
	}
	kind := q.Get("kind")
	conversationKind, targetID := parseConversationParam(q.Get("conversation"))
	sortKey, order := services.NormalizeMemorySort(q.Get("sort"), q.Get("order"))
	conversation := ""
	if conversationKind != "" {
		conversation = conversationKind + ":" + strconv.FormatInt(targetID, 10)
	}
	f := services.KnowledgeFilter{MemoryFilter: services.MemoryFilter{Kind: kind, Status: status, Keyword: strings.TrimSpace(q.Get("keyword")), Sort: sortKey, Order: order, Page: parsePositiveInt(q.Get("page"), 1), PageSize: listPageSize(q.Get("page_size"))}, ConversationKind: conversationKind, TargetID: targetID, UserID: parseInt64Query(q.Get("user_id")), AuthorID: parseInt64Query(q.Get("author_id"))}
	conversations, err := a.admin.KnowledgeConversations(r.Context())
	data := views.KnowledgeWorkspaceData{Filter: f, Conversations: conversations, CurrentURL: views.WithQuery(r.URL.RequestURI(), "conversation", conversation, "status", status, "kind", kind, "focus_kind", "", "focus_id", "", "selected_kind", "", "selected_id", "", "selected_related", "", "return_to", "", "flash_kind", "", "flash_title", "", "flash_body", "")}
	if err == nil && conversationKind == memory.ConversationKindGroup && targetID > 0 {
		data.Note, err = a.memMgr.GetWorkingNote(r.Context(), targetID)
	}
	return data, err
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
