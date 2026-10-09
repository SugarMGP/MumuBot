package utils

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const (
	publicHTTPResponseLimit  = 8 << 20
	publicHTTPRequestTimeout = 20 * time.Second
)

// publicHTTPClient 是项目共用的公网 HTTP 客户端，TLS 与 HTTP/2 指纹交给 tls-client 维护
var publicHTTPClient = newPublicHTTPClient()

func newPublicHTTPClient() tls_client.HttpClient {
	options := []tls_client.HttpClientOption{
		tls_client.WithClientProfile(profiles.Chrome_146),
		tls_client.WithDisableHttp3(),
		tls_client.WithTimeoutMilliseconds(int(publicHTTPRequestTimeout / time.Millisecond)),
		tls_client.WithTransportOptions(&tls_client.TransportOptions{
			IdleConnTimeout: durationPtr(90 * time.Second),
			MaxIdleConns:    16,
		}),
	}
	if proxy := publicProxyFromEnv(); proxy != "" {
		options = append(options, tls_client.WithProxyUrl(proxy))
	}
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		panic(fmt.Sprintf("初始化公网 HTTP 客户端失败: %v", err))
	}
	return client
}

// publicProxyFromEnv 只读取常见代理环境变量，NO_PROXY 不参与判断，值或协议不支持时跳过
func publicProxyFromEnv() string {
	supported := map[string]bool{"http": true, "https": true, "socks4": true, "socks4a": true, "socks5": true, "socks5h": true}
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			continue
		}
		u, err := url.Parse(value)
		if err == nil && u.Host != "" && supported[strings.ToLower(u.Scheme)] {
			return value
		}
	}
	return ""
}

func durationPtr(value time.Duration) *time.Duration { return &value }

// ParseHTTPURL 校验外部地址只使用不带凭据的 HTTP/HTTPS
func ParseHTTPURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("仅支持不带凭据的 HTTP/HTTPS URL")
	}
	return u, nil
}

// OpenPublicHTTP 发起公网 GET 请求并保留响应流，调用方必须关闭 Body
func OpenPublicHTTP(ctx context.Context, rawURL string) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	u, err := ParseHTTPURL(rawURL)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		applyPublicHeaders(req)
		resp, err := publicHTTPClient.Do(req)
		if err != nil {
			if attempt == 0 && ctx.Err() == nil && waitRetry(ctx, retryDelay(nil)) == nil {
				continue
			}
			return nil, err
		}
		wrapped := wrapPublicResponse(resp)
		if encoding := strings.TrimSpace(wrapped.Header.Get("Content-Encoding")); !wrapped.Uncompressed && encoding != "" && !strings.EqualFold(encoding, "identity") {
			_ = wrapped.Body.Close()
			return nil, fmt.Errorf("不支持的响应压缩编码: %s", encoding)
		}
		if wrapped.Uncompressed {
			// 库只负责一层自动解压；若压缩头里还剩多层编码，宁可报错也不要静默漏解
			codings := strings.Split(strings.Join(wrapped.Header.Values("Content-Encoding"), ","), ",")
			remaining := 0
			for _, coding := range codings {
				coding = strings.ToLower(strings.TrimSpace(coding))
				if coding != "" && coding != "identity" {
					remaining++
				}
			}
			if remaining > 1 {
				_ = wrapped.Body.Close()
				return nil, fmt.Errorf("不支持的响应压缩编码: %s", wrapped.Header.Get("Content-Encoding"))
			}
			wrapped.Header.Del("Content-Encoding")
			wrapped.Header.Del("Content-Length")
			wrapped.ContentLength = -1
		}
		if wrapped.StatusCode == http.StatusTooManyRequests || wrapped.StatusCode == http.StatusServiceUnavailable {
			if attempt == 0 {
				delay := retryDelay(wrapped)
				_ = wrapped.Body.Close()
				if err := waitRetry(ctx, delay); err != nil {
					// 退避期间上下文已取消时返回真实取消原因，不再伪装成 HTTP 失败
					return nil, err
				}
				continue
			}
		}
		if wrapped.StatusCode < 200 || wrapped.StatusCode >= 300 {
			_ = wrapped.Body.Close()
			return nil, fmt.Errorf("公网请求失败：HTTP %d", wrapped.StatusCode)
		}
		return wrapped, nil
	}
	return nil, fmt.Errorf("公网请求失败")
}

// wrapPublicResponse 把 fhttp 响应转成标准库响应，保持现有调用方不变
func wrapPublicResponse(resp *fhttp.Response) *http.Response {
	result := &http.Response{
		Status:        resp.Status,
		StatusCode:    resp.StatusCode,
		Proto:         resp.Proto,
		ProtoMajor:    resp.ProtoMajor,
		ProtoMinor:    resp.ProtoMinor,
		Header:        http.Header(resp.Header),
		Body:          resp.Body,
		ContentLength: resp.ContentLength,
		Uncompressed:  resp.Uncompressed,
	}
	requestURL := &url.URL{}
	if resp.Request != nil && resp.Request.URL != nil {
		requestURL = resp.Request.URL
	}
	result.Request = &http.Request{URL: requestURL}
	return result
}

// applyPublicHeaders 设置浏览器风格的请求头，并按发送顺序登记 header order
func applyPublicHeaders(req *fhttp.Request) {
	headers := req.Header
	headers.Append("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36")
	headers.Append("Accept", "text/html,application/xhtml+xml,application/xml,application/json;q=0.9,image/avif,image/webp,*/*;q=0.8")
	headers.Append("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.7")
	headers.Append("Cache-Control", "max-age=0")
	headers.Append("Upgrade-Insecure-Requests", "1")
	headers.Append("Sec-Fetch-Dest", "document")
	headers.Append("Sec-Fetch-Mode", "navigate")
	headers.Append("Sec-Fetch-Site", "none")
	headers.Append("Sec-Fetch-User", "?1")
	headers.Append("sec-ch-ua", `"Chromium";v="146", "Not(A:Brand";v="24", "Google Chrome";v="146"`)
	headers.Append("sec-ch-ua-mobile", "?0")
	headers.Append("sec-ch-ua-platform", `"Windows"`)
}

func retryDelay(resp *http.Response) time.Duration {
	if resp != nil {
		value := strings.TrimSpace(resp.Header.Get("Retry-After"))
		if seconds, err := strconv.ParseUint(value, 10, 64); err == nil && seconds <= 20 {
			return time.Duration(seconds) * time.Second
		}
		if until, err := http.ParseTime(value); err == nil {
			if wait := time.Until(until); wait > 0 && wait <= publicHTTPRequestTimeout {
				return wait
			}
		}
	}
	return 1200 * time.Millisecond
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
