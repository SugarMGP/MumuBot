package app

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mumu-bot/internal/logger"

	"github.com/bytedance/sonic"
)

func (a *App) handleLogs(w http.ResponseWriter, r *http.Request) {
	result := logger.Query(queryString(r, "keyword"), queryLogLevel(r))
	respondData(w, http.StatusOK, logSnapshot(result), nil)
}

func (a *App) handleLogStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, http.StatusInternalServerError, "internal", messageInternal, nil)
		return
	}
	keyword := queryString(r, "keyword")
	level := queryLogLevel(r)
	afterSeq := queryUint64(r, "after_seq")

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	writeEvent := func(event string, result logger.QueryResult) bool {
		raw, err := sonic.Marshal(logSnapshot(result))
		if err != nil {
			return false
		}
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
		flusher.Flush()
		return true
	}

	initial := logger.QueryAfter(keyword, level, afterSeq)
	if afterSeq == 0 || afterSeq > initial.LastSeq {
		if !writeEvent("snapshot", initial) {
			return
		}
	} else if initial.LastSeq > afterSeq {
		if !writeEvent("append", initial) {
			return
		}
	}
	lastSeq := initial.LastSeq
	lastTotal := initial.Total
	lastFiltered := initial.Filtered

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			result := logger.QueryAfter(keyword, level, lastSeq)
			if result.LastSeq == lastSeq && result.Total == lastTotal && result.Filtered == lastFiltered {
				continue
			}
			if !writeEvent("append", result) {
				return
			}
			lastSeq = result.LastSeq
			lastTotal = result.Total
			lastFiltered = result.Filtered
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func queryLogLevel(r *http.Request) string {
	level := queryString(r, "level")
	if level == "" {
		return "debug"
	}
	return level
}

func queryUint64(r *http.Request, key string) uint64 {
	value, err := strconv.ParseUint(strings.TrimSpace(r.URL.Query().Get(key)), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func logSnapshot(result logger.QueryResult) logSnapshotDTO {
	lines := make([]logLineDTO, 0, len(result.Lines))
	for _, line := range result.Lines {
		fields := make([]logFieldDTO, 0, len(line.Fields))
		for _, field := range line.Fields {
			fields = append(fields, logFieldDTO{Name: field.Name, Value: field.Value, FormattedValue: field.FormattedValue})
		}
		lines = append(lines, logLineDTO{
			Seq: line.Seq, Time: line.Time, Level: line.Level, Message: line.Message, Fields: fields,
		})
	}
	return logSnapshotDTO{
		Lines: lines, Total: result.Total, Filtered: result.Filtered,
		FirstSeq: result.FirstSeq, LastSeq: result.LastSeq,
	}
}
