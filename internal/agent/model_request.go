package agent

import (
	"context"
	"errors"
	"net"
	"time"

	"mumu-bot/internal/llm"
)

// modelRequestError 只标记模型生成错误，持久化或工具错误不能触发模型接管
type modelRequestError struct{ error }

func (e *modelRequestError) Unwrap() error { return e.error }

const switchTargetIdleTimeout = 360 * time.Second

var errModelRequestIdle = errors.New("目标模型请求长时间没有响应进展")

// modelRequestDeadline 只限制模型网络等待，已生成的有效摘要仍能正常校验和提交
func modelRequestDeadline(deadline time.Time) llm.RequestTracker {
	return func(ctx context.Context) (context.Context, func(), error) {
		requestCtx, cancel := context.WithDeadline(ctx, deadline)
		return requestCtx, cancel, nil
	}
}

// unavailableModel 依据 HTTP 观测判断原模型本次是否明确不可用；参数或窗口错误只影响当前会话
func unavailableModel(observer *llm.ResponseObserver) bool {
	if observer == nil {
		return false
	}
	status := observer.Status()
	if status != 0 {
		return status == 401 || status == 402 || status == 403 || status == 404 || status == 405 ||
			status == 408 || status == 429 || status >= 500
	}
	var networkErr net.Error
	return errors.As(observer.Error(), &networkErr)
}
