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
	"go.uber.org/zap"
)

// runAgent 跑一轮主模型，模型产生的消息由会话收集器统一写回
func (a *Agent) runAgent(ctx context.Context, agent *adk.TypedChatModelAgent[*schema.AgenticMessage], messages []*schema.AgenticMessage) error {
	runner := adk.NewTypedRunner[*schema.AgenticMessage](adk.TypedRunnerConfig[*schema.AgenticMessage]{Agent: agent})
	iter := runner.Run(ctx, messages)
	for {
		event, ok := iter.Next()
		if !ok {
			return nil
		}
		if event.Err != nil {
			return event.Err
		}
	}
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
	pending := tc.Session.PendingAfter(tc.ThinkStartArrivalSeq)
	if len(pending) == 0 {
		return ctx, state, nil
	}
	messages := make([]*schema.AgenticMessage, 0, len(pending))
	observed := tc.ThinkStartArrivalSeq
	for _, item := range pending {
		// 直接复用会话里渲染好的消息，内容进入上下文后不再变化
		messages = append(messages, item.Msg)
		observed = max(observed, item.ArrivalSeq)
	}
	state.Messages = append(state.Messages, messages...)
	tc.ObservedThroughSeq = observed
	tc.Session.MarkInjected(observed)
	return ctx, state, nil
}

// sessionCollector 收集本轮模型实际看到的消息，原样写回会话
type sessionCollector struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
}

// AfterAgent 在轮次结束时把助手回复、工具结果与期间注入的消息并入会话
func (h *sessionCollector) AfterAgent(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage]) (context.Context, error) {
	tc := tools.GetToolContext(ctx)
	if tc == nil || tc.Session == nil {
		return ctx, nil
	}
	messages := state.Messages
	if len(messages) > 0 && messages[0].Role == schema.AgenticRoleTypeSystem {
		messages = messages[1:]
	}
	tc.Session.CommitRound(messages)
	return ctx, nil
}

// appendSessionMessage 把落库后的消息放进会话
func (a *Agent) appendSessionMessage(ctx context.Context, msg *onebot.ConversationMessage, mediaRefs []media.Ref, log *memory.MessageLog) {
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

// markCollectedStickers 标记哪些表情已经在收藏库，消息里据此显示已收藏
func (a *Agent) markCollectedStickers(ctx context.Context, mediaRefs []media.Ref) {
	hashes := make([]string, 0, len(mediaRefs))
	for _, ref := range mediaRefs {
		if ref.Sticker && ref.Hash != "" {
			hashes = append(hashes, ref.Hash)
		}
	}
	if len(hashes) == 0 {
		return
	}
	collected, err := a.memory.WithContext(ctx).CollectedStickerHashes(hashes)
	if err != nil {
		zap.L().Warn("查询表情收藏状态失败", zap.Error(err))
		return
	}
	for index := range mediaRefs {
		if collected[mediaRefs[index].Hash] {
			mediaRefs[index].Collected = true
		}
	}
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

// groupGenModelInput 生成群聊模型输入：系统指令在前，会话消息在后
func (a *Agent) groupGenModelInput(ctx context.Context, _ string, input *adk.TypedAgentInput[*schema.AgenticMessage]) ([]*schema.AgenticMessage, error) {
	groupExtra := ""
	if tc := tools.GetToolContext(ctx); tc != nil {
		extra, err := a.memory.WithContext(ctx).GetGroupExtraPrompt(ctx, tc.TargetID)
		if err != nil {
			zap.L().Warn("读取群额外提示词失败", zap.Int64("target_id", tc.TargetID), zap.Error(err))
		} else {
			groupExtra = extra
		}
	}
	messages := make([]*schema.AgenticMessage, 0, len(input.Messages)+1)
	messages = append(messages, schema.SystemAgenticMessage(a.persona.GetGroupInstruction(groupExtra)))
	messages = append(messages, input.Messages...)
	return messages, nil
}

// privateGenModelInput 生成私聊模型输入：系统指令在前，会话消息在后
func (a *Agent) privateGenModelInput(ctx context.Context, _ string, input *adk.TypedAgentInput[*schema.AgenticMessage]) ([]*schema.AgenticMessage, error) {
	messages := make([]*schema.AgenticMessage, 0, len(input.Messages)+1)
	messages = append(messages, schema.SystemAgenticMessage(a.persona.GetPrivateInstruction()))
	messages = append(messages, input.Messages...)
	return messages, nil
}
