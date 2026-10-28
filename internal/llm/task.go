package llm

import (
	"context"

	"mumu-bot/internal/modelstats"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
)

// WithTask 给主模型请求附加统计和额外回调处理器
func WithTask(ctx context.Context, task, modelName string, extra ...callbacks.Handler) context.Context {
	handlers := make([]callbacks.Handler, 0, 1+len(extra))
	handlers = append(handlers, modelstats.Handler(task, modelName))
	handlers = append(handlers, extra...)
	return callbacks.InitCallbacks(ctx, &callbacks.RunInfo{Component: components.ComponentOfChatModel}, handlers...)
}
