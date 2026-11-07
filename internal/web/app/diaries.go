package app

import (
	"net/http"
	"strings"
	"time"

	"mumu-bot/internal/memory"

	"github.com/go-chi/chi/v5"
)

type diaryEntryDTO struct {
	Date      string    `json:"date"`
	Markdown  string    `json:"markdown"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type diaryDayResponse struct {
	Entry        *diaryEntryDTO `json:"entry"`
	PreviousDate *string        `json:"previous_date"`
	NextDate     *string        `json:"next_date"`
}

type diaryMonthResponse struct {
	Month string   `json:"month"`
	Dates []string `json:"dates"`
}

// handleDiaryMonth 列出某月已有日记的日期，直接查月内全部日期
func (a *App) handleDiaryMonth(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("month"))
	month, err := time.Parse("2006-01", raw)
	if err != nil || month.Format("2006-01") != raw {
		a.failDiaryInvalid(w)
		return
	}
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	next := start.AddDate(0, 1, 0)
	dates, err := a.memMgr.DiaryDates(r.Context(), start, next)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	out := diaryMonthResponse{Month: raw, Dates: make([]string, 0, len(dates))}
	for _, date := range dates {
		out.Dates = append(out.Dates, date.Format("2006-01-02"))
	}
	respondData(w, http.StatusOK, out, nil)
}

// handleDiaryDay 读取某天日记和相邻日期；没有日记是正常空状态
func (a *App) handleDiaryDay(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(chi.URLParam(r, "date"))
	date, err := memory.ParseDiaryDate(raw)
	if err != nil {
		a.failDiaryInvalid(w)
		return
	}
	row, err := a.memMgr.GetDiary(r.Context(), date)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	previous, next, err := a.memMgr.AdjacentDiaryDates(r.Context(), date)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	out := diaryDayResponse{}
	if row != nil {
		out.Entry = &diaryEntryDTO{
			Date: row.Date(), Markdown: row.Content, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
	}
	if previous != nil {
		value := previous.Format("2006-01-02")
		out.PreviousDate = &value
	}
	if next != nil {
		value := next.Format("2006-01-02")
		out.NextDate = &value
	}
	respondData(w, http.StatusOK, out, nil)
}

func (a *App) failDiaryInvalid(w http.ResponseWriter) {
	respondError(w, http.StatusBadRequest, "diary_date_invalid", "", nil)
}
