package app

import (
	"net/http"
	"strings"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/services"

	"github.com/go-chi/chi/v5"
)

const topicMessageLimit = 80

type topicConversationDTO struct {
	Kind     string `json:"kind"`
	TargetID int64  `json:"target_id"`
	Name     string `json:"name"`
}

type topicConversationsResponse struct {
	Items []topicConversationDTO `json:"items"`
}

func parseConversationParam(raw string) (string, int64) {
	kind, target, ok := strings.Cut(strings.TrimSpace(raw), ":")
	if !ok {
		return "", 0
	}
	kind = strings.TrimSpace(kind)
	targetID := int64(0)
	for _, char := range strings.TrimSpace(target) {
		if char < '0' || char > '9' {
			return "", 0
		}
		targetID = targetID*10 + int64(char-'0')
	}
	if targetID <= 0 || (kind != memory.ConversationKindGroup && kind != memory.ConversationKindPrivate) {
		return "", 0
	}
	return kind, targetID
}

// handleTopicConversations 只返回已经产生话题的会话，供话题筛选使用
func (a *App) handleTopicConversations(w http.ResponseWriter, r *http.Request) {
	rows, err := a.admin.KnowledgeConversations(r.Context())
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	items := make([]topicConversationDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, topicConversationDTO{
			Kind:     row.Kind,
			TargetID: row.TargetID,
			Name:     row.Name,
		})
	}
	respondData(w, http.StatusOK, topicConversationsResponse{Items: items}, nil)
}

func (a *App) handleTopics(w http.ResponseWriter, r *http.Request) {
	conversationKind, targetID := parseConversationParam(queryString(r, "conversation"))
	status := queryString(r, "status")
	if status == "all" {
		status = ""
	}
	page, pageSize := pageParams(r, compactListPageSize)
	result, err := a.admin.ListTopicThreads(services.ListFilter{
		ConversationKind: conversationKind,
		TargetID:         targetID,
		Status:           status,
		Keyword:          queryString(r, "keyword"),
		Page:             page,
		PageSize:         pageSize,
	})
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	items := make([]topicListItemDTO, 0, len(result.Items))
	for _, thread := range result.Items {
		items = append(items, topicListItem(thread))
	}
	respondData(w, http.StatusOK, listResponse(items, result.Total, result.Page, result.PageSize), nil)
}

func (a *App) handleTopicDetail(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	thread, err := a.admin.GetTopicThread(id)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	logs, err := a.admin.ListTopicMessages(id, topicMessageLimit)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	var currentSummary *memory.TopicSummary
	var currentSummaryVersion uint64
	if latest := thread.LatestSummary(); latest != nil {
		summary := topicSummaryOf(thread)
		currentSummary = &summary
		currentSummaryVersion = latest.Version
	}
	respondData(w, http.StatusOK, topicDetailResponse{
		Thread:                topicListItem(thread),
		CurrentSummary:        currentSummary,
		CurrentSummaryVersion: currentSummaryVersion,
		SummarySnapshotCount:  len(thread.Summaries),
		SummaryChanges:        topicSummaryChanges(thread),
		Messages:              topicMessages(thread, logs),
		MessageLimit:          topicMessageLimit,
	}, nil)
}
