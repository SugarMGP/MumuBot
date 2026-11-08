package app

import (
	"fmt"
	"net/http"
	"time"

	"mumu-bot/internal/logger"

	"github.com/bytedance/sonic"
)

func (a *App) handleLogStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, http.StatusInternalServerError, "internal", messageInternal, nil)
		return
	}
	keyword := queryString(r, "keyword")
	level := queryString(r, "level")
	if level == "" {
		level = "debug"
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	writeSnapshot := func(result logger.QueryResult) {
		payload := logSnapshot(result)
		raw, err := sonic.Marshal(payload)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", raw)
		flusher.Flush()
	}

	initial := logger.Query(keyword, level)
	writeSnapshot(initial)
	lastSignature := logSignature(initial)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			result := logger.Query(keyword, level)
			signature := logSignature(result)
			if signature == lastSignature {
				continue
			}
			lastSignature = signature
			writeSnapshot(result)
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func logSnapshot(result logger.QueryResult) logSnapshotDTO {
	lines := make([]logLineDTO, 0, len(result.Lines))
	for _, line := range result.Lines {
		fields := make([]logFieldDTO, 0, len(line.Fields))
		for _, field := range line.Fields {
			fields = append(fields, logFieldDTO{Name: field.Name, Value: field.Value, FormattedValue: field.FormattedValue})
		}
		lines = append(lines, logLineDTO{
			Seq:     line.Seq,
			Time:    line.Time,
			Level:   line.Level,
			Message: line.Message,
			Fields:  fields,
			Text:    line.Text,
		})
	}
	return logSnapshotDTO{Lines: lines, Total: result.Total, Filtered: result.Filtered}
}

func logSignature(result logger.QueryResult) string {
	last := uint64(0)
	if len(result.Lines) > 0 {
		last = result.Lines[len(result.Lines)-1].Seq
	}
	return fmt.Sprintf("%d/%d/%d", last, result.Total, result.Filtered)
}
