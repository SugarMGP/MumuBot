// Package schedule 统一时间表达：Asia/Shanghai 的绝对时间与标准五段 cron
package schedule

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// 时间输入的稳定分类，供工具和后台把校验失败映射成可读文案
var (
	ErrTimeInvalid = errors.New("时间输入无效")
)

var location = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	return time.Local
}()

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Location 返回日程使用的固定时区
func Location() *time.Location { return location }

// ParseTime 解析带时区的 RFC3339 或本地时间文本，无时区时按 Asia/Shanghai 解释
func ParseTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("%w：时间不能为空", ErrTimeInvalid)
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if value, err := time.ParseInLocation(layout, raw, location); err == nil {
			return value, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w：%s", ErrTimeInvalid, raw)
}

// NextRun 计算重复规则在 after 之后的下一个时点
func NextRun(expr string, after time.Time) (time.Time, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return time.Time{}, fmt.Errorf("重复规则不能为空")
	}
	parsed, err := parser.Parse(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("重复规则无效：%w", err)
	}
	if spec, ok := parsed.(*cron.SpecSchedule); ok {
		spec.Location = location
	}
	next := parsed.Next(after.In(location))
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("重复规则在可预见范围内没有下一次时点")
	}
	return next.UTC(), nil
}

// ResolveOnce 把一次性的 at / after_minutes 收敛成未来时间；公式和格式由这里统一
func ResolveOnce(at string, afterMinutes int) (time.Time, error) {
	at = strings.TrimSpace(at)
	switch {
	case at != "" && afterMinutes != 0:
		return time.Time{}, fmt.Errorf("%w：一次性时间与延时只能二选一", ErrTimeInvalid)
	case at != "":
		value, err := ParseTime(at)
		if err != nil {
			return time.Time{}, err
		}
		if !value.After(time.Now()) {
			return time.Time{}, fmt.Errorf("%w：时间必须晚于现在", ErrTimeInvalid)
		}
		return value, nil
	case afterMinutes > 0:
		return time.Now().Add(time.Duration(afterMinutes) * time.Minute), nil
	default:
		return time.Time{}, fmt.Errorf("%w：需要一次性时间或重复规则", ErrTimeInvalid)
	}
}

// Format 按固定时区格式化时间，供内部信和工具结果展示
func Format(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.In(location).Format("2006-01-02 15:04:05")
}
