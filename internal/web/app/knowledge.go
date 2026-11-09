package app

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/services"

	"github.com/go-chi/chi/v5"
)

func (a *App) handleMemories(w http.ResponseWriter, r *http.Request) {
	status := queryString(r, "status")
	if status == "all" {
		status = ""
	}
	sortKey, order := services.NormalizeMemorySort(queryString(r, "sort"), queryString(r, "order"))
	page, pageSize := pageParams(r, defaultListPageSize)
	filter := services.KnowledgeFilter{
		MemoryFilter: services.MemoryFilter{
			SubjectType:   queryString(r, "subject_type"),
			SubjectTarget: queryString(r, "subject_target"),
			MemoryType:    queryString(r, "memory_type"),
			Status:        status,
			Keyword:       queryString(r, "keyword"),
			Sort:          sortKey,
			Order:         order,
			Page:          page,
			PageSize:      pageSize,
		},
		UserID: queryInt64(r, "user_id"),
	}
	result, err := a.admin.ListKnowledge(filter)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	items := make([]memoryDTO, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, memoryItemDTO(item))
	}
	respondData(w, http.StatusOK, listResponse(items, result.Total, result.Page, result.PageSize), nil)
}

func (a *App) handleMemoryDetail(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	detail, err := a.admin.KnowledgeDetail(r.Context(), id)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, memoryDetailResponse{
		Item:      memoryItemDTO(detail.Item),
		Revisions: detail.Revisions,
	}, nil)
}

func (a *App) handleMemoryUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	var body services.KnowledgeUpdate
	if err := decodeJSON(r, &body); err != nil {
		a.failBadRequest(w, "")
		return
	}
	if body.Content != nil {
		content := strings.TrimSpace(*body.Content)
		body.Content = &content
	}
	if body.Version == 0 || (body.Status != "" && body.Status != "archived") ||
		(body.Status == "archived" && (body.Content != nil || body.Pinned != nil)) ||
		(body.Status == "" && body.Content == nil && body.Pinned == nil) ||
		(body.Content != nil && (*body.Content == "" || utf8.RuneCountInString(*body.Content) > 8000)) {
		a.failBadRequest(w, "")
		return
	}
	if err := a.admin.UpdateKnowledge(r.Context(), id, body); err != nil {
		a.failFromError(w, r, err)
		return
	}
	item, err := a.admin.GetKnowledge(id)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, memoryItemDTO(item), nil)
}

func memoryItemDTO(item memory.KnowledgeRecord) memoryDTO {
	return memoryDTO{
		ID:             item.ID,
		Version:        item.Version,
		SubjectType:    item.SubjectType,
		SubjectTarget:  item.SubjectTarget,
		MemoryType:     item.MemoryType,
		Label:          item.Label,
		Content:        item.Content,
		Pinned:         item.Pinned,
		Status:         item.Status,
		StabilityDays:  item.StabilityDays,
		RecallAnchorAt: item.RecallAnchorAt,
		ForgetAt:       item.ForgetAt,
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}
}

func (a *App) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		a.failBadRequest(w, "")
		return
	}
	if err := a.admin.DeleteKnowledge(r.Context(), id); err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, map[string]bool{"deleted": true}, nil)
}
