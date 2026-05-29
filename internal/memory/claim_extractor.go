package memory

import (
	"context"
	"fmt"
	"mumu-bot/internal/llm"
	"strings"
	"time"

	"go.uber.org/zap"
)

type NormalizedClaim struct {
	SubjectClass  MemorySubjectClass
	SubjectName   string
	CanonicalType CanonicalMemoryType
	SlotKind      string
	ValueSummary  string
	LongTerm      bool
	Ignored       bool
}

type rawNormalizedClaim struct {
	SubjectClass  string `json:"subject_class" jsonschema:"enum=group,enum=self,enum=member,enum=unknown,description=长期记忆主体类别"`
	SubjectName   string `json:"subject_name,omitempty" jsonschema:"description=当主体是 member 且能定位到候选成员时，填写候选昵称"`
	CanonicalType string `json:"canonical_type" jsonschema:"enum=fact,enum=episode,enum=preference,enum=constraint,enum=goal,enum=ignore,description=长期记忆类型"`
	SlotKind      string `json:"slot_kind,omitempty" jsonschema:"description=keyed 类型建议填写闭集槽位类型，用于话题摘要沿用旧元数据，不参与记忆主表去重"`
	ValueSummary  string `json:"value_summary,omitempty" jsonschema:"description=一句短中文，概括当前值、规则或进展"`
	LongTerm      bool   `json:"long_term" jsonschema:"description=只有适合跨会话召回时才为 true"`
}

var slotKindsByType = map[CanonicalMemoryType]map[string]struct{}{
	CanonicalMemoryTypeFact: {
		"identity": {}, "relation": {}, "role": {}, "status": {}, "assignment": {}, "schedule": {}, "conclusion": {},
	},
	CanonicalMemoryTypePreference: {
		"like": {}, "dislike": {}, "habit": {}, "style": {},
	},
	CanonicalMemoryTypeConstraint: {
		"rule": {}, "taboo": {}, "boundary": {}, "avoid": {},
	},
	CanonicalMemoryTypeGoal: {
		"project": {}, "task": {}, "deadline": {}, "milestone": {},
	},
}

func normalizeCanonicalType(raw string) CanonicalMemoryType {
	switch CanonicalMemoryType(strings.TrimSpace(strings.ToLower(raw))) {
	case CanonicalMemoryTypeFact,
		CanonicalMemoryTypeEpisode,
		CanonicalMemoryTypePreference,
		CanonicalMemoryTypeConstraint,
		CanonicalMemoryTypeGoal:
		return CanonicalMemoryType(strings.TrimSpace(strings.ToLower(raw)))
	case CanonicalMemoryType("ignore"):
		return ""
	default:
		return ""
	}
}

func normalizeSlotKind(kind CanonicalMemoryType, raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	if allowedKinds, ok := slotKindsByType[kind]; ok {
		if _, ok := allowedKinds[raw]; ok {
			return raw
		}
	}
	return ""
}

func normalizeSubjectClass(hint string) MemorySubjectClass {
	switch MemorySubjectClass(strings.TrimSpace(strings.ToLower(hint))) {
	case MemorySubjectClassGroup:
		return MemorySubjectClassGroup
	case MemorySubjectClassSelf:
		return MemorySubjectClassSelf
	case MemorySubjectClassMember:
		return MemorySubjectClassMember
	case MemorySubjectClassUnknown:
		return MemorySubjectClassUnknown
	}
	return ""
}

