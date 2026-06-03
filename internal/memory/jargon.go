package memory

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// SearchJargons 搜索黑话（通过关键词匹配，本群优先）
func (m *Manager) SearchJargons(groupID int64, keyword string, limit int) ([]Jargon, error) {
	var jargons []Jargon
	q := m.db.Model(&Jargon{}).Where("rejected = ?", false)

	if keyword != "" {
		keywords := strings.Fields(keyword)
		if len(keywords) > 0 {
			likeConditions := make([]string, 0, len(keywords))
			args := make([]interface{}, 0, len(keywords))
			for _, kw := range keywords {
				likeConditions = append(likeConditions, "content LIKE ?")
				args = append(args, "%"+kw+"%")
			}
			q = q.Where(strings.Join(likeConditions, " OR "), args...)
		}
	}

	err := q.Order(fmt.Sprintf("CASE WHEN group_id = %d THEN 0 ELSE 1 END, checked DESC", groupID)).
		Limit(limit).Find(&jargons).Error
	return jargons, err
}

// SaveJargon 保存黑话/术语
func (m *Manager) SaveJargon(jargon *Jargon) error {
	var existing Jargon
	err := m.db.Where("group_id = ? AND content = ?", jargon.GroupID, jargon.Content).First(&existing).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return m.db.Create(jargon).Error
	} else if err != nil {
		return err
	}

	updates := map[string]any{
		"meaning":  jargon.Meaning,
		"context":  jargon.Context,
		"checked":  false,
		"rejected": false,
	}
	return m.db.Model(&existing).Updates(updates).Error
}

// BatchReviewJargon 批量审核黑话
func (m *Manager) BatchReviewJargon(ids []uint, approve bool) error {
	if len(ids) == 0 {
		return nil
	}
	updates := map[string]any{
		"checked": true,
	}
	if approve {
		updates["rejected"] = false
	} else {
		updates["rejected"] = true
	}
	return m.db.Model(&Jargon{}).Where("id IN ?", ids).Updates(updates).Error
}

// GetUncheckedJargons 获取待审核的黑话
func (m *Manager) GetUncheckedJargons(groupID int64, limit int) ([]Jargon, error) {
	var jargons []Jargon
	err := m.db.Where("group_id = ? AND checked = ?", groupID, false).
		Limit(limit).Find(&jargons).Error
	return jargons, err
}

// GetAllApprovedJargons 获取所有已审核通过的黑话（用于构建 AC 自动机）
func (m *Manager) GetAllApprovedJargons() ([]Jargon, error) {
	var jargons []Jargon
	err := m.db.Where("checked = ? AND rejected = ?", true, false).Find(&jargons).Error
	return jargons, err
}
