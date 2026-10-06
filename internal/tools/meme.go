package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	projectutils "mumu-bot/internal/utils"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const memeAPIBaseURL = "https://cnmeme.wiki/api/v1/public/memes"

// SearchMemeInput 是 searchMeme 的查询参数
type SearchMemeInput struct {
	Query  string `json:"query" jsonschema:"description=要搜索的黑话、网络梗或表情包词语，最多 80 字"`
	Tag    string `json:"tag,omitempty" jsonschema:"description=可选的完整标签，最多 40 字"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=返回条数，默认 8，范围 1 到 30"`
	Offset int    `json:"offset,omitempty" jsonschema:"description=跳过的条数，用于继续翻页，不得为负数"`
}

// MemeSearchResult 是 searchMeme 返回的公开梗词条
type MemeSearchResult struct {
	Success bool        `json:"success"`
	Action  string      `json:"action"`
	Query   string      `json:"query,omitempty"`
	Tag     string      `json:"tag,omitempty"`
	Limit   int         `json:"limit"`
	Offset  int         `json:"offset"`
	HasMore bool        `json:"has_more"`
	Results []MemeEntry `json:"results,omitempty"`
}

// MemeEntry 是梗Wiki公开词条的摘要
type MemeEntry struct {
	Slug        string           `json:"slug"`
	Title       string           `json:"title"`
	Summary     string           `json:"summary,omitempty"`
	Tags        []string         `json:"tags,omitempty"`
	CoverURL    string           `json:"cover_url,omitempty"`
	UpdatedAt   string           `json:"updated_at,omitempty"`
	DetailURL   string           `json:"detail_url"`
	SourceVideo *MemeSourceVideo `json:"source_video,omitempty"`
}

// MemeSourceVideo 是词条关联的来源视频
type MemeSourceVideo struct {
	BVID        string `json:"bvid"`
	Title       string `json:"title,omitempty"`
	CoverURL    string `json:"cover_url,omitempty"`
	CreatorName string `json:"creator_name,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
}

type memeAPIResponse struct {
	SchemaVersion int         `json:"schema_version"`
	Items         []MemeEntry `json:"items"`
	Limit         int         `json:"limit"`
	Offset        *int        `json:"offset"`
	HasMore       *bool       `json:"has_more"`
}

func searchMeme(ctx context.Context, input *SearchMemeInput) (*MemeSearchResult, error) {
	if input == nil {
		return nil, fmt.Errorf("搜索条件不能为空")
	}
	query := cleanMemeTerm(input.Query, 80)
	tag := cleanMemeTerm(input.Tag, 40)
	if query == "" && tag == "" {
		return nil, fmt.Errorf("query 和 tag 至少填写一个")
	}
	if input.Offset < 0 {
		return nil, fmt.Errorf("offset 不得为负数")
	}
	limit := input.Limit
	if limit == 0 {
		limit = 8
	}
	if limit < 1 || limit > 30 {
		return nil, fmt.Errorf("limit 必须在 1 到 30 之间")
	}

	params := url.Values{}
	if query != "" {
		params.Set("q", query)
	}
	if tag != "" {
		params.Set("tag", tag)
	}
	params.Set("limit", fmt.Sprint(limit))
	params.Set("offset", fmt.Sprint(input.Offset))
	body, _, err := projectutils.FetchPublicText(ctx, memeAPIBaseURL+"?"+params.Encode())
	if err != nil {
		return nil, err
	}
	var payload memeAPIResponse
	if err := sonic.UnmarshalString(body, &payload); err != nil {
		return nil, fmt.Errorf("梗Wiki 响应解析失败: %w", err)
	}
	if payload.SchemaVersion != 1 || payload.Items == nil || payload.Limit != limit ||
		payload.Offset == nil || *payload.Offset != input.Offset || payload.HasMore == nil {
		return nil, fmt.Errorf("梗Wiki 响应结构无效或版本不受支持")
	}
	for i := range payload.Items {
		if strings.TrimSpace(payload.Items[i].Slug) == "" || strings.TrimSpace(payload.Items[i].Title) == "" {
			return nil, fmt.Errorf("梗Wiki 词条缺少 slug 或 title")
		}
		payload.Items[i].DetailURL = memeAPIBaseURL + "/" + url.PathEscape(payload.Items[i].Slug)
	}
	return &MemeSearchResult{
		Success: true,
		Action:  "search_meme",
		Query:   query,
		Tag:     tag,
		Limit:   payload.Limit,
		Offset:  *payload.Offset,
		HasMore: *payload.HasMore,
		Results: payload.Items,
	}, nil
}

func cleanMemeTerm(raw string, limit int) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, raw)
	clean = strings.Join(strings.Fields(clean), " ")
	if runes := []rune(clean); len(runes) > limit {
		clean = string(runes[:limit])
	}
	return clean
}

// NewSearchMemeTool 创建梗Wiki搜索工具
func NewSearchMemeTool() (tool.InvokableTool, error) {
	return utils.InferTool("searchMeme", "优先搜索梗Wiki中的黑话、网络梗和表情包词条；按标题、标签和正文做子串匹配，不是语义搜索；返回公开释义、标签和来源，完整正文用 fetchWeb 读取 detail_url。", searchMeme)
}