func (m *Manager) extractNormalizedClaim(ctx context.Context, input MemoryIngestInput, content string) NormalizedClaim {
	if m.claimModel == nil {
		return NormalizedClaim{}
	}

	extractCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	subjectCandidates := "无"
	if len(input.SubjectCandidates) > 0 {
		parts := make([]string, 0, len(input.SubjectCandidates))
		for _, candidate := range input.SubjectCandidates {
			name := strings.TrimSpace(candidate.Nickname)
			if name == "" {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s(%d)", name, candidate.UserID))
		}
		if len(parts) > 0 {
			subjectCandidates = strings.Join(parts, "、")
		}
	}

	raw, err := llm.GenerateStructuredJSONObject[rawNormalizedClaim](extractCtx, m.claimModel, buildMemoryClaimPrompt(input, subjectCandidates, content))
	if err != nil {
		zap.L().Warn("结构化提取长期记忆失败", zap.Error(err))
		return NormalizedClaim{}
	}

	claim := buildNormalizedClaim(input, raw)
	if claim.CanonicalType == "" && !claim.Ignored {
		zap.L().Warn("结构化提取长期记忆返回无效 claim",
			zap.String("subject_class", raw.SubjectClass),
			zap.String("subject_name", raw.SubjectName),
			zap.String("canonical_type", raw.CanonicalType),
			zap.String("slot_kind", raw.SlotKind))
	}
	return claim
}

func buildMemoryClaimPrompt(input MemoryIngestInput, subjectCandidates string, content string) string {
	return fmt.Sprintf(`请把下面这句候选长期记忆提取成一个结构化 claim。

规则：
- subject_class 只能是 group | self | member | unknown
- 如果 subject_class=member，且提供了 subject_candidates，subject_name 只能从候选成员里挑一个昵称；无法确定就把 subject_class 设为 unknown
- 如果 related_user_id > 0 且不等于 self_id，它表示外部已经明确指定了某个 member 主体；这种情况下 subject_class 可以是 member，subject_name 允许留空
- 如果 related_user_id > 0 且等于 self_id，它表示外部已经明确指定了 self 主体；这种情况下 subject_class 应该是 self
- canonical_type 只能是 fact | episode | preference | constraint | goal | ignore
- 当 allowed_canonical_types 非空时，canonical_type 只能从 allowed_canonical_types 中选择；如果都不合适，就输出 ignore
- keyed 类型尽量填写合法 slot_kind；slot_kind 只帮助后续摘要沿用旧条目，不参与长期记忆去重：
  - fact: identity | relation | role | status | assignment | schedule | conclusion
  - preference: like | dislike | habit | style
  - constraint: rule | taboo | boundary | avoid
  - goal: project | task | deadline | milestone
- episode 不需要 slot_kind
- value_summary 用一句短中文概括当前值、规则或进展
- 只有适合跨会话长期召回时才把 long_term 设为 true
- 如果这条信息不适合长期记忆，canonical_type 设为 ignore

输入：
- source_kind: %s
- related_user_id: %d
- self_id: %d
- subject_candidates: %s
- allowed_canonical_types: %s
- content: %s`,
		input.SourceKind,
		input.RelatedUserID,
		input.SelfID,
		subjectCandidates,
		allowedCanonicalTypesPrompt(input.AllowedCanonicalTypes),
		content,
	)
}

func buildNormalizedClaim(input MemoryIngestInput, raw rawNormalizedClaim) NormalizedClaim {
	if strings.EqualFold(strings.TrimSpace(raw.CanonicalType), "ignore") {
		return NormalizedClaim{Ignored: true}
	}
	claim := NormalizedClaim{
		SubjectName:   strings.TrimSpace(raw.SubjectName),
		CanonicalType: normalizeCanonicalType(raw.CanonicalType),
		LongTerm:      raw.LongTerm,
		ValueSummary:  strings.TrimSpace(raw.ValueSummary),
	}
	if claim.CanonicalType == "" {
		return NormalizedClaim{}
	}
	claim.SubjectClass = normalizeSubjectClass(raw.SubjectClass)
	if claim.SubjectClass == "" {
		return NormalizedClaim{}
	}

	switch claim.SubjectClass {
	case MemorySubjectClassGroup:
		if input.GroupID <= 0 {
			return NormalizedClaim{}
		}
	case MemorySubjectClassSelf:
		if input.SelfID <= 0 {
			return NormalizedClaim{}
		}
		if input.RelatedUserID > 0 && input.RelatedUserID != input.SelfID {
			return NormalizedClaim{}
		}
	case MemorySubjectClassMember:
		if input.RelatedUserID > 0 {
			if input.SelfID > 0 && input.RelatedUserID == input.SelfID {
				return NormalizedClaim{}
			}
		} else if resolveSubjectCandidateUserID(raw.SubjectName, input.SubjectCandidates) == 0 {
			return NormalizedClaim{}
		}
	case MemorySubjectClassUnknown:
	default:
		return NormalizedClaim{}
	}

	if IsKeyedCanonicalType(claim.CanonicalType) {
		claim.SlotKind = normalizeSlotKind(claim.CanonicalType, raw.SlotKind)
	}

	return claim
}

func resolveSubjectCandidateUserID(subjectName string, candidates []TopicParticipantRef) int64 {
	subjectName = strings.TrimSpace(subjectName)
	if subjectName == "" || len(candidates) == 0 {
		return 0
	}

	var matched int64
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Nickname) != subjectName {
			continue
		}
		if matched != 0 && matched != candidate.UserID {
			return 0
		}
		matched = candidate.UserID
	}
	return matched
}

func allowedCanonicalTypesPrompt(allowed []CanonicalMemoryType) string {
	if len(allowed) == 0 {
		return "fact | episode | preference | constraint | goal | ignore"
	}
	parts := make([]string, 0, len(allowed)+1)
	for _, item := range allowed {
		item = CanonicalMemoryType(strings.TrimSpace(string(item)))
		if item == "" {
			continue
		}
		parts = append(parts, string(item))
	}
	if len(parts) == 0 {
		return "fact | episode | preference | constraint | goal | ignore"
	}
	parts = append(parts, "ignore")
	return strings.Join(parts, " | ")
}
