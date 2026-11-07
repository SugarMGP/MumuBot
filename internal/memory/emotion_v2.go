package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	emotionKindMaxRunes = 100
	emotionTextMaxRunes = 2000
	emotionHalfLife     = 24 * time.Hour
	emotionSettleFloor  = 0.001
)

// EmotionChange 是一次按条目维护的感受变更；Kind/Fact/Reason 为 nil 表示沿用原值
type EmotionChange struct {
	ID        uint
	Version   uint64
	Kind      *string
	Fact      *string
	Reason    *string
	Intensity string
}

// AffectUpdate 是 life 同批提交的精力与交流意愿变化
type AffectUpdate struct {
	Energy      *string
	Sociability *string
	Reason      string
}

// CurrentEmotion 是读时计算后的当前感受；Strength 不是模型刚填的档位
type CurrentEmotion struct {
	ID           uint       `json:"id"`
	Kind         string     `json:"kind"`
	Fact         string     `json:"fact"`
	Reason       string     `json:"reason"`
	Strength     float64    `json:"strength"`
	StrengthBand string     `json:"strength_band"`
	OccurredAt   time.Time  `json:"occurred_at"`
	AnchorAt     time.Time  `json:"anchor_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	Version      uint64     `json:"version"`
}

// emotionSnapshot 是 emotion_revisions.snapshot 的完整状态
type emotionSnapshot struct {
	Kind       string     `json:"kind"`
	Fact       string     `json:"fact"`
	Reason     string     `json:"reason"`
	Strength   float64    `json:"strength"`
	OccurredAt time.Time  `json:"occurred_at"`
	AnchorAt   time.Time  `json:"anchor_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	Version    uint64     `json:"version"`
}

// parseIntensity 解析模型提交的三档强度或平复指令
func parseIntensity(value string) (strength float64, settled, ok bool) {
	switch strings.TrimSpace(value) {
	case "轻微":
		return 0.25, false, true
	case "明显":
		return 0.60, false, true
	case "强烈":
		return 1.0, false, true
	case "已平复":
		return 0, true, true
	default:
		return 0, false, false
	}
}

// currentStrength 按统一 24 小时半衰期做惰性淡化，读多少次都不会加速
func currentStrength(state EmotionState, now time.Time) float64 {
	if state.EndedAt != nil {
		return 0
	}
	hours := now.Sub(state.AnchorAt).Hours()
	if hours < 0 {
		hours = 0
	}
	return state.Strength * math.Pow(2, -hours/emotionHalfLife.Hours())
}

// EmotionStrengthBand 返回可读强度档位，供前后端共用同一阈值
func EmotionStrengthBand(strength float64) string {
	switch {
	case strength >= 0.8:
		return "strong"
	case strength >= 0.4:
		return "clear"
	case strength >= 0.1:
		return "light"
	default:
		return "faint"
	}
}

func toCurrentEmotion(state EmotionState, now time.Time) CurrentEmotion {
	strength := currentStrength(state, now)
	return CurrentEmotion{
		ID: state.ID, Kind: state.Kind, Fact: state.Fact, Reason: state.Reason,
		Strength: strength, StrengthBand: EmotionStrengthBand(strength),
		OccurredAt: state.OccurredAt, AnchorAt: state.AnchorAt, UpdatedAt: state.UpdatedAt,
		EndedAt: state.EndedAt, Version: state.Version,
	}
}

// normalizeEmotionText 校验非空和输入体积上限，不静默截断
func normalizeEmotionText(value *string, label string, maxRunes int) (*string, error) {
	if value == nil {
		return nil, fmt.Errorf("情感%s不能为空", label)
	}
	text := strings.TrimSpace(*value)
	if text == "" {
		return nil, fmt.Errorf("情感%s不能为空", label)
	}
	if utf8.RuneCountInString(text) > maxRunes {
		return nil, fmt.Errorf("情感%s不能超过 %d 字", label, maxRunes)
	}
	return &text, nil
}

// SettleEmotions 结算自然低于阈值的感受，可按维护循环或读取前调用
func (m *Manager) SettleEmotions(ctx context.Context, now time.Time) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, err := settleEmotionStatesTx(tx, now)
		return err
	})
}

