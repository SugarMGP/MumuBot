package app

import (
	"net/http"
	neturl "net/url"
	"strings"

	"mumu-bot/internal/web/services"

	"github.com/go-chi/chi/v5"
)

func stickerFileURL(fileName string) string {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return ""
	}
	return "/api/v1/stickers/files/" + neturl.PathEscape(fileName)
}

func (a *App) handleStickers(w http.ResponseWriter, r *http.Request) {
	sortKey, order := services.NormalizeStickerSort(queryString(r, "sort"), queryString(r, "order"))
	page, pageSize := pageParams(r, compactListPageSize)
	result, err := a.admin.ListStickers(services.ListFilter{
		Keyword:  queryString(r, "keyword"),
		Sort:     sortKey,
		Order:    order,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	items := make([]stickerDTO, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, stickerDTO{
			ID:          item.ID,
			FileName:    item.FileName,
			Description: item.Description,
			UseCount:    item.UseCount,
			UpdatedAt:   item.UpdatedAt,
			FileURL:     stickerFileURL(item.FileName),
		})
	}
	respondData(w, http.StatusOK, listResponse(items, result.Total, result.Page, result.PageSize), nil)
}

func (a *App) handleStickerDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	if err := a.admin.DeleteSticker(r.Context(), id); err != nil {
		a.failFromError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
