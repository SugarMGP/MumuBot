package memory

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var intimacyLevelNames = [...]string{"极度反感", "反感", "不喜欢", "冷淡", "疏离", "普通", "友好", "亲近", "喜欢", "亲密"}

func (m *Manager) RecordMemberName(userID, groupID int64, value string, updatedAt time.Time) error {
	value = strings.TrimSpace(value)
	if userID == 0 || groupID == 0 || value == "" {
		return nil
	}
	name := MemberName{UserID: userID, GroupID: groupID, Value: value, UpdatedAt: updatedAt}
	return m.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "group_id"}, {Name: "value"}},
		DoUpdates: clause.Assignments(map[string]any{"updated_at": gorm.Expr("GREATEST(member_names.updated_at, EXCLUDED.updated_at)")}),
	}).Create(&name).Error
}

func (m *Manager) LatestMemberGroupCard(userID, groupID int64) (string, error) {
	var row MemberName
	err := m.db.Where("user_id = ? AND group_id = ?", userID, groupID).Order("updated_at DESC").First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	return row.Value, err
}

// IntimacyLevel 将 -1 到 1 的好感度映射为 1 到 10 级
func IntimacyLevel(value float64) int {
	value = min(max(value, -1.0), 1.0)
	level := int((value+1)*5) + 1
	if level > 10 {
		return 10
	}
	return level
}

// IntimacyLevelName 返回好感度等级的中文名称
func IntimacyLevelName(value float64) string {
	return intimacyLevelNames[IntimacyLevel(value)-1]
}

// UpdateMemberIntimacy 按固定增量更新成员好感度，并记录变化原因
func (m *Manager) UpdateMemberIntimacy(userID int64, delta float64, reason string) (*MemberProfile, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("成员 QQ 号无效")
	}
	if delta < -0.15 || delta > 0.15 {
		return nil, fmt.Errorf("好感度变化超出允许范围")
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > 200 {
		reason = string([]rune(reason)[:200])
	}

	var profile MemberProfile
	err := m.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).First(&profile).Error; err != nil {
			return err
		}
		before := profile.Intimacy
		profile.Intimacy = min(max(profile.Intimacy+delta, -1.0), 1.0)
		if err := tx.Model(&MemberProfile{}).Where("user_id = ?", userID).Update("intimacy", profile.Intimacy).Error; err != nil {
			return err
		}
		return tx.Create(&MemberIntimacyLog{UserID: userID, Delta: profile.Intimacy - before, BeforeValue: before, AfterValue: profile.Intimacy, Reason: reason, CreatedAt: time.Now()}).Error
	})
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

// RecentIntimacyLogs 读取若干成员各自最近的好感度变化
func (m *Manager) RecentIntimacyLogs(userIDs []int64, perUser int) (map[int64][]MemberIntimacyLog, error) {
	result := map[int64][]MemberIntimacyLog{}
	if len(userIDs) == 0 {
		return result, nil
	}
	if perUser <= 0 || perUser > 20 {
		perUser = 8
	}
	var rows []MemberIntimacyLog
	if err := m.db.Raw(`SELECT id,user_id,delta,before_value,after_value,reason,created_at FROM (
		SELECT *,row_number() OVER (PARTITION BY user_id ORDER BY created_at DESC,id DESC) AS position
		FROM member_intimacy_logs WHERE user_id=ANY(?::bigint[])
	) recent WHERE position<=? ORDER BY user_id,created_at DESC,id DESC`, int64Array(userIDs), perUser).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.UserID] = append(result[row.UserID], row)
	}
	return result, nil
}

// ListMemberNames 读取成员在群里的称呼，按来源群和更新时间返回
func (m *Manager) ListMemberNames(userID int64) ([]MemberName, error) {
	rows := make([]MemberName, 0, 4)
	if userID <= 0 {
		return rows, nil
	}
	err := m.db.Where("user_id=?", userID).Order("group_id, updated_at DESC").Find(&rows).Error
	return rows, err
}
