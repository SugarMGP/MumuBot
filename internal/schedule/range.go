package schedule

import (
	"fmt"
	"strings"
	"time"
)

// ParseRange 把可选 from/to 统一成 [from, until) 边界
// 纯日期 to 顺延到次日零点，带时间或时区的上界保持精确时刻
func ParseRange(from, to string) (fromTime, untilTime *time.Time, err error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from != "" {
		value, parseErr := parseBoundary(from, false)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		fromTime = &value
	}
	if to != "" {
		value, parseErr := parseBoundary(to, true)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		untilTime = &value
	}
	if fromTime != nil && untilTime != nil && !untilTime.After(*fromTime) {
		return nil, nil, fmt.Errorf("%w：开始时间必须早于结束时间", ErrTimeInvalid)
	}
	return fromTime, untilTime, nil
}

func parseBoundary(raw string, upper bool) (time.Time, error) {
	loc := Location()
	// 纯日期：上界取次日零点，让结束日期本身包含在范围内
	if len(raw) == 10 {
		if value, err := time.ParseInLocation("2006-01-02", raw, loc); err == nil {
			if upper {
				value = value.AddDate(0, 0, 1)
			}
			return value, nil
		}
	}
	if value, err := time.Parse(time.RFC3339, raw); err == nil {
		return value, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if value, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return value, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w：%s", ErrTimeInvalid, raw)
}
