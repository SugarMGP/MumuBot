package memory

import (
	"context"
	"fmt"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/utils"
	"strings"
	"time"

	"go.uber.org/zap"
)

type rawMemoryMergeDecision struct {
	ShouldMerge   bool   `json:"should_merge" jsonschema:"description=是否应把新记忆合并进已有记忆"`
	TargetID      uint   `json:"target_id,omitempty" jsonschema:"description=合并目标记忆 ID；should_merge=true 时必须来自候选列表"`
	MergeIDs      []uint `json:"merge_ids,omitempty" jsonschema:"description=本次要归档的重复候选 ID，必须来自候选列表；至少包含 target_id"`
	MergedContent string `json:"merged_content,omitempty" jsonschema:"description=合并后的完整新记忆内容；保留事实，不编造新增信息"`
}

func (m *Manager) decideMemoryMerge(ctx context.Context, input memoryMergeInput) (memoryMergeDecision, error) {
	if m.claimModel == nil || len(input.Candidates) == 0 {
		return memoryMergeDecision{}, nil
	}

	mergeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	candidateLines := make([]string, 0, len(input.Candidates))
	for _, candidate := range input.Candidates {
		candidateLines = append(candidateLines, fmt.Sprintf("- id=%d content=%s", candidate.ID, strings.TrimSpace(candidate.Content)))
	}
	prompt := fmt.Sprintf(`请判断新长期记忆是否与候选中的已有记忆语义重复。

规则：
- 只有确认表达同一件长期事实、偏好、约束或目标时才 should_merge=true。
- 不是同一件事、只是同主题、只是可能相关时，should_merge=false。
- 合并后内容必须是一句完整中文记忆，保留双方确定信息，不新增没有证据的内容。
- target_id 和 merge_ids 只能使用候选 id；merge_ids 至少包含 target_id。
- 如果候选中有多条重复项，可以把它们都放进 merge_ids；非重复项不要放入。

新记忆：
- kind=%s
- content=%s

候选记忆：
%s`,
		input.Incoming.Kind,
		strings.TrimSpace(input.Incoming.Content),
		strings.Join(candidateLines, "\n"),
	)

	raw, err := llm.GenerateStructuredJSONObject[rawMemoryMergeDecision](mergeCtx, m.claimModel, prompt)
	if err != nil {
		zap.L().Warn("长期记忆语义合并判断失败", zap.Error(err))
		return memoryMergeDecision{}, err
	}
	return normalizeMemoryMergeDecision(input.Candidates, raw), nil
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
		MergeIDs:      utils.UniqueIDs(mergeIDs),
		MergedContent: strings.TrimSpace(raw.MergedContent),
	}
}
