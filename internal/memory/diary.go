package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DiaryEntry 是一天的 Markdown 日记，日期唯一
type DiaryEntry struct {
	DiaryDate time.Time `gorm:"column:diary_date;type:date;primaryKey" json:"-"`
	Content   string    `gorm:"column:content_markdown;type:text;not null" json:"markdown"`
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (DiaryEntry) TableName() string { return "diaries" }

// Date 按归属日输出 YYYY-MM-DD，不把 DATE 序列化成 RFC3339
func (d DiaryEntry) Date() string {
	return d.DiaryDate.Format("2006-01-02")
}

// ErrDiaryDateInvalid 表示日期格式或范围不合法
var ErrDiaryDateInvalid = errors.New("日记日期无效")

// ParseDiaryDate 解析严格的 YYYY-MM-DD，并统一成 UTC 零点保存
func ParseDiaryDate(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	value, err := time.ParseInLocation("2006-01-02", raw, time.UTC)
	if err != nil || value.Format("2006-01-02") != raw {
		return time.Time{}, fmt.Errorf("%w：%s", ErrDiaryDateInvalid, raw)
	}
	return value, nil
}

// SaveDiary 按日期 UPSERT；首次保存时间不变，同日补记整篇替换正文
func (m *Manager) SaveDiary(ctx context.Context, date time.Time, markdown string) (*DiaryEntry, error) {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return nil, fmt.Errorf("日记正文不能为空")
	}
	now := time.Now()
	row := &DiaryEntry{DiaryDate: date, Content: markdown, CreatedAt: now, UpdatedAt: now}
	if err := m.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "diary_date"}},
		DoUpdates: clause.Assignments(map[string]any{
			"content_markdown": markdown,
			"updated_at":       now,
		}),
	}).Create(row).Error; err != nil {
		return nil, err
	}
	return m.GetDiary(ctx, date)
}

// GetDiary 读取指定日期的日记，没有时返回 nil
func (m *Manager) GetDiary(ctx context.Context, date time.Time) (*DiaryEntry, error) {
	var row DiaryEntry
	err := m.db.WithContext(ctx).Where("diary_date=?", date).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// LatestDiary 读取最近一篇日记，空库返回 nil
func (m *Manager) LatestDiary(ctx context.Context) (*DiaryEntry, error) {
	var row DiaryEntry
	err := m.db.WithContext(ctx).Order("diary_date DESC").Limit(1).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// DiaryDates 返回 [from, until) 内已有日记的日期
func (m *Manager) DiaryDates(ctx context.Context, from, until time.Time) ([]time.Time, error) {
	var rows []DiaryEntry
	if err := m.db.WithContext(ctx).Select("diary_date").
		Where("diary_date>=? AND diary_date<?", from, until).
		Order("diary_date ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	dates := make([]time.Time, 0, len(rows))
	for _, row := range rows {
		dates = append(dates, row.DiaryDate)
	}
	return dates, nil
}

// AdjacentDiaryDates 返回所选日期前后最近的有日记日期，可跨月
func (m *Manager) AdjacentDiaryDates(ctx context.Context, date time.Time) (previous, next *time.Time, err error) {
	var previousRow DiaryEntry
	if findErr := m.db.WithContext(ctx).Where("diary_date<?", date).Order("diary_date DESC").Limit(1).Take(&previousRow).Error; findErr == nil {
		value := previousRow.DiaryDate
		previous = &value
	} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return nil, nil, findErr
	}
	var nextRow DiaryEntry
	if findErr := m.db.WithContext(ctx).Where("diary_date>?", date).Order("diary_date ASC").Limit(1).Take(&nextRow).Error; findErr == nil {
		value := nextRow.DiaryDate
		next = &value
	} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return nil, nil, findErr
	}
	return previous, next, nil
}

// DiarySearchQuery 是日记检索入参；日期范围为 [From, To)
type DiarySearchQuery struct {
	Keywords []string
	From     *time.Time
	To       *time.Time
	Cursor   string
	Limit    int
}

type diarySearchFilter struct {
	Keywords []string   `json:"keywords"`
	From     *time.Time `json:"from,omitempty"`
	To       *time.Time `json:"to,omitempty"`
}

// DiaryPage 是日期倒序的日记检索结果
type DiaryPage struct {
	Items      []DiaryEntry
	NextCursor string
	HasMore    bool
}

// SearchDiaries 按日期范围和正文精确子串检索，日期倒序 keyset 分页
func (m *Manager) SearchDiaries(ctx context.Context, query DiarySearchQuery) (DiaryPage, error) {
	keywords := NormalizeKeywords(query.Keywords)
	filter := diarySearchFilter{Keywords: keywords, From: query.From, To: query.To}
	cursor, err := DecodeCursor(query.Cursor)
	if err != nil {
		return DiaryPage{}, err
	}
	if cursor.FilterHash == "" {
		cursor.FilterHash = CursorFilterHash(filter)
	}
	if err := ValidateCursorFilter(cursor, filter); err != nil {
		return DiaryPage{}, err
	}
	snapshot := CursorTimeOrNow(cursor)
	limit := NormalizeLimit(query.Limit)
	q := m.db.WithContext(ctx).Model(&DiaryEntry{}).Where("updated_at<=?", snapshot)
	if filter.From != nil {
		q = q.Where("diary_date>=?", *filter.From)
	}
	if filter.To != nil {
		q = q.Where("diary_date<?", *filter.To)
	}
	for _, keyword := range keywords {
		q = q.Where("content_markdown ILIKE ?", LiteralContainsPattern(keyword))
	}
	if !cursor.LastTime.IsZero() {
		q = q.Where("diary_date<?", cursor.LastTime)
	}
	var rows []DiaryEntry
	if err := q.Order("diary_date DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return DiaryPage{}, err
	}
	page := DiaryPage{}
	if len(rows) > limit {
		page.HasMore = true
		rows = rows[:limit]
	}
	page.Items = rows
	if page.HasMore && len(rows) > 0 {
		page.NextCursor = NewPageCursor("diary_date", 0, rows[len(rows)-1].DiaryDate, 0, snapshot, filter)
	}
	return page, nil
}
