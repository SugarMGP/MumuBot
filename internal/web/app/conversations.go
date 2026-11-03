package app

import (
	"net/http"
	"strings"

	"mumu-bot/internal/memory"

	"github.com/go-chi/chi/v5"
)

func (a *App) loadConversations(r *http.Request) (conversationsResponse, error) {
	groups, err := a.admin.ListConversationTargets(r.Context(), memory.ConversationKindGroup)
	if err != nil {
		return conversationsResponse{}, err
	}
	friends, err := a.admin.ListConversationTargets(r.Context(), memory.ConversationKindPrivate)
	if err != nil {
		return conversationsResponse{}, err
	}
	return conversationsResponse{
		Groups:  conversationDTOs(groups),
		Friends: conversationDTOs(friends),
	}, nil
}

func conversationDTOs(rows []memory.ConversationTarget) []conversationDTO {
	items := make([]conversationDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, conversationDTO{
			Kind:         row.ConversationKind,
			TargetID:     row.TargetID,
			Name:         row.Name,
			RemoteRemark: row.RemoteRemark,
			Blocked:      row.Blocked,
			ExtraPrompt:  row.ExtraPrompt,
			Active:       row.Active,
			UpdatedAt:    row.UpdatedAt,
		})
	}
	return items
}

func (a *App) handleConversations(w http.ResponseWriter, r *http.Request) {
	data, err := a.loadConversations(r)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, data, nil)
}

func (a *App) handleConversationUpdate(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimSpace(chi.URLParam(r, "kind"))
	targetID, err := parseUintParam(chi.URLParam(r, "targetID"))
	if err != nil || targetID == 0 || (kind != memory.ConversationKindGroup && kind != memory.ConversationKindPrivate) {
		a.failBadRequest(w, "会话参数无效。")
		return
	}
	var body struct {
		Blocked     *bool   `json:"blocked"`
		ExtraPrompt *string `json:"extra_prompt"`
	}
	if err := decodeJSON(r, &body); err != nil {
		a.failBadRequest(w, "请求格式不正确。")
		return
	}
	if body.Blocked == nil && body.ExtraPrompt == nil {
		a.failBadRequest(w, "没有需要更新的内容。")
		return
	}
	if body.ExtraPrompt != nil && kind != memory.ConversationKindGroup {
		a.failBadRequest(w, "只有群聊可以设置补充提示。")
		return
	}
	if body.Blocked != nil {
		if err := a.admin.SetConversationBlocked(r.Context(), kind, int64(targetID), *body.Blocked); err != nil {
			a.failFromError(w, r, err)
			return
		}
	}
	if body.ExtraPrompt != nil {
		if err := a.admin.SetConversationExtraPrompt(r.Context(), int64(targetID), strings.TrimSpace(*body.ExtraPrompt)); err != nil {
			a.failFromError(w, r, err)
			return
		}
	}
	data, err := a.loadConversations(r)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, data, nil)
}
