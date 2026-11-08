package llm

import (
	"context"

	"mumu-bot/internal/modelstats"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
)

// WithTask 给模型请求统一附加统计和日志
func WithTask(ctx context.Context, task, modelName string) context.Context {
	return callbacks.InitCallbacks(ctx, &callbacks.RunInfo{Component: components.ComponentOfAgenticModel}, modelstats.Handler(task, modelName), modelLogHandler(task, modelName))
}
