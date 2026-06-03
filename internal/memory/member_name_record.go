package memory

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
)

func ParseMemberNameRecords(raw string) []MemberNameRecord {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var records []MemberNameRecord
	if err := sonic.UnmarshalString(raw, &records); err != nil {
		return nil
	}
	return normalizeMemberNameRecords(records)
}

func EncodeMemberNameRecords(records []MemberNameRecord) string {
	normalized := normalizeMemberNameRecords(records)
	if len(normalized) == 0 {
		return ""
	}
	b, err := sonic.MarshalString(normalized)
	if err != nil {
		return ""
	}
	return b
}

func normalizeMemberNameRecords(records []MemberNameRecord) []MemberNameRecord {
	if len(records) == 0 {
		return nil
	}

	type dedupeKey struct {
		content string
		source  MemberNameSource
		groupID int64
	}

	latestByKey := make(map[dedupeKey]MemberNameRecord, len(records))
	for _, record := range records {
		record.Content = strings.TrimSpace(record.Content)
		record.Source = MemberNameSource(strings.TrimSpace(string(record.Source)))
		if record.Content == "" {
			continue
		}
		switch record.Source {
		case MemberNameSourceGroupCard:
			if record.GroupID <= 0 {
				continue
			}
		case MemberNameSourceLearnedAlias:
			record.GroupID = 0
		default:
			continue
		}

		key := dedupeKey{
			content: strings.ToLower(record.Content),
			source:  record.Source,
			groupID: record.GroupID,
		}
		if existing, ok := latestByKey[key]; ok && existing.UpdatedAt.After(record.UpdatedAt) {
			continue
		}
		latestByKey[key] = record
	}

	normalized := make([]MemberNameRecord, 0, len(latestByKey))
	for _, record := range latestByKey {
		normalized = append(normalized, record)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].UpdatedAt.Equal(normalized[j].UpdatedAt) {
			if normalized[i].Source != normalized[j].Source {
				return normalized[i].Source < normalized[j].Source
			}
			if normalized[i].GroupID != normalized[j].GroupID {
				return normalized[i].GroupID < normalized[j].GroupID
			}
			return normalized[i].Content < normalized[j].Content
		}
		return normalized[i].UpdatedAt.After(normalized[j].UpdatedAt)
	})
	return normalized
}

func UpsertMemberGroupCard(records []MemberNameRecord, groupID int64, card string, updatedAt time.Time) []MemberNameRecord {
	card = strings.TrimSpace(card)
	if groupID <= 0 || card == "" {
		return normalizeMemberNameRecords(records)
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	records = append(records, MemberNameRecord{
		Content:   card,
		Source:    MemberNameSourceGroupCard,
		GroupID:   groupID,
		UpdatedAt: updatedAt,
	})
	return normalizeMemberNameRecords(records)
}

func UpsertMemberLearnedAliases(records []MemberNameRecord, aliases []string, updatedAt time.Time) []MemberNameRecord {
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		records = append(records, MemberNameRecord{
			Content:   alias,
			Source:    MemberNameSourceLearnedAlias,
			GroupID:   0,
			UpdatedAt: updatedAt,
		})
	}
	return normalizeMemberNameRecords(records)
}

func LatestMemberGroupCard(records []MemberNameRecord, groupID int64) string {
	if groupID <= 0 {
		return ""
	}
	for _, record := range normalizeMemberNameRecords(records) {
		if record.Source == MemberNameSourceGroupCard && record.GroupID == groupID {
			return record.Content
		}
	}
	return ""
}

func MemberLearnedAliases(records []MemberNameRecord) []string {
	normalized := normalizeMemberNameRecords(records)
	if len(normalized) == 0 {
		return nil
	}
	aliases := make([]string, 0, len(normalized))
	for _, record := range normalized {
		if record.Source == MemberNameSourceLearnedAlias {
			aliases = append(aliases, record.Content)
		}
	}
	return aliases
}

func MemberNamesForAdmin(records []MemberNameRecord, nickname string) []string {
	normalized := normalizeMemberNameRecords(records)
	if len(normalized) == 0 && strings.TrimSpace(nickname) == "" {
		return nil
	}

	seen := make(map[string]struct{}, len(normalized)+1)
	names := make([]string, 0, len(normalized)+1)
	push := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}

	push(nickname)
	for _, record := range normalized {
		push(record.Content)
	}
	return names
}

func (p *MemberProfile) MemberNameRecords() []MemberNameRecord {
	if p == nil {
		return nil
	}
	return ParseMemberNameRecords(p.NameRecords)
}

func (p *MemberProfile) UpsertGroupCard(groupID int64, card string, updatedAt time.Time) {
	if p == nil {
		return
	}
	p.NameRecords = EncodeMemberNameRecords(UpsertMemberGroupCard(p.MemberNameRecords(), groupID, card, updatedAt))
}

func (p *MemberProfile) UpsertLearnedAliases(aliases []string, updatedAt time.Time) {
	if p == nil {
		return
	}
	p.NameRecords = EncodeMemberNameRecords(UpsertMemberLearnedAliases(p.MemberNameRecords(), aliases, updatedAt))
}

func (m *Manager) UpdateMemberProfileLearned(profile *MemberProfile) error {
	if profile == nil {
		return nil
	}
	var existing MemberProfile
	err := m.db.Where("user_id = ?", profile.UserID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		profile.Activity = applyActivityUpdate(profile.Activity, time.Time{}, profile.LastSpeak)
		return m.db.Create(profile).Error
	}
	if err != nil {
		return err
	}
	profile.ID = existing.ID
	profile.Activity = applyActivityUpdate(profile.Activity, existing.LastSpeak, profile.LastSpeak)
	return m.db.Save(profile).Error
}
