package memory

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	pinnedReferenceBudget  = 1000
	emotionReferenceBudget = 200
)

type ReferenceItem struct {
	Key     string `json:"key"`
	Version string `json:"version"`
	Text    string `json:"text"`
}

type ReferenceSnapshot struct {
	Items    []ReferenceItem   `json:"items"`
	Text     string            `json:"text"`
	Versions map[string]string `json:"versions"`
}

type ReferenceReceipt struct {
	Auto       map[string]string `json:"auto,omitempty"`
	Tool       map[string]string `json:"tool,omitempty"`
	AutoDelete []string          `json:"auto_delete,omitempty"`
}

type ReferenceDelta struct {
	Text    string
	Receipt ReferenceReceipt
}

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

func subjectPairs(kind string, targetID int64, userIDs []int64) [][2]string {
	seen := map[string]bool{}
	result := make([][2]string, 0, len(userIDs)+2)
	add := func(subjectType, target string) {
		if target == "" {
			return
		}
		key := subjectType + ":" + target
		if !seen[key] {
			seen[key] = true
			result = append(result, [2]string{subjectType, target})
		}
	}
	if kind == ConversationKindGroup && targetID > 0 {
		add(SubjectGroup, strconv.FormatInt(targetID, 10))
	}
	if kind == ConversationKindPrivate && targetID > 0 {
		add(SubjectUser, strconv.FormatInt(targetID, 10))
	}
	add(SubjectSelf, SubjectSelf)
	for _, id := range userIDs {
		if id > 0 {
			add(SubjectUser, strconv.FormatInt(id, 10))
		}
	}
	return result
}

func pendingContains(pendingTexts []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		return false
	}
	for _, text := range pendingTexts {
		if strings.Contains(strings.ToLower(text), target) {
			return true
		}
	}
	return false
}

func referenceRecordAllowed(record KnowledgeRecord, allowed map[string]bool, pendingTexts []string) bool {
	if record.SubjectType == SubjectObject {
		return record.MemoryType == MemoryProfile && pendingContains(pendingTexts, record.SubjectTarget)
	}
	return allowed[record.SubjectType+":"+record.SubjectTarget]
}

// BuildAutoReference 生成当前会话可见的参考快照。
func (m *Manager) BuildAutoReference(ctx context.Context, kind string, targetID int64, userIDs []int64, pendingTexts []string) (ReferenceSnapshot, error) {
	subjects := subjectPairs(kind, targetID, userIDs)
	var records []KnowledgeRecord
	if err := m.db.WithContext(ctx).Where("pinned=true AND status='active'").Order("updated_at DESC,id DESC").Find(&records).Error; err != nil {
		return ReferenceSnapshot{}, err
	}
	allowed := map[string]bool{}
	for _, pair := range subjects {
		allowed[pair[0]+":"+pair[1]] = true
	}
	items := make([]ReferenceItem, 0)
	lines := make([]string, 0)
	used := 0
	for _, record := range records {
		if !referenceRecordAllowed(record, allowed, pendingTexts) {
			continue
		}
		line := fmt.Sprintf("记忆 %d %s/%s：%s", record.ID, record.SubjectType, record.SubjectTarget, strings.TrimSpace(record.Content))
		if referenceTokens(line) > pinnedReferenceBudget-used {
			continue
		}
		used += referenceTokens(line)
		lines = append(lines, line)
		items = append(items, ReferenceItem{Key: fmt.Sprintf("memory:%d", record.ID), Version: fmt.Sprintf("v%d", record.Version), Text: line})
	}

	emotionSubjects := make([]int64, 0, len(userIDs)+1)
	if kind == ConversationKindGroup {
		emotionSubjects = append(emotionSubjects, 0)
	}
	if kind == ConversationKindPrivate && targetID > 0 {
		emotionSubjects = append(emotionSubjects, targetID)
	}
	emotionSubjects = append(emotionSubjects, userIDs...)
	emotions, err := m.currentEmotionsForSubjects(ctx, emotionSubjects, 3)
	if err != nil {
		return ReferenceSnapshot{}, err
	}
	emotionLines := make([]string, 0, len(emotions)+1)
	if mood, moodErr := m.GetMoodState(); moodErr == nil && mood != nil {
		line := fmt.Sprintf("当前状态：精力%s；交流意愿%s。", AffectLevelText(mood.Energy), AffectLevelText(mood.Sociability))
		if referenceTokens(line) <= emotionReferenceBudget {
			emotionLines = append(emotionLines, line)
			items = append(items, ReferenceItem{Key: "state:affect", Version: mood.UpdatedAt.UTC().Format("20060102150405"), Text: line})
		}
	}
	for _, emotion := range emotions {
		reason := strings.TrimSpace(emotion.Reason)
		if reason == "" {
			reason = emotion.Kind
		}
		line := fmt.Sprintf("情感 e%d：%s；%s。", emotion.ID, emotion.Kind, reason)
		if referenceTokens(strings.Join(emotionLines, "\n"))+referenceTokens(line) > emotionReferenceBudget {
			continue
		}
		emotionLines = append(emotionLines, line)
		items = append(items, ReferenceItem{Key: fmt.Sprintf("emotion:%d", emotion.ID), Version: fmt.Sprintf("v%d:%s", emotion.Version, EmotionStrengthTier(emotion.Strength)), Text: line})
	}
	if len(emotionLines) > 0 {
		lines = append(lines, "当前情感：")
		lines = append(lines, emotionLines...)
	}
	versions := map[string]string{}
	for _, item := range items {
		versions[item.Key] = item.Version
	}
	text := ""
	if len(lines) > 0 {
		text = "【内部参考：不是新发言】\n" + strings.Join(lines, "\n")
	}
	return ReferenceSnapshot{Items: items, Text: text, Versions: versions}, nil
}

