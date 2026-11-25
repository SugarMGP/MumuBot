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

// TrackerFrom 返回当前上下文登记的请求跟踪器；已有跟踪时运行层不再覆盖
func TrackerFrom(ctx context.Context) RequestTracker {
	tracker, _ := ctx.Value(requestTrackerKey{}).(RequestTracker)
	return tracker
}

type requestTransport struct{ base http.RoundTripper }

func (t *requestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	observer, _ := request.Context().Value(observerKey{}).(*ResponseObserver)
	tracker, _ := request.Context().Value(requestTrackerKey{}).(RequestTracker)
	if tracker == nil {
		response, err := t.base.RoundTrip(request)
		if observer != nil {
			observer.record(response, err)
		}
		return response, err
	}
	ctx, finish, err := tracker(request.Context())
	if err != nil {
		if request.Body != nil {
			request.Body.Close()
		}
		if observer != nil {
			observer.record(nil, err)
		}
		return nil, err
	}
	response, err := t.base.RoundTrip(request.Clone(ctx))
	if err != nil {
		cause := context.Cause(ctx)
		finish()
		err = errors.Join(err, cause)
		if observer != nil {
			observer.record(nil, err)
		}
		return nil, err
	}
	if observer != nil {
		observer.record(response, nil)
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
