package utils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/bytedance/sonic"
	"golang.org/x/net/html/charset"
)

func readPublicText(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, publicHTTPResponseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > publicHTTPResponseLimit {
		return nil, fmt.Errorf("公网响应超过大小限制")
	}
	return body, nil
}

// decodePublicText 仅为 HTML 嗅探 meta，JSON 正文在任何字符集转换前原样保留
func decodePublicText(body []byte, contentType string) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	}
	mediaType = strings.ToLower(mediaType)
	if mediaType == "application/json" || mediaType == "text/json" || strings.HasSuffix(mediaType, "+json") || sonic.Valid(body) {
		return body, nil
	}
	var reader io.Reader
	if mediaType == "text/html" || mediaType == "application/xhtml+xml" || mediaType == "" {
		reader, err = charset.NewReader(bytes.NewReader(body), contentType)
	} else if label := params["charset"]; label != "" {
		reader, err = charset.NewReaderLabel(label, bytes.NewReader(body))
	} else {
		return body, nil
	}
	if err != nil {
		return nil, fmt.Errorf("网页字符集转换失败: %w", err)
	}
	return readPublicText(reader)
}

// FetchPublicText 读取公网正文和最终 URL，限制解压及字符集转换后的大小
func FetchPublicText(ctx context.Context, rawURL string) (string, string, error) {
	resp, err := OpenPublicHTTP(ctx, rawURL)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, err := readPublicText(resp.Body)
	if err != nil {
		return "", "", err
	}
	body, err = decodePublicText(body, resp.Header.Get("Content-Type"))
	if err != nil {
		return "", "", err
	}
	return string(body), resp.Request.URL.String(), nil
}
