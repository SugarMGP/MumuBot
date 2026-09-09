package llm

import (
	"context"

	"mumu-bot/internal/modelstats"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
)

func WithTask(ctx context.Context, task, modelName string) context.Context {
	return callbacks.InitCallbacks(ctx, &callbacks.RunInfo{Component: components.ComponentOfChatModel}, modelstats.Handler(task, modelName))
}
