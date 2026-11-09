package memory

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SyncMemberProfiles 批量记录名单资料，保留旧名片且不制造发言或记忆
func (m *Manager) SyncMemberProfiles(ctx context.Context, profiles []MemberProfile, names []MemberName) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(profiles) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "user_id"}},
				DoUpdates: clause.Assignments(map[string]any{
					"nickname":   gorm.Expr("CASE WHEN EXCLUDED.nickname<>'' AND EXCLUDED.updated_at>=member_profiles.updated_at THEN EXCLUDED.nickname ELSE member_profiles.nickname END"),
					"updated_at": gorm.Expr("GREATEST(member_profiles.updated_at, EXCLUDED.updated_at)"),
				}),
			}).CreateInBatches(profiles, 200).Error; err != nil {
				return err
			}
		}
		if len(names) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "group_id"}, {Name: "value"}},
			DoUpdates: clause.Assignments(map[string]any{"updated_at": gorm.Expr("GREATEST(member_names.updated_at, EXCLUDED.updated_at)")}),
		}).CreateInBatches(names, 200).Error
	})
}

type MemberSearchQuery struct {
	Keyword   string
	GroupID   int64
	MemberIDs []int64
	Cursor    string
	Limit     int
}

type MemberSearchHit struct {
	MemberProfile
	Names              []MemberName `json:"names"`
	CurrentGroupMember *bool        `json:"current_group_member,omitempty"`
}

type MemberSearchPage struct {
	Members    []MemberSearchHit `json:"members"`
	NextCursor string            `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
}

func (m *Manager) SearchMembers(ctx context.Context, input MemberSearchQuery) (*MemberSearchPage, error) {
	keyword := strings.TrimSpace(input.Keyword)
	filter := struct {
		Keyword string
		GroupID int64
	}{keyword, input.GroupID}
	cursor, err := DecodeCursor(input.Cursor)
	if err != nil {
		return nil, err
	}
	if cursor.Sort != "" && cursor.Sort != "members" {
		return nil, ErrInvalidCursor
	}
	if err := ValidateCursorFilter(cursor, filter); err != nil {
		return nil, err
	}
	snapshot := CursorTimeOrNow(cursor)
	q := m.db.WithContext(ctx).Model(&MemberProfile{})
	if keyword != "" {
		pattern := LiteralContainsPattern(keyword)
		q = q.Where("CAST(user_id AS TEXT)=? OR nickname ILIKE ? OR EXISTS (SELECT 1 FROM member_names mn WHERE mn.user_id=member_profiles.user_id AND mn.value ILIKE ?)", keyword, pattern, pattern)
	}
	if input.GroupID > 0 {
		q = q.Where("user_id IN ?", input.MemberIDs)
	}
	if cursor.LastID > 0 {
		q = q.Where("user_id<?", cursor.LastID)
	}
	limit := NormalizeLimit(input.Limit)
	var profiles []MemberProfile
	if err := q.Order("user_id DESC").Limit(limit + 1).Find(&profiles).Error; err != nil {
		return nil, err
	}
	page := &MemberSearchPage{Members: []MemberSearchHit{}, HasMore: len(profiles) > limit}
	if page.HasMore {
		profiles = profiles[:limit]
	}
	if len(profiles) == 0 {
		return page, nil
	}
	ids := make([]int64, len(profiles))
	for i, row := range profiles {
		ids[i] = row.UserID
	}
	var names []MemberName
	if err := m.db.WithContext(ctx).Where("user_id IN ?", ids).Order("updated_at DESC,group_id,value").Find(&names).Error; err != nil {
		return nil, err
	}
	byUser := make(map[int64][]MemberName)
	for _, name := range names {
		if keyword == "" || strings.Contains(strings.ToLower(name.Value), strings.ToLower(keyword)) || fmt.Sprint(name.UserID) == keyword {
			byUser[name.UserID] = append(byUser[name.UserID], name)
		}
	}
	for _, row := range profiles {
		page.Members = append(page.Members, MemberSearchHit{MemberProfile: row, Names: byUser[row.UserID]})
	}
	if page.HasMore {
		last := profiles[len(profiles)-1]
		page.NextCursor = NewPageCursor("members", 0, last.UpdatedAt, uint(last.UserID), snapshot, filter)
	}
	return page, nil
}
