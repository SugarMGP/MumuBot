package llm

import (
	"context"
	"time"

	"mumu-bot/internal/logger"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	callbacktpl "github.com/cloudwego/eino/utils/callbacks"
	"go.uber.org/zap"
)

type modelLogStartKey struct{}

func modelLogHandler(task, modelName string) callbacks.Handler {
	fields := []zap.Field{zap.String("task", task), zap.String("model", modelName)}
	return callbacktpl.NewHandlerHelper().AgenticModel(&callbacktpl.AgenticModelCallbackHandler{
		OnStart: func(ctx context.Context, _ *callbacks.RunInfo, input *model.AgenticCallbackInput) context.Context {
			log := logger.FromContext(ctx).With(fields...)
			if input != nil {
				log = log.With(zap.Int("message_count", len(input.Messages)), zap.Int("tool_count", len(input.Tools)))
				if logger.ContentEnabled(ctx) {
					log = log.With(zap.Any("input", logger.Payload(input.Messages)), zap.Any("tools", logger.Payload(input.Tools)))
				}
			}
			log.Info("模型请求开始")
			return context.WithValue(ctx, modelLogStartKey{}, time.Now())
		},
		OnEnd: func(ctx context.Context, _ *callbacks.RunInfo, output *model.AgenticCallbackOutput) context.Context {
			log := logger.FromContext(ctx).With(fields...).With(zap.Duration("duration", modelLogDuration(ctx)))
			if output != nil {
				log = log.With(zap.Any("usage", output.TokenUsage))
				if logger.ContentEnabled(ctx) {
					log = log.With(zap.Any("output", logger.Payload(output.Message)))
				}
			}
			log.Info("模型请求完成")
			return ctx
		},
		OnError: func(ctx context.Context, _ *callbacks.RunInfo, err error) context.Context {
			logger.FromContext(ctx).With(fields...).Error("模型请求失败", zap.Duration("duration", modelLogDuration(ctx)), zap.Error(err))
			return ctx
		},
	}).Handler()
}

func modelLogDuration(ctx context.Context) time.Duration {
	started, _ := ctx.Value(modelLogStartKey{}).(time.Time)
	if started.IsZero() {
		return 0
	}
	return time.Since(started)
}
