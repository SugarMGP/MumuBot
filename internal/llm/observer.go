package llm

import (
	"context"
	"net/http"
	"sync"
)

// ResponseObserver 记录本次模型请求真实收到的 HTTP 状态与传输错误
// trpc 适配器错误响应不带状态码，切换时的原模型不可用判定依赖这份观测
type ResponseObserver struct {
	mu      sync.Mutex
	status  int
	lastErr error
}

// WithResponseObserver 把观测器放进上下文，供 requestTransport 写入
func WithResponseObserver(ctx context.Context, observer *ResponseObserver) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, observerKey{}, observer)
}

// TrackerError 取回上下文中观测器记录的最后传输错误
func TrackerError(ctx context.Context) error {
	observer, _ := ctx.Value(observerKey{}).(*ResponseObserver)
	if observer == nil {
		return nil
	}
	return observer.Error()
}

type observerKey struct{}

func (o *ResponseObserver) Record(status int, err error) {
	o.mu.Lock()
	o.status = status
	o.lastErr = err
	o.mu.Unlock()
}

// Status 返回最后一次响应状态码；没有响应时为 0
func (o *ResponseObserver) Status() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.status
}

// Error 返回最后一次传输错误
func (o *ResponseObserver) Error() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastErr
}

// record 由 requestTransport 调用
func (o *ResponseObserver) record(response *http.Response, err error) {
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	o.Record(status, err)
}
