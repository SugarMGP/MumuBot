package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/schedule"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type WriteDiaryInput struct {
	Date     string `json:"date" jsonschema:"description=归属日 YYYY-MM-DD；凌晨写前一天时仍填前一天"`
	Markdown string `json:"markdown" jsonschema:"description=整篇 Markdown 正文；补记前先读已有正文"`
}

type WriteDiaryOutput struct {
	Success   bool      `json:"success"`
	Date      string    `json:"date"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func writeDiary(ctx context.Context, input *WriteDiaryInput) (*WriteDiaryOutput, error) {
	_, mem, err := requireLifeContext(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, fmt.Errorf("需要 date 和 markdown")
	}
	date, err := memory.ParseDiaryDate(input.Date)
	if err != nil {
		return nil, err
	}
	today := time.Now().In(schedule.Location()).Format("2006-01-02")
	if date.Format("2006-01-02") > today {
		return nil, fmt.Errorf("日记日期不能晚于今天")
	}
	row, err := mem.SaveDiary(ctx, date, input.Markdown)
	if err != nil {
		return nil, err
	}
	return &WriteDiaryOutput{
		Success: true, Date: row.Date(), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

type ReadDiaryInput struct {
	Date   string `json:"date,omitempty" jsonschema:"description=YYYY-MM-DD；省略时读最近一篇"`
	Offset int    `json:"offset,omitempty" jsonschema:"description=按字符读取的起点，默认 0"`
	Length int    `json:"length,omitempty" jsonschema:"description=读取字符数，默认 4000，最大 8000"`
}

type ReadDiaryOutput struct {
	Success    bool       `json:"success"`
	Found      bool       `json:"found"`
	Date       string     `json:"date,omitempty"`
	Text       string     `json:"text,omitempty"`
	Total      int        `json:"total,omitempty"`
	Complete   bool       `json:"complete"`
	NextOffset int        `json:"next_offset,omitempty"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

func readDiary(ctx context.Context, input *ReadDiaryInput) (*ReadDiaryOutput, error) {
	_, mem, err := requireDiaryReadContext(ctx)
	if err != nil {
		return nil, err
	}
	var row *memory.DiaryEntry
	if input == nil || strings.TrimSpace(input.Date) == "" {
		row, err = mem.LatestDiary(ctx)
	} else {
		var date time.Time
		date, err = memory.ParseDiaryDate(input.Date)
		if err == nil {
			row, err = mem.GetDiary(ctx, date)
		}
	}
	if err != nil {
		return nil, err
	}
	if row == nil {
		return &ReadDiaryOutput{Success: true, Found: false}, nil
	}
	runes := []rune(row.Content)
	total := len(runes)
	offset := 0
	if input != nil && input.Offset > 0 {
		offset = input.Offset
	}
	if offset > total {
		offset = total
	}
	length := 4000
	if input != nil && input.Length > 0 {
		length = input.Length
	}
	if length > 8000 {
		length = 8000
	}
	end := offset + length
	if end > total {
		end = total
	}
	out := &ReadDiaryOutput{
		Success: true, Found: true, Date: row.Date(), Text: string(runes[offset:end]), Total: total,
		Complete: end >= total, CreatedAt: &row.CreatedAt, UpdatedAt: &row.UpdatedAt,
	}
	if !out.Complete {
		out.NextOffset = end
	}
	return out, nil
}

type SearchDiaryInput struct {
	From     string   `json:"from,omitempty" jsonschema:"description=起始日期 YYYY-MM-DD，包含当天"`
	To       string   `json:"to,omitempty" jsonschema:"description=结束日期 YYYY-MM-DD，包含当天"`
	Keywords []string `json:"keywords,omitempty" jsonschema:"description=正文精确子串；留空按日期浏览"`
	Cursor   string   `json:"cursor,omitempty"`
	Limit    int      `json:"limit,omitempty" jsonschema:"description=默认12，最大99"`
}

type DiarySearchHit struct {
	Date      string    `json:"date"`
	Excerpt   string    `json:"excerpt"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SearchDiaryOutput struct {
	Success    bool             `json:"success"`
	Entries    []DiarySearchHit `json:"entries"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

func searchDiary(ctx context.Context, input *SearchDiaryInput) (*SearchDiaryOutput, error) {
	_, mem, err := requireDiaryReadContext(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil {
		input = &SearchDiaryInput{}
	}
	from, until, err := schedule.ParseRange(input.From, input.To)
	if err != nil {
		return nil, err
	}
	page, err := mem.SearchDiaries(ctx, memory.DiarySearchQuery{
		Keywords: input.Keywords, From: from, To: until, Cursor: input.Cursor, Limit: input.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := &SearchDiaryOutput{Success: true, Entries: make([]DiarySearchHit, 0, len(page.Items)), HasMore: page.HasMore, NextCursor: page.NextCursor}
	for _, row := range page.Items {
		out.Entries = append(out.Entries, DiarySearchHit{
			Date: row.Date(), Excerpt: diaryExcerpt(row.Content, 200), UpdatedAt: row.UpdatedAt,
		})
	}
	return out, nil
}

// diaryExcerpt 只给检索结果一个短摘录，完整正文仍由 readDiary 读取
func diaryExcerpt(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

// requireDiaryReadContext 允许生活、群聊和私聊只读日记，写入仍由 writeDiary 限定 life
func requireDiaryReadContext(ctx context.Context) (*ToolContext, *memory.Manager, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, nil, NewTerminalToolError(fmt.Errorf("日记读取能力未初始化"))
	}
	switch tc.ConversationKind {
	case memory.ConversationKindLife, memory.ConversationKindGroup, memory.ConversationKindPrivate:
		return tc, tc.MemoryMgr, nil
	default:
		return nil, nil, fmt.Errorf("当前会话不能读取日记")
	}
}

func NewWriteDiaryTool() (tool.InvokableTool, error) {
	return utils.InferTool("writeDiary", "按归属日保存整篇 Markdown 日记；同日再次保存会整篇替换，首次保存时间不变。", writeDiary)
}

func NewReadDiaryTool() (tool.InvokableTool, error) {
	return utils.InferTool("readDiary", "只读某天的日记正文，可按字符窗口分页；省略日期时读最近一篇，没有日记返回 found=false；不得自动对外披露。", readDiary)
}

func NewSearchDiaryTool() (tool.InvokableTool, error) {
	return utils.InferTool("searchDiary", "只读检索日记的日期范围和正文关键词，日期倒序返回日期、摘录和更新时间。", searchDiary)
}
