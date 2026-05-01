package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	flowagent "github.com/cloudwego/eino/flow/agent"
	agentreact "github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

const memoryMergeToolName = "submitMemoryMergeDecision"

type memoryMergeCaptureKey struct{}

type rawMemoryMergeDecision struct {
	ShouldMerge   bool   `json:"should_merge" jsonschema:"description=是否应把新记忆合并进已有记忆"`
	TargetID      uint   `json:"target_id,omitempty" jsonschema:"description=合并目标记忆 ID；should_merge=true 时必须来自候选列表"`
	MergeIDs      []uint `json:"merge_ids,omitempty" jsonschema:"description=本次要归档的重复候选 ID，必须来自候选列表；至少包含 target_id"`
	MergedContent string `json:"merged_content,omitempty" jsonschema:"description=合并后的完整新记忆内容；保留事实，不编造新增信息"`
}

type memoryMergeToolOutput struct {
	Success bool `json:"success"`
}

func withMemoryMergeTarget(ctx context.Context, target *rawMemoryMergeDecision) context.Context {
	return context.WithValue(ctx, memoryMergeCaptureKey{}, target)
}

func getMemoryMergeTarget(ctx context.Context) *rawMemoryMergeDecision {
	target, _ := ctx.Value(memoryMergeCaptureKey{}).(*rawMemoryMergeDecision)
	return target
}

func newMemoryMergeTool() (tool.InvokableTool, error) {
	return toolutils.InferTool(
		memoryMergeToolName,
		"提交长期记忆语义合并判断。必须调用一次，不要输出普通文本。",
		func(ctx context.Context, input *rawMemoryMergeDecision) (*memoryMergeToolOutput, error) {
			target := getMemoryMergeTarget(ctx)
			if target == nil {
				return nil, fmt.Errorf("记忆合并接收器未初始化")
			}
			*target = rawMemoryMergeDecision{
				ShouldMerge:   input.ShouldMerge,
				TargetID:      input.TargetID,
				MergeIDs:      append([]uint(nil), input.MergeIDs...),
				MergedContent: strings.TrimSpace(input.MergedContent),
			}
			if err := agentreact.SetReturnDirectly(ctx); err != nil {
				return nil, err
			}
			return &memoryMergeToolOutput{Success: true}, nil
		},
	)
}

func newMemoryMergeDecider(mergeModel model.ToolCallingChatModel) (*agentreact.Agent, error) {
	if mergeModel == nil {
		return nil, fmt.Errorf("mergeModel 不能为空")
	}
	mergeTool, err := newMemoryMergeTool()
	if err != nil {
		return nil, err
	}
	return agentreact.NewAgent(context.Background(), &agentreact.AgentConfig{
		ToolCallingModel: mergeModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:               []tool.BaseTool{mergeTool},
			ExecuteSequentially: true,
		},
		MaxStep:            4,
		ToolReturnDirectly: map[string]struct{}{memoryMergeToolName: {}},
	})
}

func (m *Manager) decideMemoryMerge(ctx context.Context, input memoryMergeInput) (memoryMergeDecision, error) {
	if m.mergeDecider == nil || len(input.Candidates) == 0 {
		return memoryMergeDecision{}, nil
	}
	mergeCtx, cancel := context.WithTimeout(withMemoryMergeTarget(ctx, &rawMemoryMergeDecision{}), 20*time.Second)
	defer cancel()

	target := getMemoryMergeTarget(mergeCtx)
	if target == nil {
		return memoryMergeDecision{}, nil
	}

	candidateLines := make([]string, 0, len(input.Candidates))
	for _, candidate := range input.Candidates {
		candidateLines = append(candidateLines, fmt.Sprintf(
			"- id=%d evidence_count=%d content=%s",
			candidate.ID,
			max(candidate.EvidenceCount, 1),
			strings.TrimSpace(candidate.Content),
		))
	}
	prompt := fmt.Sprintf(`请判断新长期记忆是否与候选中的已有记忆语义重复，并且必须调用一次 %s 工具，不要输出普通文本。

规则：
- 只有确认表达同一件长期事实、偏好、约束或目标时才 should_merge=true。
- 不是同一件事、只是同主题、只是可能相关时，should_merge=false。
- 合并后内容必须是一句完整中文记忆，保留双方确定信息，不新增没有证据的内容。
- target_id 和 merge_ids 只能使用候选 id；merge_ids 至少包含 target_id。
- 如果候选中有多条重复项，可以把它们都放进 merge_ids；非重复项不要放入。

新记忆：
- canonical_type=%s
- content=%s

候选记忆：
%s`,
		memoryMergeToolName,
		input.Incoming.CanonicalType,
		strings.TrimSpace(input.Incoming.Content),
		strings.Join(candidateLines, "\n"),
	)
	_, err := m.mergeDecider.Generate(mergeCtx, []*schema.Message{
		schema.SystemMessage("你负责判断长期记忆是否语义重复，并产出合并后的单条记忆。你必须调用工具提交结果，不要输出普通文本。"),
		schema.UserMessage(prompt),
	}, flowagent.WithComposeOptions(
		compose.WithChatModelOption(model.WithToolChoice(schema.ToolChoiceForced, memoryMergeToolName)),
	))
	if err != nil {
		zap.L().Warn("长期记忆语义合并判断失败", zap.Error(err))
		return memoryMergeDecision{}, err
	}
	return normalizeMemoryMergeDecision(input.Candidates, *target), nil
}

func normalizeMemoryMergeDecision(candidates []Memory, raw rawMemoryMergeDecision) memoryMergeDecision {
	if !raw.ShouldMerge {
		return memoryMergeDecision{}
	}
	candidateIDs := make(map[uint]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidateIDs[candidate.ID] = struct{}{}
	}
	if _, ok := candidateIDs[raw.TargetID]; !ok {
		return memoryMergeDecision{}
	}
	mergeIDs := make([]uint, 0, len(raw.MergeIDs)+1)
	mergeIDs = append(mergeIDs, raw.TargetID)
	for _, id := range raw.MergeIDs {
		if _, ok := candidateIDs[id]; ok {
			mergeIDs = append(mergeIDs, id)
		}
	}
	return memoryMergeDecision{
		ShouldMerge:   true,
		TargetID:      raw.TargetID,
		MergeIDs:      uniqueMemoryIDs(mergeIDs),
		MergedContent: strings.TrimSpace(raw.MergedContent),
	}
}
