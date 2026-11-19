package agent

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"mumu-bot/internal/llm"

	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	chatopenai "github.com/meguminnnnnnnnn/go-openai"
	"github.com/openai/openai-go/v3"
)

// modelRequestError 只标记模型生成错误，持久化或工具错误不能触发模型接管
type modelRequestError struct{ error }

func (e *modelRequestError) Unwrap() error { return e.error }

const switchTargetIdleTimeout = 360 * time.Second

var errModelRequestIdle = errors.New("目标模型请求长时间没有响应进展")

type requestModel struct {
	model.AgenticModel
	idleTimeout time.Duration
}

func (m *requestModel) GetType() string {
	name, _ := components.GetType(m.AgenticModel)
	return name
}

func (m *requestModel) IsCallbacksEnabled() bool {
	return components.IsCallbacksEnabled(m.AgenticModel)
}

func (m *requestModel) Generate(ctx context.Context, messages []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	if m.idleTimeout > 0 {
		ctx, _ = withModelRequestIdle(ctx, m.idleTimeout)
	}
	message, err := m.AgenticModel.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, wrapModelRequestError(ctx, err)
	}
	return message, nil
}

func (m *requestModel) Stream(ctx context.Context, messages []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	var progress func()
	if m.idleTimeout > 0 {
		ctx, progress = withModelRequestIdle(ctx, m.idleTimeout)
	}
	reader, err := m.AgenticModel.Stream(ctx, messages, opts...)
	if err != nil {
		return nil, wrapModelRequestError(ctx, err)
	}
	return schema.StreamReaderWithConvert(reader, func(message *schema.AgenticMessage) (*schema.AgenticMessage, error) {
		if progress != nil && hasModelOutput(message) {
			progress()
		}
		return message, nil
	}, schema.WithErrWrapper(func(err error) error { return wrapModelRequestError(ctx, err) })), nil
}

func wrapModelRequestError(ctx context.Context, err error) error {
	if context.Cause(ctx) == errModelRequestIdle && !errors.Is(err, errModelRequestIdle) {
		err = errors.Join(err, errModelRequestIdle)
	}
	return &modelRequestError{err}
}

func hasModelOutput(message *schema.AgenticMessage) bool {
	if message == nil {
		return false
	}
	for _, block := range message.ContentBlocks {
		if block == nil {
			continue
		}
		switch {
		case block.AssistantGenText != nil:
			if block.AssistantGenText.Text != "" {
				return true
			}
		case block.Reasoning != nil:
			if block.Reasoning.Text != "" || block.Reasoning.Signature != "" {
				return true
			}
			if extension := block.Reasoning.OpenAIExtension; extension != nil {
				for _, part := range extension.Content {
					if part != nil && part.Text != "" {
						return true
					}
				}
			}
		case block.FunctionToolCall != nil:
			if block.FunctionToolCall.Name != "" || block.FunctionToolCall.Arguments != "" {
				return true
			}
		case block.AssistantGenImage != nil:
			return true
		}
	}
	return false
}

// withModelRequestIdle 只在 HTTP 请求期间计时；流式心跳和空分片不算生成进展
func withModelRequestIdle(ctx context.Context, timeout time.Duration) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	var mu sync.Mutex
	var lastProgress time.Time
	tracker := func(reqCtx context.Context) (context.Context, func(), error) {
		mu.Lock()
		defer mu.Unlock()
		if lastProgress.IsZero() {
			lastProgress = time.Now()
		}
		var timer *time.Timer
		finished := false
		timer = time.AfterFunc(max(time.Until(lastProgress.Add(timeout)), 0), func() {
			mu.Lock()
			defer mu.Unlock()
			if finished {
				return
			}
			if remaining := time.Until(lastProgress.Add(timeout)); remaining > 0 {
				timer.Reset(remaining)
			} else {
				cancel(errModelRequestIdle)
			}
		})
		return reqCtx, func() {
			mu.Lock()
			defer mu.Unlock()
			finished = true
			timer.Stop()
		}, nil
	}
	return llm.WithRequestTracker(ctx, tracker), func() {
		mu.Lock()
		lastProgress = time.Now()
		mu.Unlock()
	}
}

// modelRequestDeadline 只限制模型网络等待，已生成的有效摘要仍能正常校验和提交
func modelRequestDeadline(deadline time.Time) llm.RequestTracker {
	return func(ctx context.Context) (context.Context, func(), error) {
		requestCtx, cancel := context.WithDeadline(ctx, deadline)
		return requestCtx, cancel, nil
	}
}

func unavailableModel(err error) bool {
	status := 0
	var chatErr *chatopenai.APIError
	var requestErr *chatopenai.RequestError
	var responsesErr *openai.Error
	switch {
	case errors.As(err, &chatErr):
		status = chatErr.HTTPStatusCode
	case errors.As(err, &requestErr):
		status = requestErr.HTTPStatusCode
	case errors.As(err, &responsesErr):
		status = responsesErr.StatusCode
	}
	if status != 0 {
		return status == 401 || status == 402 || status == 403 || status == 404 || status == 405 || status == 408 || status == 429 || status >= 500
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}
