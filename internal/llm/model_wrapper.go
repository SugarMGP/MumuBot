package llm

import (
	"context"
	"errors"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// ErrorCodeModel 标记模型生成错误，供切换流程判定是否允许目标模型接管
	ErrorCodeModel = "mumu_model_error"
	// ErrorCodeIdle 标记目标模型长时间没有进展
	ErrorCodeIdle = "mumu_model_idle"
)

var errModelIdle = errors.New("模型请求长时间没有响应进展")

// RequestModel 包装 trpc 模型：空闲计时、错误标记按需要开启
type RequestModel struct {
	Base        model.Model
	IdleTimeout time.Duration
}

func (m *RequestModel) Info() model.Info {
	return m.Base.Info()
}

func (m *RequestModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	runCtx := ctx
	var idle *idleTimer
	var cancel context.CancelCauseFunc
	if m.IdleTimeout > 0 {
		runCtx, cancel = context.WithCancelCause(ctx)
		idle = newIdleTimer(m.IdleTimeout, cancel)
	}
	stream, err := m.Base.GenerateContent(runCtx, request)
	if err != nil {
		code := ErrorCodeModel
		if idle != nil {
			if errors.Is(context.Cause(runCtx), errModelIdle) {
				code = ErrorCodeIdle
			}
			idle.stop()
			cancel(nil)
		}
		// 系统级失败也包装成模型错误事件，切换流程才能区分模型故障与持久化故障
		out := make(chan *model.Response, 1)
		out <- &model.Response{Done: true, Error: &model.ResponseError{
			Type: model.ErrorTypeAPIError, Message: err.Error(), Code: &code,
		}}
		close(out)
		return out, nil
	}
	out := make(chan *model.Response)
	go func() {
		defer close(out)
		defer func() {
			if idle != nil {
				idle.stop()
				cancel(nil)
			}
		}()
		emitted := false
		for response := range stream {
			if response == nil {
				continue
			}
			if response.Error != nil {
				emitted = true
				response = markedError(runCtx, response)
			} else if idle != nil && hasModelOutput(response) {
				idle.progress()
			}
			select {
			case out <- response:
			case <-ctx.Done():
				return
			}
		}
		if idle != nil && errors.Is(context.Cause(runCtx), errModelIdle) && !emitted {
			code := ErrorCodeIdle
			select {
			case out <- &model.Response{Done: true, Error: &model.ResponseError{
				Type: model.ErrorTypeAPIError, Message: errModelIdle.Error(), Code: &code,
			}}:
			case <-ctx.Done():
			}
		}
	}()
	return out, nil
}

func markedError(ctx context.Context, original *model.Response) *model.Response {
	code := ErrorCodeModel
	if errors.Is(context.Cause(ctx), errModelIdle) {
		code = ErrorCodeIdle
	}
	copied := original.Clone()
	if copied.Error == nil {
		copied.Error = &model.ResponseError{Type: model.ErrorTypeAPIError}
	}
	copied.Error.Code = &code
	return copied
}

func hasModelOutput(response *model.Response) bool {
	for _, choice := range response.Choices {
		for _, message := range []model.Message{choice.Delta, choice.Message} {
			if message.Content != "" || message.ReasoningContent != "" || message.ReasoningSignature != "" ||
				len(message.ContentParts) > 0 || len(message.ToolCalls) > 0 {
				return true
			}
		}
	}
	return false
}

type idleTimer struct {
	timeout time.Duration
	last    time.Time
	timer   *time.Timer
	cancel  context.CancelCauseFunc
	mu      sync.Mutex
	stopped bool
}

func newIdleTimer(timeout time.Duration, cancel context.CancelCauseFunc) *idleTimer {
	timer := &idleTimer{timeout: timeout, last: time.Now(), cancel: cancel}
	timer.timer = time.AfterFunc(timeout, timer.fire)
	return timer
}

func (t *idleTimer) fire() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	if remaining := time.Until(t.last.Add(t.timeout)); remaining > 0 {
		t.timer.Reset(remaining)
		return
	}
	t.cancel(errModelIdle)
}

func (t *idleTimer) progress() {
	t.mu.Lock()
	t.last = time.Now()
	t.mu.Unlock()
}

func (t *idleTimer) stop() {
	t.mu.Lock()
	t.stopped = true
	t.mu.Unlock()
	t.timer.Stop()
}
