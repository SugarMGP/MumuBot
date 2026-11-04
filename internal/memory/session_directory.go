package memory

import (
	"context"
	"time"
)

const SessionDirectoryFilter = "session_directory"

type SessionDirectoryEntry struct {
	Kind            string    `json:"kind"`
	TargetID        int64     `json:"target_id"`
	Name            string    `json:"name"`
	Active          bool      `json:"active"`
	Blocked         bool      `json:"blocked"`
	WorkerStatus    string    `json:"worker_status,omitempty"`
	WorkerLastError string    `json:"worker_last_error,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
	KindRank        int       `json:"-"`
}

// ListSessionDirectory 读取全部群聊、私聊和当前 worker 池；停用的 worker 不进入目录
func (m *Manager) ListSessionDirectory(ctx context.Context, snapshot time.Time, lastTime time.Time, lastRank int, lastID uint64, limit int) ([]SessionDirectoryEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	if snapshot.IsZero() {
		snapshot = time.Now().UTC()
	}
	const baseQuery = `
WITH directory AS (
  SELECT t.conversation_kind AS kind,
         t.target_id,
         t.name,
         t.active,
         t.blocked,
         t.updated_at,
         COALESCE(w.status, '') AS worker_status,
         COALESCE(w.last_error, '') AS worker_last_error,
         CASE t.conversation_kind WHEN 'group' THEN 0 WHEN 'private' THEN 1 ELSE 2 END AS kind_rank
  FROM conversation_targets t
  LEFT JOIN worker_sessions w
    ON t.conversation_kind = 'worker' AND t.target_id = w.id
)
SELECT kind, target_id, name, active, blocked, worker_status, worker_last_error, updated_at, kind_rank
FROM directory
WHERE updated_at <= ?
  AND (kind <> 'worker' OR active)`
	where := baseQuery
	args := []any{snapshot}
	if lastID > 0 {
		where += " AND (updated_at < ? OR (updated_at = ? AND (kind_rank > ? OR (kind_rank = ? AND target_id > ?))))"
		args = append(args, lastTime, lastTime, lastRank, lastRank, int64(lastID))
	}
	where += " ORDER BY updated_at DESC, kind_rank, target_id LIMIT ?"
	args = append(args, limit)

	var rows []SessionDirectoryEntry
	err := m.db.WithContext(ctx).Raw(where, args...).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}
