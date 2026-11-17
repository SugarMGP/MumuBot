package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
)

// RequestTracker 在真实 HTTP 请求期间登记取消句柄，响应体关闭后注销
type RequestTracker func(context.Context) (context.Context, func(), error)

type requestTrackerKey struct{}

// WithRequestTracker 只跟踪当前调用的网络请求，不取消工具所在的上层上下文
func WithRequestTracker(ctx context.Context, tracker RequestTracker) context.Context {
	return context.WithValue(ctx, requestTrackerKey{}, tracker)
}

type requestTransport struct{ base http.RoundTripper }

func (t *requestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	tracker, _ := request.Context().Value(requestTrackerKey{}).(RequestTracker)
	if tracker == nil {
		return t.base.RoundTrip(request)
	}
	ctx, finish, err := tracker(request.Context())
	if err != nil {
		if request.Body != nil {
			request.Body.Close()
		}
		return nil, err
	}
	response, err := t.base.RoundTrip(request.Clone(ctx))
	if err != nil {
		cause := context.Cause(ctx)
		finish()
		return nil, errors.Join(err, cause)
	}
	response.Body = &trackedResponseBody{ReadCloser: response.Body, ctx: ctx, finish: finish}
	return response, nil
}

type trackedResponseBody struct {
	io.ReadCloser
	ctx    context.Context
	finish func()
	once   sync.Once
}

func (b *trackedResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		cause := context.Cause(b.ctx)
		b.once.Do(b.finish)
		if cause != nil {
			return n, cause
		}
	}
	return n, err
}

func (b *trackedResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.finish)
	return err
}