func settleEmotionStatesTx(tx *gorm.DB, now time.Time) (int, error) {
	total := 0
	var lastID uint
	for {
		var rows []EmotionState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("ended_at IS NULL AND id>?", lastID).Order("id").Limit(500).Find(&rows).Error; err != nil {
			return total, err
		}
		if len(rows) == 0 {
			return total, nil
		}
		lastID = rows[len(rows)-1].ID
		for _, state := range rows {
			if currentStrength(state, now) >= emotionSettleFloor {
				continue
			}
			crossAt := now
			if state.Strength > 0 {
				crossAt = state.AnchorAt.Add(time.Duration(float64(emotionHalfLife) * math.Log2(state.Strength/emotionSettleFloor)))
				if crossAt.After(now) {
					crossAt = now
				}
			}
			state.EndedAt = &crossAt
			state.Version++
			state.UpdatedAt = now
			if err := tx.Save(&state).Error; err != nil {
				return total, err
			}
			if err := appendEmotionRevision(tx, state); err != nil {
				return total, err
			}
			total++
		}
		if len(rows) < 500 {
			return total, nil
		}
	}
}

// UpdateEmotions 在一个事务里按条目新建、修改或平复感受
func (m *Manager) UpdateEmotions(ctx context.Context, changes []EmotionChange) ([]CurrentEmotion, error) {
	var output []CurrentEmotion
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if _, err := settleEmotionStatesTx(tx, now); err != nil {
			return err
		}
		var err error
		output, err = applyEmotionChanges(tx, changes, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return output, nil
}

// UpdateEmotionsAndAffect 让 life 的情感与精力/交流意愿共享同一个事务
func (m *Manager) UpdateEmotionsAndAffect(ctx context.Context, changes []EmotionChange, affect AffectUpdate) ([]CurrentEmotion, *MoodState, error) {
	var output []CurrentEmotion
	var mood *MoodState
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if _, err := settleEmotionStatesTx(tx, now); err != nil {
			return err
		}
		var err error
		output, err = applyEmotionChanges(tx, changes, now)
		if err != nil {
			return err
		}
		mood, err = applyAffectUpdateTx(tx, affect)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return output, mood, nil
}

func applyEmotionChanges(tx *gorm.DB, changes []EmotionChange, now time.Time) ([]CurrentEmotion, error) {
	output := make([]CurrentEmotion, 0, len(changes))
	seen := make(map[uint]bool, len(changes))
	for _, change := range changes {
		if change.ID != 0 {
			if seen[change.ID] {
				return nil, fmt.Errorf("同一条情感不能在本次更新中重复出现")
			}
			seen[change.ID] = true
		}
		item, err := applyEmotionChange(tx, change, now)
		if err != nil {
			return nil, err
		}
		output = append(output, item)
	}
	return output, nil
}

func applyEmotionChange(tx *gorm.DB, change EmotionChange, now time.Time) (CurrentEmotion, error) {
	intensity := strings.TrimSpace(change.Intensity)
	strength, settled, ok := parseIntensity(intensity)
	if intensity != "" && !ok {
		return CurrentEmotion{}, fmt.Errorf("未知情感强度 %s", intensity)
	}
	if change.ID == 0 {
		if change.Version != 0 {
			return CurrentEmotion{}, fmt.Errorf("新建情感不能带 version")
		}
		if intensity == "" {
			return CurrentEmotion{}, fmt.Errorf("新建情感需要 intensity")
		}
		if settled {
			return CurrentEmotion{}, fmt.Errorf("新建情感不能直接平复")
		}
		kind, err := normalizeEmotionText(change.Kind, "名称", emotionKindMaxRunes)
		if err != nil {
			return CurrentEmotion{}, err
		}
		fact, err := normalizeEmotionText(change.Fact, "事实", emotionTextMaxRunes)
		if err != nil {
			return CurrentEmotion{}, err
		}
		reason, err := normalizeEmotionText(change.Reason, "意义", emotionTextMaxRunes)
		if err != nil {
			return CurrentEmotion{}, err
		}
		state := EmotionState{
			Kind: *kind, Fact: *fact, Reason: *reason, Strength: strength,
			OccurredAt: now, AnchorAt: now, UpdatedAt: now, Version: 1,
		}
		if err := tx.Create(&state).Error; err != nil {
			return CurrentEmotion{}, err
		}
		if err := appendEmotionRevision(tx, state); err != nil {
			return CurrentEmotion{}, err
		}
		return toCurrentEmotion(state, now), nil
	}
	if change.Version == 0 {
		return CurrentEmotion{}, fmt.Errorf("修改情感需要 version")
	}
	var state EmotionState
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", change.ID).Take(&state).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return CurrentEmotion{}, fmt.Errorf("情绪条目不存在")
		}
		return CurrentEmotion{}, err
	}
	if state.EndedAt != nil {
		return CurrentEmotion{}, fmt.Errorf("这条情感已经平复，请新建一条")
	}
	if state.Version != change.Version {
		return CurrentEmotion{}, fmt.Errorf("情绪版本已变化，请重新读取后再更新")
	}
	changed := false
	if change.Kind != nil {
		kind, err := normalizeEmotionText(change.Kind, "名称", emotionKindMaxRunes)
		if err != nil {
			return CurrentEmotion{}, err
		}
		state.Kind = *kind
		changed = true
	}
	if change.Fact != nil {
		fact, err := normalizeEmotionText(change.Fact, "事实", emotionTextMaxRunes)
		if err != nil {
			return CurrentEmotion{}, err
		}
		state.Fact = *fact
		changed = true
	}
	if change.Reason != nil {
		reason, err := normalizeEmotionText(change.Reason, "意义", emotionTextMaxRunes)
		if err != nil {
			return CurrentEmotion{}, err
		}
		state.Reason = *reason
		changed = true
	}
	if settled {
		if change.Reason == nil {
			return CurrentEmotion{}, fmt.Errorf("平复情感需要说明原因")
		}
		state.EndedAt = &now
		changed = true
	} else if intensity != "" {
		state.Strength = strength
		state.AnchorAt = now
		changed = true
	}
	if !changed {
		return CurrentEmotion{}, fmt.Errorf("没有需要更新的内容")
	}
	state.Version++
	state.UpdatedAt = now
	if err := tx.Save(&state).Error; err != nil {
		return CurrentEmotion{}, err
	}
	if err := appendEmotionRevision(tx, state); err != nil {
		return CurrentEmotion{}, err
	}
	return toCurrentEmotion(state, now), nil
}

