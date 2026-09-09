package llm

import (
	"context"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"mumu-bot/internal/modelstats"
)

func WithTask(ctx context.Context, task, modelName string) context.Context {
	return callbacks.InitCallbacks(ctx, &callbacks.RunInfo{Component: components.ComponentOfChatModel}, modelstats.Handler(task, modelName))
}
