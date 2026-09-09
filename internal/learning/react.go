package learning

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// The framework owns the loop; this per-investigation model only enforces request policy.
type memoryChatModel struct {
	model.ToolCallingChatModel
	beforeRequest func(context.Context) error
	responses     int
}

func (m *memoryChatModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := m.ToolCallingChatModel.WithTools(infos)
	if err != nil {
		return nil, err
	}
	return &memoryChatModel{ToolCallingChatModel: bound, beforeRequest: m.beforeRequest}, nil
}

func (m *memoryChatModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if m.responses >= maxMemorySteps {
		return nil, noFinishError()
	}
	if m.responses == maxMemorySteps-1 {
		messages = append(append([]*schema.Message(nil), messages...), schema.UserMessage("这是最后一次模型响应，请单独调用 finishMemoryBatch 提交已完成调查；证据不足保持 candidate。"))
	}
	if err := m.beforeRequest(ctx); err != nil {
		return nil, err
	}
	m.responses++
	response, err := m.ToolCallingChatModel.Generate(ctx, messages, append(opts, model.WithMaxTokens(8192))...)
	if err != nil {
		return nil, err
	}
	if response == nil || len(response.ToolCalls) == 0 {
		return nil, noFinishError()
	}
	for _, call := range response.ToolCalls {
		if call.Function.Name == "finishMemoryBatch" && len(response.ToolCalls) != 1 {
			return nil, fmt.Errorf("终结提交必须单独调用")
		}
	}
	return response, nil
}

func (r *investigation) readBudget(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
	return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		if input.Name == "finishMemoryBatch" {
			return next(ctx, input)
		}
		r.readCalls++
		if r.readCalls > maxMemoryReadCalls {
			return nil, noFinishError()
		}
		output, err := next(ctx, input)
		if err != nil {
			return nil, err
		}
		count := utf8.RuneCountInString(output.Result)
		r.textChars += count
		if count > 8000 || r.textChars > 24000 {
			return nil, fmt.Errorf("记忆调查读取预算耗尽")
		}
		return output, nil
	}
}
