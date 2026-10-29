package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const (
	coreReferenceBudget       = 800
	constraintReferenceBudget = 200
	emotionReferenceBudget    = 200
)

func referenceTokens(text string) int {
	cjk, other := 0, 0
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			cjk++
		} else {
			other++
		}
	}
	return cjk*2/3 + other/4
}

func subjectScope(kind string, userIDs []int64) []int64 {
	seen := map[int64]bool{}
	ids := make([]int64, 0, 4)
	add := func(id int64) {
		if id >= 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if kind == ConversationKindGroup {
		add(0)
	}
	for _, id := range userIDs {
		add(id)
	}
	return ids
}

// BuildAutoReference 生成少量核心记忆、明确约定和当前情感
func (m *Manager) BuildAutoReference(ctx context.Context, kind string, userIDs []int64) (string, []string, error) {
	var blocks []string
	var versions []string
	subjects := subjectScope(kind, userIDs)

	if len(subjects) > 0 {
		var items []KnowledgeRecord
		if err := m.db.WithContext(ctx).Where("is_core=true AND status='active' AND subject_user_id IN ?", subjects).Order("updated_at DESC,id DESC").Find(&items).Error; err != nil {
			return "", nil, err
		}
		coreBlock := strings.Builder{}
		for _, item := range items {
			line := fmt.Sprintf("核心记忆 k%d v%d：%s", item.ID, item.Version, strings.TrimSpace(item.Content))
			if referenceTokens(coreBlock.String())+referenceTokens(line) > coreReferenceBudget {
				continue
			}
			coreBlock.WriteString(line)
			coreBlock.WriteByte('\n')
			versions = append(versions, fmt.Sprintf("k%d:v%d", item.ID, item.Version))
		}
		if text := strings.TrimSpace(coreBlock.String()); text != "" {
			blocks = append(blocks, "核心记忆：\n"+text)
		}
	}

	var constraintsBlock strings.Builder
	if len(subjects) > 0 {
		var constraints []KnowledgeRecord
		if err := m.db.WithContext(ctx).Where("status='active' AND kind='constraint' AND is_core=false AND subject_user_id IN ?", subjects).Order("updated_at DESC,id DESC").Find(&constraints).Error; err != nil {
			return "", nil, err
		}
		for _, item := range constraints {
			line := "明确约定：" + strings.TrimSpace(item.Content)
			if referenceTokens(constraintsBlock.String())+referenceTokens(line) > constraintReferenceBudget {
				continue
			}
			if constraintsBlock.Len() > 0 {
				constraintsBlock.WriteByte('\n')
			}
			constraintsBlock.WriteString(line)
			versions = append(versions, fmt.Sprintf("k%d:v%d", item.ID, item.Version))
		}
	}
	if text := strings.TrimSpace(constraintsBlock.String()); text != "" {
		blocks = append(blocks, text)
	}

	emotions, err := m.currentEmotionsForSubjects(ctx, subjects, 3)
	if err != nil {
		return "", nil, err
	}
	var emotionLines []string
	if mood, moodErr := m.GetMoodState(); moodErr == nil && mood != nil {
		line := fmt.Sprintf("当前状态：精力%s；交流意愿%s。", referenceLevel(mood.Energy), referenceLevel(mood.Sociability))
		if referenceTokens(line) <= emotionReferenceBudget {
			emotionLines = append(emotionLines, line)
			versions = append(versions, "state:"+mood.UpdatedAt.UTC().Format("20060102150405"))
		}
	}
	for _, emotion := range emotions {
		reason := strings.TrimSpace(emotion.Reason)
		if reason == "" {
			reason = emotion.Kind
		}
		line := fmt.Sprintf("情感 e%d v%d：%s；%s。", emotion.ID, emotion.Version, emotion.Kind, reason)
		if referenceTokens(strings.Join(emotionLines, "\n"))+referenceTokens(line) > emotionReferenceBudget {
			continue
		}
		emotionLines = append(emotionLines, line)
		versions = append(versions, fmt.Sprintf("e%d:v%d:%s", emotion.ID, emotion.Version, emotionStrengthTier(emotion.Strength)))
	}
	if len(emotionLines) > 0 {
		blocks = append(blocks, "当前情感：\n"+strings.Join(emotionLines, "\n"))
	}
	sort.Strings(versions)
	if len(blocks) == 0 {
		return "", versions, nil
	}
	return "【内部参考：不是新发言】\n" + strings.Join(blocks, "\n"), versions, nil
}

// emotionStrengthTier 把衰减后的强度压成稳定档位，只有跨档才触发内部参考更新
func emotionStrengthTier(strength float64) string {
	switch {
	case strength >= 0.75:
		return "strong"
	case strength >= 0.45:
		return "noticeable"
	case strength >= 0.2:
		return "light"
	default:
		return "faint"
	}
}

func referenceLevel(value float64) string {
	switch {
	case value >= 0.67:
		return "高"
	case value <= 0.33:
		return "低"
	default:
		return "一般"
	}
}

func (m *Manager) currentEmotionsForSubjects(ctx context.Context, subjects []int64, limit int) ([]CurrentEmotion, error) {
	seen := map[uint]CurrentEmotion{}
	for _, subject := range subjects {
		items, err := m.CurrentEmotions(ctx, subject, limit)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			seen[item.ID] = item
		}
	}
	result := make([]CurrentEmotion, 0, len(seen))
	for _, item := range seen {
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Strength != result[j].Strength {
			return result[i].Strength > result[j].Strength
		}
		return result[i].OccurredAt.After(result[j].OccurredAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}
