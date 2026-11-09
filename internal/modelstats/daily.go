package modelstats

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type DailyUsage struct {
	Date     string `json:"date"`
	Limit    int    `json:"limit"`
	Total    int64  `json:"total"`
	Life     int64  `json:"life"`
	Worker   int64  `json:"worker"`
	Failures int64  `json:"failures"`
	Status   string `json:"status"`
}

// QueryDailyUsage 统计上海自然日内已落库的生活与工作模型请求，不包含 QQ 压缩
func QueryDailyUsage(ctx context.Context, db *gorm.DB, now time.Time, limit int) (*DailyUsage, error) {
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, err
	}
	local := now.In(zone)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	usage := &DailyUsage{}
	err = db.WithContext(ctx).Raw(`SELECT
		COALESCE(sum(request_count) FILTER (WHERE task='life'),0) AS life,
		COALESCE(sum(request_count) FILTER (WHERE task='worker'),0) AS worker,
		COALESCE(sum(failure_count),0) AS failures
		FROM model_call_hourly WHERE bucket_start>=? AND bucket_start<? AND task IN ('life','worker')`, start, start.AddDate(0, 0, 1)).Scan(usage).Error
	if err != nil {
		return nil, err
	}
	usage.Date = start.Format("2006-01-02")
	usage.Limit = limit
	usage.Total = usage.Life + usage.Worker
	usage.Status = DailyUsageStatus(usage.Total, limit)
	return usage, nil
}

func DailyUsageStatus(total int64, limit int) string {
	switch {
	case limit == 0:
		return "disabled"
	case total >= int64(limit):
		return "over"
	case float64(total) >= float64(limit)*0.8:
		return "near"
	default:
		return "normal"
	}
}
