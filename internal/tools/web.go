package tools

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	projectutils "mumu-bot/internal/utils"

	"github.com/PuerkitoBio/goquery"
	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"golang.org/x/net/html"
)

type SearchWebInput struct {
	Query string `json:"query" jsonschema:"description=搜索词，最多 120 字"`
}

type FetchWebInput struct {
	URL string `json:"url" jsonschema:"description=要抓取的网页 HTTP/HTTPS 地址"`
}

type WebResult struct {
	Success bool        `json:"success"`
	Action  string      `json:"action"`
	Title   string      `json:"title,omitempty"`
	Content string      `json:"content,omitempty"`
	OGImage string      `json:"og_image,omitempty"`
	Results []WebSearch `json:"results,omitempty"`
	Links   []string    `json:"links,omitempty"`
	Images  []string    `json:"images,omitempty"`
}

type WebSearch struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

var (
	spaceRE = regexp.MustCompile(`\s+`)
	cqRE    = regexp.MustCompile(`\[CQ:[^\]]*\]`)
)

// nonTextTags 是提取正文时整块跳过的元素，textarea 常被用来藏样式和模板
var nonTextTags = map[string]bool{
	"head":     true,
	"script":   true,
	"style":    true,
	"noscript": true,
	"template": true,
	"textarea": true,
	"iframe":   true,
	"svg":      true,
	"canvas":   true,
}

// cleanWebQuery 清理搜索词中的控制字符、CQ 码和多余空白，并限制长度
func cleanWebQuery(raw string) string {
	query := cqRE.ReplaceAllString(raw, " ")
	query = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, query)
	query = strings.TrimSpace(spaceRE.ReplaceAllString(query, " "))
	if runes := []rune(query); len(runes) > 120 {
		query = string(runes[:120])
	}
	return query
}

// documentText 提取正文文本，元素边界补空格避免相邻文字粘连
func documentText(doc *goquery.Document) string {
	var builder strings.Builder
	appendNodeText(doc.Get(0), &builder)
	return cleanText(builder.String())
}

func appendNodeText(node *html.Node, builder *strings.Builder) {
	if node.Type == html.TextNode {
		builder.WriteString(node.Data)
		return
	}
	if node.Type == html.ElementNode {
		if nonTextTags[node.Data] {
			return
		}
		builder.WriteByte(' ')
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		appendNodeText(child, builder)
	}
	if node.Type == html.ElementNode {
		builder.WriteByte(' ')
	}
}

// cleanText 折叠文本里的空白
func cleanText(raw string) string {
	return strings.TrimSpace(spaceRE.ReplaceAllString(raw, " "))
}

func resolveURLs(baseURL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return raw
	}
	item, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return base.ResolveReference(item).String()
}

// collectImages 读取 img/source 的 src 和 srcset，srcset 按候选逐个拆分
func collectImages(doc *goquery.Document, baseURL string) []string {
	var images []string
	doc.Find("img, source").Each(func(_ int, node *goquery.Selection) {
		if src, ok := node.Attr("src"); ok {
			images = appendUnique(images, resolveURLs(baseURL, src))
		}
		srcset, ok := node.Attr("srcset")
		if !ok {
			return
		}
		for _, candidate := range strings.Split(srcset, ",") {
			fields := strings.Fields(strings.TrimSpace(candidate))
			if len(fields) > 0 {
				images = appendUnique(images, resolveURLs(baseURL, fields[0]))
			}
		}
	})
	return images
}

