package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mumu-bot/internal/tools"
	"mumu-bot/internal/workerproto"
)

type workerExecutor struct {
	baseURL string
	client  *http.Client
}

func newWorkerExecutor(baseURL string) *workerExecutor {
	return &workerExecutor{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (e *workerExecutor) exec(ctx context.Context, req workerproto.ExecRequest) (*workerproto.ExecResponse, error) {
	if e == nil || e.baseURL == "" {
		return nil, fmt.Errorf("worker executor 未配置")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = workerproto.DefaultTimeoutSeconds
	}
	httpCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout+15)*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(httpCtx, http.MethodPost, e.baseURL+"/v1/exec", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(httpReq)
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
	var out workerproto.ExecResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析 worker executor 结果失败: %w", err)
	}
	return &out, nil
}

func (a *Agent) workerBashCallback(workerID uint) tools.BashCallback {
	return func(ctx context.Context, command string, timeoutSeconds int) (*tools.BashResult, error) {
		session, err := a.memory.GetWorkerSessionByID(ctx, workerID)
		if err != nil {
			return nil, fmt.Errorf("读取 worker 失败: %w", err)
		}
		out, err := a.workerExec.exec(ctx, workerproto.ExecRequest{
			SessionID: workerID, User: session.UserName, Home: session.HomeDir,
			Command: command, TimeoutSeconds: timeoutSeconds,
		})
		if err != nil {
			return nil, err
		}
		return &tools.BashResult{
			Stdout: out.Stdout, Stderr: out.Stderr, ExitCode: out.ExitCode,
			TimedOut: out.TimedOut, Truncated: out.Truncated,
		}, nil
	}
}