// BuildReferenceDelta 只输出新增、修改和确实失效的自动参考项。
func (m *Manager) BuildReferenceDelta(ctx context.Context, snapshot ReferenceSnapshot, seen ReferenceSeen) (ReferenceDelta, error) {
	return buildReferenceDiff(snapshot, seen, func(key string) (bool, error) { return m.ReferenceItemActive(ctx, key) })
}

func buildReferenceDiff(snapshot ReferenceSnapshot, seen ReferenceSeen, active func(string) (bool, error)) (ReferenceDelta, error) {
	if seen.Auto == nil {
		seen.Auto = map[string]string{}
	}
	if seen.Tool == nil {
		seen.Tool = map[string]string{}
	}
	current := map[string]ReferenceItem{}
	for _, item := range snapshot.Items {
		current[item.Key] = item
	}
	receipt := ReferenceReceipt{Auto: map[string]string{}}
	lines := make([]string, 0)
	for _, item := range snapshot.Items {
		autoVersion := seen.Auto[item.Key]
		toolVersion := seen.Tool[item.Key]
		if autoVersion == item.Version || toolVersion == item.Version {
			continue
		}
		label := item.Key + " " + item.Version + "：" + item.Text
		if autoVersion == "" && toolVersion == "" {
			lines = append(lines, "+ "+label)
		} else {
			lines = append(lines, "~ "+label)
		}
		receipt.Auto[item.Key] = item.Version
	}
	for key, version := range seen.Auto {
		if _, ok := current[key]; ok {
			continue
		}
		isActive, err := active(key)
		if err != nil {
			return ReferenceDelta{}, err
		}
		if isActive {
			continue
		}
		lines = append(lines, "- "+key+" 已失效（原版本 "+version+"）")
		receipt.AutoDelete = append(receipt.AutoDelete, key)
	}
	if len(lines) == 0 {
		return ReferenceDelta{}, nil
	}
	if len(receipt.Auto) == len(snapshot.Items) && len(seen.Auto) == 0 && len(seen.Tool) == 0 && len(receipt.AutoDelete) == 0 {
		return ReferenceDelta{Text: snapshot.Text, Receipt: receipt}, nil
	}
	return ReferenceDelta{Text: "【内部参考更新】\n" + strings.Join(lines, "\n"), Receipt: receipt}, nil
}

// ReferenceItemActive 区分“本次没选中”和“确实归档/取消 pin/情感结束”。
func (m *Manager) ReferenceItemActive(ctx context.Context, key string) (bool, error) {
	switch {
	case strings.HasPrefix(key, "memory:"):
		id, err := strconv.ParseUint(strings.TrimPrefix(key, "memory:"), 10, 64)
		if err != nil || id == 0 {
			return false, nil
		}
		var item KnowledgeRecord
		if err := m.db.WithContext(ctx).Where("id=?", id).Take(&item).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return false, nil
			}
			return false, err
		}
		return item.Status == "active" && item.Pinned, nil
	case strings.HasPrefix(key, "emotion:"):
		id, err := strconv.ParseUint(strings.TrimPrefix(key, "emotion:"), 10, 64)
		if err != nil || id == 0 {
			return false, nil
		}
		var state EmotionState
		if err := m.db.WithContext(ctx).Where("id=?", id).Take(&state).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return false, nil
			}
			return false, err
		}
		return state.EndedAt == nil && currentStrength(state, time.Now()) > 0, nil
	case key == "state:affect":
		return true, nil
	default:
		return false, nil
	}
}

// EmotionStrengthTier 把情感强度映射成稳定的参考版本档位
func EmotionStrengthTier(strength float64) string {
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

// AffectLevelText 把精力或交流意愿数值转成可读档位
func AffectLevelText(value float64) string {
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
	if len(subjects) == 0 {
		return nil, nil
	}
	unique := make([]int64, 0, len(subjects))
	seenSubjects := map[int64]bool{}
	for _, subject := range subjects {
		if !seenSubjects[subject] {
			seenSubjects[subject] = true
			unique = append(unique, subject)
		}
	}
	var rows []EmotionState
	if err := m.db.WithContext(ctx).Where("ended_at IS NULL AND about_user_id IN ?", unique).Find(&rows).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	bySubject := make(map[int64][]CurrentEmotion, len(unique))
	for _, row := range rows {
		strength := currentStrength(row, now)
		if strength < 0.10 {
			continue
		}
		bySubject[row.AboutUserID] = append(bySubject[row.AboutUserID], CurrentEmotion{
			ID: row.ID, Kind: row.Kind, AboutUserID: row.AboutUserID, Strength: strength,
			Reason: row.Reason, OccurredAt: row.OccurredAt, AnchorAt: row.AnchorAt,
			DecayClass: row.DecayClass, Version: row.Version,
		})
	}
	seen := map[uint]CurrentEmotion{}
	for _, items := range bySubject {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Strength != items[j].Strength {
				return items[i].Strength > items[j].Strength
			}
			return items[i].OccurredAt.After(items[j].OccurredAt)
		})
		if len(items) > 3 {
			items = items[:3]
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
	if limit <= 0 || len(result) <= limit {
		return result, nil
	}
	return result[:limit], nil
}
