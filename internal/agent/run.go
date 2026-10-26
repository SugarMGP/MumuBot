package agent

import (
	"context"

	"mumu-bot/internal/config"
	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/tools"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// runAgent 跑一轮主模型，返回本轮模型产生的消息
func (a *Agent) runAgent(ctx context.Context, agent *adk.TypedChatModelAgent[*schema.AgenticMessage], messages []*schema.AgenticMessage) ([]*schema.AgenticMessage, error) {
	runner := adk.NewTypedRunner[*schema.AgenticMessage](adk.TypedRunnerConfig[*schema.AgenticMessage]{Agent: agent})
	iter := runner.Run(ctx, messages)
	var produced []*schema.AgenticMessage
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return produced, event.Err
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		message, err := event.Output.MessageOutput.GetMessage()
		if err != nil {
			return produced, err
		}
		if message != nil {
			produced = append(produced, message)
		}
	}
	return produced, nil
}

// sessionInjector 把运行期间到达的新消息插到模型调用边界
type sessionInjector struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
}

// BeforeModelRewriteState 在每次模型调用前补上本轮运行期间到达的消息
func (h *sessionInjector) BeforeModelRewriteState(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], mc *adk.TypedModelContext[*schema.AgenticMessage]) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	tc := tools.GetToolContext(ctx)
	if tc == nil || tc.Session == nil {
		return ctx, state, nil
	}
	floor := tc.ThinkStartArrivalSeq
	pending := tc.Session.PendingAfter(floor)
	if len(pending) == 0 {
		return ctx, state, nil
	}
	messages := make([]*schema.AgenticMessage, 0, len(pending))
	observed := floor
	for _, item := range pending {
		messages = append(messages, schema.UserAgenticMessage(item.Content))
		observed = max(observed, item.ArrivalSeq)
	}
	state.Messages = append(state.Messages, messages...)
	tc.ObservedThroughSeq = observed
	tc.Session.MarkInjected(observed)
	return ctx, state, nil
}

// appendSessionMessage 把落库后的消息放进会话
func (a *Agent) appendSessionMessage(msg *onebot.ConversationMessage, mediaRefs []media.Ref, log *memory.MessageLog) {
	session := a.sessions.get(msg.ConversationKind, msg.TargetID)
	logID := uint(0)
	if log != nil {
		logID = log.ID
	}
	if msg.MessageID != 0 && msg.UserID == a.bot.GetSelfID() {
		// 自身发言已经出现在模型的工具轨迹里，只推进边界，不再重复进入上下文
		session.MarkConsumed(msg.ArrivalSeq)
		return
	}
	session.AppendUser(msg, logID, mediaRefs)
}

// markSessionRecalled 标记会话里的消息已撤回，上下文保留原文
func (a *Agent) markSessionRecalled(kind string, targetID, messageID int64) {
	a.sessions.get(kind, targetID).MarkRecalledByMessageID(messageID)
}

// sessionBudget 算出本轮可用的上下文预算
func sessionBudget() int {
	cfg := config.Get()
	window := cfg.ModelTiers.High.ContextWindow
	if window <= 0 {
		return 0
	}
	return int(float64(window) * cfg.Agent.ContextBudgetRatio)
}
