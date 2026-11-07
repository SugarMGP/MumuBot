package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"mumu-bot/internal/workerproto"
)

type workerFileContent struct {
	Data        []byte
	ContentType string
}

func (e *workerExecutor) postFile(ctx context.Context, route string, request, result any, timeout time.Duration) error {
	if e == nil || e.baseURL == "" {
		return fmt.Errorf("worker executor 未配置")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	httpCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(httpCtx, http.MethodPost, e.baseURL+route, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("worker executor 不可达: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var failure workerproto.ErrorResponse
		_ = json.Unmarshal(raw, &failure)
		if failure.Error != "" {
			return fmt.Errorf("worker executor 失败: %s", failure.Error)
		}
		return fmt.Errorf("worker executor 返回 HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("解析 worker executor 结果失败: %w", err)
	}
	return nil
}

func (e *workerExecutor) listFiles(ctx context.Context, request workerproto.FileListRequest) (*workerproto.FileListResponse, error) {
	var result workerproto.FileListResponse
	if err := e.postFile(ctx, "/v1/files/list", request, &result, 30*time.Second); err != nil {
		return nil, err
	}
	return &result, nil
}

func (e *workerExecutor) readFile(ctx context.Context, request workerproto.FileReadRequest) (*workerproto.FileReadResponse, error) {
	var result workerproto.FileReadResponse
	if err := e.postFile(ctx, "/v1/files/read", request, &result, 30*time.Second); err != nil {
		return nil, err
	}
	return &result, nil
}

func (e *workerExecutor) writeFile(ctx context.Context, request workerproto.FileWriteRequest) (*workerproto.FileWriteResponse, error) {
	var result workerproto.FileWriteResponse
	if err := e.postFile(ctx, "/v1/files/write", request, &result, 30*time.Second); err != nil {
		return nil, err
	}
	return &result, nil
}

func (e *workerExecutor) saveFile(ctx context.Context, request workerproto.FileSaveRequest) (*workerproto.FileSaveResponse, error) {
	var result workerproto.FileSaveResponse
	if err := e.postFile(ctx, "/v1/files/save", request, &result, 150*time.Second); err != nil {
		return nil, err
	}
	return &result, nil
}

func (e *workerExecutor) fileContent(ctx context.Context, path string) (*workerFileContent, error) {
	if e == nil || e.baseURL == "" {
		return nil, fmt.Errorf("worker executor 未配置")
	}
	httpCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	endpoint := e.baseURL + "/v1/files/content?path=" + url.QueryEscape(path)
	req, err := http.NewRequestWithContext(httpCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("worker executor 不可达: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, workerproto.MaxFileBytes+1))
	if resp.StatusCode != http.StatusOK {
		if len(raw) > 0 && len(raw) < 1024 {
			return nil, fmt.Errorf("worker executor 失败: %s", strings.TrimSpace(string(raw)))
		}
		return nil, fmt.Errorf("worker executor 返回 HTTP %d", resp.StatusCode)
	}
	if len(raw) > workerproto.MaxFileBytes {
		return nil, fmt.Errorf("文件超过 32 MiB")
	}
	size, _ := strconv.ParseInt(resp.Header.Get("X-Mumu-File-Size"), 10, 64)
	if size > 0 && int64(len(raw)) != size {
		return nil, fmt.Errorf("worker 文件长度不符: 声明 %d，实际 %d", size, len(raw))
	}
	return &workerFileContent{Data: raw, ContentType: resp.Header.Get("Content-Type")}, nil
}

// fileContentStream 返回 worker 受限字节流，调用方负责关闭
func (e *workerExecutor) fileContentStream(ctx context.Context, path string) (io.ReadCloser, int64, error) {
	if e == nil || e.baseURL == "" {
		return nil, 0, fmt.Errorf("worker executor 未配置")
	}
	httpCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	req, err := http.NewRequestWithContext(httpCtx, http.MethodGet, e.baseURL+"/v1/files/content?path="+url.QueryEscape(path), nil)
	if err != nil {
		cancel()
		return nil, 0, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		cancel()
		return nil, 0, fmt.Errorf("worker executor 不可达: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		cancel()
		return nil, 0, fmt.Errorf("worker executor 返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	size, _ := strconv.ParseInt(resp.Header.Get("X-Mumu-File-Size"), 10, 64)
	return &cancelReadCloser{ReadCloser: resp.Body, cancel: cancel}, size, nil
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// putFileContent 把快照字节以原始 body 导入 worker，不经过 JSON/Base64
func (e *workerExecutor) putFileContent(ctx context.Context, path string, overwrite bool, body io.Reader, size int64) (*workerproto.FileWriteResponse, error) {
	if e == nil || e.baseURL == "" {
		return nil, fmt.Errorf("worker executor 未配置")
	}
	httpCtx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	endpoint := e.baseURL + "/v1/files/content?path=" + url.QueryEscape(path) + "&overwrite=" + strconv.FormatBool(overwrite)
	req, err := http.NewRequestWithContext(httpCtx, http.MethodPut, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = size
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("worker executor 不可达: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var failure workerproto.ErrorResponse
		_ = json.Unmarshal(raw, &failure)
		if failure.Error != "" {
			return nil, fmt.Errorf("worker executor 失败: %s", failure.Error)
		}
		return nil, fmt.Errorf("worker executor 返回 HTTP %d", resp.StatusCode)
	}
	var result workerproto.FileWriteResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
