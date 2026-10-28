package agent

import (
	"context"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type modelTimeoutKey struct{}

func withModelTimeout(ctx context.Context, timeout time.Duration) context.Context {
	if timeout <= 0 {
		return ctx
	}
	return context.WithValue(ctx, modelTimeoutKey{}, timeout)
}

type timedAgenticModel struct {
	base           model.AgenticModel
	defaultTimeout time.Duration
}

func newTimedAgenticModel(base model.AgenticModel, defaultTimeout time.Duration) model.AgenticModel {
	return &timedAgenticModel{base: base, defaultTimeout: defaultTimeout}
}

func (m *timedAgenticModel) timeout(ctx context.Context) time.Duration {
	if timeout, ok := ctx.Value(modelTimeoutKey{}).(time.Duration); ok && timeout > 0 {
		return timeout
	}
	return m.defaultTimeout
}

func (m *timedAgenticModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	timeout := m.timeout(ctx)
	if timeout <= 0 {
		return m.base.Generate(ctx, input, opts...)
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return m.base.Generate(callCtx, input, opts...)
}

func (m *timedAgenticModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	// shortcut: 流式调用不单独包超时，当前主链使用 Generate；需要流式超时时再接可关闭的 reader 包装
	return m.base.Stream(ctx, input, opts...)
}
