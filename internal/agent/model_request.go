package agent

import (
	"context"
	"errors"
	"net"
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

type requestModel struct{ model.AgenticModel }

func (m *requestModel) GetType() string {
	name, _ := components.GetType(m.AgenticModel)
	return name
}

func (m *requestModel) IsCallbacksEnabled() bool {
	return components.IsCallbacksEnabled(m.AgenticModel)
}

func (m *requestModel) Generate(ctx context.Context, messages []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	message, err := m.AgenticModel.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, &modelRequestError{err}
	}
	return message, nil
}

func (m *requestModel) Stream(ctx context.Context, messages []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	reader, err := m.AgenticModel.Stream(ctx, messages, opts...)
	if err != nil {
		return nil, &modelRequestError{err}
	}
	return schema.StreamReaderWithConvert(reader, func(message *schema.AgenticMessage) (*schema.AgenticMessage, error) {
		return message, nil
	}, schema.WithErrWrapper(func(err error) error { return &modelRequestError{err} })), nil
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
