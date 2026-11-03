package app

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
)

const maxJSONBody = 1 << 20

// respondData 输出统一成功信封；warning 表示已落库但运行态未完全应用
func respondData(w http.ResponseWriter, status int, data any, warning *warningPayload) {
	payload := struct {
		Data    any             `json:"data"`
		Warning *warningPayload `json:"warning,omitempty"`
	}{Data: data, Warning: warning}
	writeJSON(w, status, payload)
}

func respondError(w http.ResponseWriter, status int, code string, message string, details map[string]any) {
	errorBody := map[string]any{"code": code, "message": message}
	if len(details) > 0 {
		errorBody["details"] = details
	}
	writeJSON(w, status, map[string]any{"error": errorBody})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	raw, err := sonic.Marshal(payload)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"操作未完成，请稍后再试。"}}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func decodeJSON(r *http.Request, target any) error {
	if r.Body == nil {
		return errors.New("empty body")
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBody))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(raw)) == "" {
		return errors.New("empty body")
	}
	return sonic.Unmarshal(raw, target)
}

func queryInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(key)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func queryInt64(r *http.Request, key string) int64 {
	value, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get(key)), 10, 64)
	return value
}

func queryString(r *http.Request, key string) string {
	return strings.TrimSpace(r.URL.Query().Get(key))
}

func parseUintParam(raw string) (uint, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	return uint(value), err
}

func pageParams(r *http.Request, defaultSize int) (int, int) {
	page := queryInt(r, "page", 1)
	pageSize := queryInt(r, "page_size", defaultSize)
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func listResponse[T any](items []T, total int64, page, pageSize int) map[string]any {
	if items == nil {
		items = []T{}
	}
	return map[string]any{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}
}