func searchWeb(ctx context.Context, input *SearchWebInput) (*WebResult, error) {
	if input == nil {
		return nil, fmt.Errorf("搜索词不能为空")
	}
	query := cleanWebQuery(input.Query)
	if query == "" {
		return nil, fmt.Errorf("搜索词不能为空")
	}
	searchURL := "https://cn.bing.com/search?q=" + url.QueryEscape(query)
	body, _, err := projectutils.FetchPublicText(ctx, searchURL)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("解析搜索结果失败: %w", err)
	}
	result := &WebResult{Success: true, Action: "search"}
	doc.Find("li.b_algo").EachWithBreak(func(_ int, block *goquery.Selection) bool {
		link := block.Find("h2 a").First()
		href, ok := link.Attr("href")
		title := cleanText(block.Find("h2").First().Text())
		if !ok || strings.TrimSpace(href) == "" || title == "" {
			return true
		}
		item := WebSearch{URL: strings.TrimSpace(href), Title: title}
		if snippet := block.Find("p").First(); snippet.Length() > 0 {
			item.Snippet = cleanText(snippet.Text())
		}
		result.Results = append(result.Results, item)
		return len(result.Results) < 8
	})
	if len(result.Results) == 0 && !hasNoSearchResults(body) {
		return nil, fmt.Errorf("搜索结果解析失败")
	}
	return result, nil
}

// hasNoSearchResults 判断页面是否明确表示没有搜索结果，避免把空结果误判成解析失败
func hasNoSearchResults(body string) bool {
	for _, marker := range []string{"没有与此相关的结果", "找不到与", "There are no results", "No results found", "没有找到"} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

func fetchWeb(ctx context.Context, input *FetchWebInput) (*WebResult, error) {
	if input == nil || strings.TrimSpace(input.URL) == "" {
		return nil, fmt.Errorf("网页 URL 不能为空")
	}
	body, finalURL, err := projectutils.FetchPublicText(ctx, input.URL)
	if err != nil {
		return nil, err
	}
	// JSON 详情保留全部字段及正文，不把字符串里的 HTML 当成网页结构过滤
	if sonic.ValidString(body) {
		return &WebResult{Success: true, Action: "fetch", Content: body}, nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("解析网页失败: %w", err)
	}
	result := &WebResult{Success: true, Action: "fetch", Content: documentText(doc)}
	baseURL := finalURL
	if base, ok := doc.Find("base[href]").First().Attr("href"); ok {
		baseURL = resolveURLs(finalURL, base)
	}
	if title := doc.Find("title").First(); title.Length() > 0 {
		result.Title = cleanText(title.Text())
	}
	if og, ok := doc.Find(`meta[property="og:image"]`).First().Attr("content"); ok {
		result.OGImage = resolveURLs(baseURL, og)
	}
	doc.Find("a[href]").Each(func(_ int, node *goquery.Selection) {
		href, _ := node.Attr("href")
		link := resolveURLs(baseURL, href)
		if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
			result.Links = appendUnique(result.Links, link)
		}
	})
	result.Images = collectImages(doc, baseURL)
	return result, nil
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func NewSearchWebTool() (tool.InvokableTool, error) {
	return utils.InferTool("searchWeb", "搜索网页并返回标题、链接和摘要。", searchWeb)
}

func NewFetchWebTool() (tool.InvokableTool, error) {
	return utils.InferTool("fetchWeb", "抓取网页完整正文、链接、图片 URL 和 og:image。", fetchWeb)
}

type InspectImageInput struct {
	URL string `json:"url" jsonschema:"description=普通网页图片的 HTTP/HTTPS 地址"`
}

func inspectImage(ctx context.Context, input *InspectImageInput) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.InspectImageCallback == nil {
		return nil, NewTerminalToolError(fmt.Errorf("图片识别能力未初始化"))
	}
	imageURL := ""
	if input != nil {
		imageURL = strings.TrimSpace(input.URL)
	}
	if _, err := projectutils.ParseHTTPURL(imageURL); err != nil {
		return nil, fmt.Errorf("图片 URL 无效：%w", err)
	}
	return tc.InspectImageCallback(ctx, imageURL)
}

func NewInspectImageTool() (tool.InvokableTool, error) {
	return utils.InferTool("inspectImage", "识别普通网页图片 URL 并转成中文描述，不处理 QQ 表情包。", inspectImage)
}
