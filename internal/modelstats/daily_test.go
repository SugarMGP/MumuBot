package modelstats

import (
	"context"
	"testing"
	"time"

	"mumu-bot/internal/testutil"
)

func TestDailyUsageShanghai(t *testing.T) {
	db, _ := testutil.Database(t)
	zone, _ := time.LoadLocation("Asia/Shanghai")
	start := time.Date(2026, 11, 9, 0, 0, 0, 0, zone)
	for _, row := range []struct {
		hour             int
		task             string
		requests, failed int
	}{{-1, "life", 100, 0}, {1, "life", 80, 4}, {2, "worker", 20, 1}, {3, "react", 999, 0}, {4, "compaction", 999, 0}, {25, "life", 100, 0}} {
		if err := db.Exec(`INSERT INTO model_call_hourly(bucket_start,task,model,request_count,failure_count) VALUES(?,?,?,?,?)`, start.Add(time.Duration(row.hour)*time.Hour), row.task, "mock-model", row.requests, row.failed).Error; err != nil {
			t.Fatal(err)
		}
	}
	usage, err := QueryDailyUsage(context.Background(), db, start.Add(5*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Date != "2026-11-09" || usage.Total != 100 || usage.Life != 80 || usage.Worker != 20 || usage.Failures != 5 || usage.Status != "over" || usage.Limit != 100 {
		t.Fatalf("%+v", usage)
	}
	for _, test := range []struct {
		total int64
		limit int
		want  string
	}{{0, 0, "disabled"}, {79, 100, "normal"}, {80, 100, "near"}, {100, 100, "over"}, {8, 10, "near"}} {
		if got := DailyUsageStatus(test.total, test.limit); got != test.want {
			t.Fatalf("status: %v %v %s", test.total, test.limit, got)
		}
	}
}
