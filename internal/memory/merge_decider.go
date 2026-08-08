package memory

import (
	"context"
	"fmt"
	"mumu-bot/internal/config"
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
		candidateLines = append(candidateLines, fmt.Sprintf("- id=%d subject_user_id=%d kind=%s content=%s",
			candidate.ID, candidate.SubjectUserID, candidate.Kind, strings.TrimSpace(candidate.Content)))
	}
	prompt := fmt.Sprintf(`请判断新长期记忆是否与候选中的已有记忆语义重复。

规则：
- 只有确认表达同一件长期事实、偏好、约束或目标时才 should_merge=true。
- 主体和核心命题相同的重复表述、再次强调、否认、解释或细节补充属于同一件事，应合并而不是并列保存；不要因为句式、措辞或叙述角度不同就判为新记忆。
- 候选 kind 可能与新记忆不同；只有确认这是同一底层命题的分类误差时才允许跨类型合并。
- 不同成员、不同实体、不同事件、不同时间状态或仅仅主题相近时，必须 should_merge=false；不能因为向量相似就合并。
- subject_user_id 已由服务端硬过滤；禁止把不同主体合并。
- 不是同一件事、只是同主题、只是可能相关时，should_merge=false。
- 合并后内容必须是一句完整、稳定、可复用的中文记忆，去掉“再次表示、强调、解释”等重复话语动作，保留双方确定信息，不新增没有证据的内容。
- target_id 和 merge_ids 只能使用候选 id；merge_ids 至少包含 target_id。
- 如果候选中有多条重复项，可以把它们都放进 merge_ids；非重复项不要放入。

新记忆：
- subject_user_id=%d
- kind=%s
- content=%s

候选记忆：
%s`,
		input.Incoming.SubjectUserID,
		input.Incoming.Kind,
		strings.TrimSpace(input.Incoming.Content),
		strings.Join(candidateLines, "\n"),
	)

	raw, err := llm.GenerateStructuredJSONObject[rawMemoryMergeDecision](llm.WithTask(mergeCtx, "memory_merge", config.Get().ModelTiers.Low.Model), m.claimModel, prompt)
	if err != nil {
		zap.L().Warn("长期记忆语义合并判断失败", zap.Error(err))
		return memoryMergeDecision{}, err
	}
	return normalizeMemoryMergeDecision(input.Incoming, input.Candidates, raw), nil
}

func normalizeMemoryMergeDecision(incoming Memory, candidates []Memory, raw rawMemoryMergeDecision) memoryMergeDecision {
	if !raw.ShouldMerge {
		return memoryMergeDecision{}
	}
	candidateIDs := make(map[uint]Memory, len(candidates))
	for _, candidate := range candidates {
		candidateIDs[candidate.ID] = candidate
	}
	target, ok := candidateIDs[raw.TargetID]
	if !ok || !memorySubjectsCompatible(incoming, target) {
		return memoryMergeDecision{}
	}
	mergeIDs := make([]uint, 0, len(raw.MergeIDs)+1)
	mergeIDs = append(mergeIDs, raw.TargetID)
	for _, id := range raw.MergeIDs {
		if candidate, ok := candidateIDs[id]; ok && memorySubjectsCompatible(incoming, candidate) {
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

func memorySubjectsCompatible(incoming, candidate Memory) bool {
	return incoming.SubjectUserID == candidate.SubjectUserID
}
