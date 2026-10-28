package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var EmotionKinds = map[string]bool{
	"喜悦": true, "好奇": true, "亲近": true, "感动": true, "思念": true, "吃醋": true,
	"担忧": true, "安心": true, "委屈": true, "失落": true, "不满": true, "愧疚": true,
}

type EmotionChange struct {
	EmotionID       uint
	Kind            string
	AboutUserID     int64
	Intensity       string
	DecayClass      string
	Reason          string
	SourceMeta      string
	ExpectedVersion uint64
}

type CurrentEmotion struct {
	ID          uint      `json:"id"`
	Kind        string    `json:"kind"`
	AboutUserID int64     `json:"about_user_id"`
	Strength    float64   `json:"strength"`
	Reason      string    `json:"reason"`
	OccurredAt  time.Time `json:"occurred_at"`
	AnchorAt    time.Time `json:"anchor_at"`
	DecayClass  string    `json:"decay_class"`
	Version     uint64    `json:"version"`
}

func intensityValue(value string) (float64, bool) {
	switch strings.TrimSpace(value) {
	case "轻微":
		return 0.25, false
	case "明显":
		return 0.60, false
	case "强烈":
		return 1.0, false
	case "已平复":
		return 0, true
	default:
		return 0, false
	}
}

func currentStrength(state EmotionState, now time.Time) float64 {
	if state.EndedAt != nil {
		return 0
	}
	half := 12 * time.Hour
	if state.DecayClass == "brief" {
		half = time.Hour
	}
	hours := now.Sub(state.AnchorAt).Hours()
	if hours < 0 {
		hours = 0
	}
	strength := state.Strength * math.Pow(2, -hours/half.Hours())
	if strength < 0.10 {
		return 0
	}
	return strength
}

// UpdateEmotions 更新最多 4 条具体情感，同一调用内全部成功或全部回滚
func (m *Manager) UpdateEmotions(ctx context.Context, changes []EmotionChange) ([]CurrentEmotion, error) {
	if len(changes) == 0 || len(changes) > 4 {
		return nil, fmt.Errorf("一次情感更新必须为 1 到 4 项")
	}
	now := time.Now()
	var output []CurrentEmotion
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, change := range changes {
			kind := strings.TrimSpace(change.Kind)
			intensity := strings.TrimSpace(change.Intensity)
			decayClass := strings.TrimSpace(change.DecayClass)
			if !EmotionKinds[kind] {
				return fmt.Errorf("未知情感类型 %s", kind)
			}
			if change.AboutUserID < 0 {
				return fmt.Errorf("情感对象无效")
			}
			if decayClass != "" && decayClass != "brief" && decayClass != "lingering" {
				return fmt.Errorf("情感衰减类型无效")
			}
			strength, settled := intensityValue(intensity)
			if !settled && strength == 0 {
				return fmt.Errorf("未知情感强度 %s", change.Intensity)
			}
			if intensity != "已平复" && strings.TrimSpace(change.Reason) == "" {
				return fmt.Errorf("情感变化必须说明原因")
			}
			var state EmotionState
			var err error
			if change.EmotionID > 0 {
				err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", change.EmotionID).Take(&state).Error
			} else {
				err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("about_user_id=? AND kind=?", change.AboutUserID, kind).Take(&state).Error
			}
			if err == gorm.ErrRecordNotFound {
				state = EmotionState{AboutUserID: change.AboutUserID, Kind: kind, Strength: strength, OccurredAt: now, AnchorAt: now, DecayClass: decayClass, Reason: strings.TrimSpace(change.Reason), SourceMeta: normalizedSourceMeta(change.SourceMeta), Version: 1}
				if state.DecayClass == "" {
					state.DecayClass = "lingering"
				}
				if settled {
					state.Strength = 0
					state.EndedAt = &now
					if state.Reason == "" {
						state.Reason = "情绪已平复"
					}
				}
				if err := tx.Create(&state).Error; err != nil {
					return err
				}
				if err := tx.Create(&EmotionRevision{
					EmotionID: state.ID, BeforeStrength: 0, AfterStrength: state.Strength,
					Reason: state.Reason, SourceMeta: state.SourceMeta, Version: state.Version,
				}).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			} else {
				if change.ExpectedVersion != 0 && change.ExpectedVersion != state.Version {
					return fmt.Errorf("情感 %d 已被其他请求修改，请重新读取后再试", state.ID)
				}
				before := currentStrength(state, now)
				state.Version++
				state.OccurredAt = now
				state.AnchorAt = now
				state.Reason = strings.TrimSpace(change.Reason)
				if state.Reason == "" && settled {
					state.Reason = "情绪已平复"
				}
				if change.SourceMeta != "" {
					state.SourceMeta = normalizedSourceMeta(change.SourceMeta)
				}
				if decayClass != "" {
					state.DecayClass = decayClass
				}
				if settled {
					state.Strength = 0
					state.EndedAt = &now
				} else {
					state.Strength = strength
					state.EndedAt = nil
				}
				if err := tx.Save(&state).Error; err != nil {
					return err
				}
				if err := tx.Create(&EmotionRevision{EmotionID: state.ID, BeforeStrength: before, AfterStrength: state.Strength, Reason: state.Reason, SourceMeta: state.SourceMeta, Version: state.Version}).Error; err != nil {
					return err
				}
			}
			output = append(output, CurrentEmotion{ID: state.ID, Kind: state.Kind, AboutUserID: state.AboutUserID, Strength: currentStrength(state, now), Reason: state.Reason, OccurredAt: state.OccurredAt, AnchorAt: state.AnchorAt, DecayClass: state.DecayClass, Version: state.Version})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return output, nil
}

// CurrentEmotions 返回按强度排序且已经应用惰性衰减的情感
func (m *Manager) CurrentEmotions(ctx context.Context, aboutUserID int64, limit int) ([]CurrentEmotion, error) {
	now := time.Now()
	var rows []EmotionState
	q := m.db.WithContext(ctx).Where("ended_at IS NULL")
	if aboutUserID >= 0 {
		q = q.Where("about_user_id=?", aboutUserID)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]CurrentEmotion, 0, len(rows))
	for _, row := range rows {
		strength := currentStrength(row, now)
		if strength < 0.10 {
			continue
		}
		items = append(items, CurrentEmotion{ID: row.ID, Kind: row.Kind, AboutUserID: row.AboutUserID, Strength: strength, Reason: row.Reason, OccurredAt: row.OccurredAt, AnchorAt: row.AnchorAt, DecayClass: row.DecayClass, Version: row.Version})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Strength != items[j].Strength {
			return items[i].Strength > items[j].Strength
		}
		return items[i].OccurredAt.After(items[j].OccurredAt)
	})
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	if limit > 3 {
		limit = 3
	}
	return items[:limit], nil
}

// AffectLevel 将玩家可读档位转换成辅助状态数值
func AffectLevel(value string, current float64) float64 {
	switch strings.TrimSpace(value) {
	case "low":
		return 0.2
	case "high":
		return 0.8
	case "normal":
		return 0.5
	default:
		return current
	}
}
