package memory

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var ErrInvalidCursor = errors.New("cursor 无效或筛选条件已变化")

const (
	DefaultPageLimit = 12
	MaxPageLimit     = 99
)

type PageCursor struct {
	Version    int       `json:"v"`
	Sort       string    `json:"sort"`
	LastKey    float64   `json:"last_key,omitempty"`
	LastTime   time.Time `json:"last_time,omitempty"`
	LastID     uint64    `json:"last_id,omitempty"`
	SnapshotAt time.Time `json:"snapshot_at,omitempty"`
	FilterHash string    `json:"filter_hash"`
}

func NormalizeLimit(limit int) int {
	if limit <= 0 {
		return DefaultPageLimit
	}
	if limit > MaxPageLimit {
		return MaxPageLimit
	}
	return limit
}

func EncodeCursor(cursor PageCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func DecodeCursor(raw string) (PageCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PageCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return PageCursor{}, ErrInvalidCursor
	}
	var cursor PageCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.Version != 1 || cursor.Sort == "" || cursor.FilterHash == "" {
		return PageCursor{}, ErrInvalidCursor
	}
	return cursor, nil
}

func CursorFilterHash(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ValidateCursorFilter(cursor PageCursor, filter any) error {
	if cursor.FilterHash != "" && cursor.FilterHash != CursorFilterHash(filter) {
		return ErrInvalidCursor
	}
	return nil
}

func NewPageCursor(sort string, lastKey float64, lastTime time.Time, lastID uint, snapshot time.Time, filter any) string {
	return EncodeCursor(PageCursor{
		Version: 1, Sort: sort, LastKey: lastKey, LastTime: lastTime,
		LastID: uint64(lastID), SnapshotAt: snapshot, FilterHash: CursorFilterHash(filter),
	})
}

func CursorTimeOrNow(cursor PageCursor) time.Time {
	if cursor.SnapshotAt.IsZero() {
		return time.Now().UTC()
	}
	return cursor.SnapshotAt
}

func NormalizeKeywords(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}

func NormalizeMatch(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "or", nil
	}
	if raw != "or" && raw != "and" {
		return "", fmt.Errorf("match 只能是 or 或 and")
	}
	return raw, nil
}
