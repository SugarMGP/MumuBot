package app

import (
	"fmt"
	"net/http"

	"mumu-bot/internal/tools"

	"github.com/go-chi/chi/v5"
)

func (a *App) handleLifeHistory(w http.ResponseWriter, r *http.Request) {
	a.serveSessionHistory(w, r, "life:1")
}

func (a *App) handleLifeEntry(w http.ResponseWriter, r *http.Request) {
	a.serveSessionEntry(w, r, "life:1")
}

func (a *App) handleWorkerHistory(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil || id == 0 {
		respondError(w, http.StatusBadRequest, "validation_failed", "", nil)
		return
	}
	a.serveSessionHistory(w, r, fmt.Sprintf("worker:%d", id))
}

func (a *App) handleWorkerEntry(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil || id == 0 {
		respondError(w, http.StatusBadRequest, "validation_failed", "", nil)
		return
	}
	a.serveSessionEntry(w, r, fmt.Sprintf("worker:%d", id))
}

func (a *App) serveSessionHistory(w http.ResponseWriter, r *http.Request, sessionID string) {
	data, err := tools.ReadSessionHistory(r.Context(), a.memMgr, &tools.ReadSessionHistoryInput{
		SessionID: sessionID, BeforeID: uint(max(0, queryInt(r, "before_id", 0))), Limit: 20,
	})
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, data, nil)
}

func (a *App) serveSessionEntry(w http.ResponseWriter, r *http.Request, sessionID string) {
	entryID, entryErr := parseUintParam(chi.URLParam(r, "entryID"))
	if entryErr != nil || entryID == 0 {
		respondError(w, http.StatusBadRequest, "validation_failed", "", nil)
		return
	}
	offset := max(0, queryInt(r, "offset", 0))
	data, err := tools.ReadSessionEntry(r.Context(), a.memMgr, &tools.ReadSessionEntryInput{
		SessionID: sessionID, EntryID: entryID, Offset: offset, Length: 8000,
	})
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	if data.ResultRef != "" {
		window, readErr := a.memMgr.ReadToolResultText(r.Context(), data.ResultRef, offset, 8000, "")
		if readErr != nil {
			a.failFromError(w, r, readErr)
			return
		}
		data.Text, data.Total = window.Content, window.Total
		data.Complete, data.NextOffset = window.Complete, window.NextOffset
	}
	respondData(w, http.StatusOK, data, nil)
}
