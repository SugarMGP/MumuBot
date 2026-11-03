package app

import (
	"net/http"

	"mumu-bot/internal/web/services"
)

func (a *App) handleMembers(w http.ResponseWriter, r *http.Request) {
	sortKey, order := services.NormalizeMemberSort(queryString(r, "sort"), queryString(r, "order"))
	page, pageSize := pageParams(r, defaultListPageSize)
	result, err := a.admin.ListMemberProfiles(services.ListFilter{
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
	items := make([]memberDTO, 0, len(result.Items))
	for _, item := range result.Items {
		names := make([]memberNameDTO, 0, len(item.Names))
		for _, name := range item.Names {
			names = append(names, memberNameDTO{Value: name.Value, GroupID: name.GroupID})
		}
		items = append(items, memberDTO{
			UserID:         item.UserID,
			Nickname:       item.Nickname,
			UpdatedAt:      item.UpdatedAt,
			Names:          names,
			KnowledgeCount: item.KnowledgeCount,
		})
	}
	respondData(w, http.StatusOK, listResponse(items, result.Total, result.Page, result.PageSize), nil)
}
