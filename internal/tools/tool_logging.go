package tools

import (
	"context"
	"time"

	"mumu-bot/internal/logger"

	"github.com/cloudwego/eino/compose"
	"go.uber.org/zap"
)

// ToolLogMiddleware 在结果外置前记录完整文本，普通和多模态工具共用同一口径
func ToolLogMiddleware() compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				finish := startToolLog(ctx, input)
				output, err := next(ctx, input)
				var result any
				if output != nil {
					result = output.Result
				}
				finish(result, err == nil && toolOutputSucceeded(output), err)
				return output, err
			}
		},
		EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
				finish := startToolLog(ctx, input)
				output, err := next(ctx, input)
				var result any
				success := false
				if output != nil {
					result = output.Result
					success = output.Result != nil && toolOutputSucceeded(&compose.ToolOutput{Result: toolResultText(output.Result)})
				}
				finish(result, err == nil && success, err)
				return output, err
			}
		},
	}
}

func startToolLog(ctx context.Context, input *compose.ToolInput) func(any, bool, error) {
	log := logger.FromContext(ctx).With(zap.String("tool", input.Name), zap.String("call_id", input.CallID))
	content := logger.ContentEnabled(ctx)
	if content {
		log.Info("工具调用开始", zap.Any("arguments", logger.Payload(input.Arguments)))
	} else {
		log.Info("工具调用开始")
	}
	started := time.Now()
	return func(result any, success bool, err error) {
		fields := []zap.Field{zap.Duration("duration", time.Since(started)), zap.Bool("success", success)}
		if content {
			fields = append(fields, zap.Any("result", logger.Payload(result)))
		}
		if err != nil {
			fields = append(fields, zap.Error(err))
		}
		if success {
			log.Info("工具调用完成", fields...)
		} else {
			log.Warn("工具调用失败", fields...)
		}
	}
}