func appendEmotionRevision(tx *gorm.DB, state EmotionState) error {
	snapshot, err := sonic.Marshal(emotionSnapshot{
		Kind: state.Kind, Fact: state.Fact, Reason: state.Reason, Strength: state.Strength,
		OccurredAt: state.OccurredAt, AnchorAt: state.AnchorAt, UpdatedAt: state.UpdatedAt,
		EndedAt: state.EndedAt, Version: state.Version,
	})
	if err != nil {
		return err
	}
	return tx.Create(&EmotionRevision{
		EmotionID: state.ID, Version: state.Version, Snapshot: snapshot,
	}).Error
}

// CurrentEmotions 返回全部未平复感受，按当前强度、修订时间和 ID 稳定排序
func (m *Manager) CurrentEmotions(ctx context.Context) ([]CurrentEmotion, error) {
	now := time.Now()
	var rows []EmotionState
	if err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := settleEmotionStatesTx(tx, now); err != nil {
			return err
		}
		return tx.Where("ended_at IS NULL").Find(&rows).Error
	}); err != nil {
		return nil, err
	}
	items := make([]CurrentEmotion, 0, len(rows))
	for _, row := range rows {
		items = append(items, toCurrentEmotion(row, now))
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Strength != items[j].Strength {
			return items[i].Strength > items[j].Strength
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
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

// AffectLevel 将玩家可读档位转换成辅助状态数值，未知档位返回错误
func AffectLevel(value string) (float64, error) {
	switch strings.TrimSpace(value) {
	case "low":
		return 0.2, nil
	case "high":
		return 0.8, nil
	case "normal":
		return 0.5, nil
	default:
		return 0, fmt.Errorf("精力或交流意愿只能是 low/normal/high")
	}
}
