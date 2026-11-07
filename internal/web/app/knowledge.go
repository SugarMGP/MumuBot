package app

import (
	"net/http"
	"strings"

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
	var body struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &body); err != nil {
		a.failBadRequest(w, "请求格式不正确。")
		return
	}
	status := strings.TrimSpace(body.Status)
	if status != "archived" {
		a.failBadRequest(w, "记忆只能归档，不能从后台恢复。")
		return
	}
	if err := a.admin.UpdateKnowledgeStatus(r.Context(), id, status); err != nil {
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
