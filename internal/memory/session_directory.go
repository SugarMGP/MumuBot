package memory

import (
	"context"
	"time"
)

const SessionDirectoryFilter = "session_directory"

// SessionDirectoryFilterValue 让不同角色的目录游标互不串用
type SessionDirectoryFilterValue struct {
	Scope          string `json:"scope"`
	ViewerKind     string `json:"viewer_kind"`
	ViewerTargetID int64  `json:"viewer_target_id,omitempty"`
}

type SessionDirectoryEntry struct {
	Kind            string     `json:"kind"`
	TargetID        int64      `json:"target_id"`
	Name            string     `json:"name"`
	Active          bool       `json:"active"`
	Blocked         bool       `json:"blocked"`
	WorkerStatus    string     `json:"worker_status,omitempty"`
	WorkerLastError string     `json:"worker_last_error,omitempty"`
	PendingCount    int        `json:"pending_count"`
	LastMessageAt   *time.Time `json:"last_message_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
	KindRank        int        `json:"-"`
}

// SessionDirectoryQuery 描述一次目录读取：角色视图、真实概况和游标
type SessionDirectoryQuery struct {
	ViewerKind     string
	ViewerTargetID int64
	SelfUserID     int64
	Snapshot       time.Time
	LastTime       time.Time
	LastRank       int
	LastID         uint64
	Limit          int
}

// ListSessionDirectory 按角色视图读取会话目录，附带待处理数量和最近真人消息时间；
// 角色过滤在分页查询之前完成，游标继续使用 updated_at + kind_rank + target_id
func (m *Manager) ListSessionDirectory(ctx context.Context, query SessionDirectoryQuery) ([]SessionDirectoryEntry, error) {
	if query.Limit <= 0 {
		return nil, nil
	}
	if query.Snapshot.IsZero() {
		query.Snapshot = time.Now().UTC()
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
         CASE t.conversation_kind WHEN 'group' THEN 0 WHEN 'private' THEN 1 WHEN 'life' THEN 2 ELSE 3 END AS kind_rank,
         (SELECT count(*) FROM conversation_transcript tr
           WHERE tr.conversation_kind = t.conversation_kind AND tr.target_id = t.target_id
             AND tr.visible_seq IS NULL AND tr.entry_kind IN ('user','observation','agent_message')) AS pending_count,
         CASE WHEN t.conversation_kind IN ('group','private')
              THEN (SELECT max(ml.message_time) FROM message_logs ml
                     WHERE ml.conversation_kind = t.conversation_kind AND ml.target_id = t.target_id
                       AND ml.user_id <> ?)
              ELSE NULL END AS last_message_at
  FROM conversation_targets t
  LEFT JOIN worker_sessions w
    ON t.conversation_kind = 'worker' AND t.target_id = w.id
)
SELECT kind, target_id, name, active, blocked, worker_status, worker_last_error,
       pending_count, last_message_at, updated_at, kind_rank
FROM directory
WHERE updated_at <= ?
  AND (kind <> 'worker' OR active)`
	statement := baseQuery
	args := []any{query.SelfUserID, query.Snapshot}
	switch query.ViewerKind {
	case ConversationKindLife:
		// life 看全部会话
	case ConversationKindWorker:
		statement += "\n  AND (kind='life' OR (kind='worker' AND target_id=?))"
		args = append(args, query.ViewerTargetID)
	default:
		statement += "\n  AND kind IN ('group','private','life')"
	}
	if query.LastID > 0 {
		statement += "\n  AND (updated_at < ? OR (updated_at = ? AND (kind_rank > ? OR (kind_rank = ? AND target_id > ?))))"
		args = append(args, query.LastTime, query.LastTime, query.LastRank, query.LastRank, int64(query.LastID))
	}
	statement += "\nORDER BY updated_at DESC, kind_rank, target_id LIMIT ?"
	args = append(args, query.Limit)

	var rows []SessionDirectoryEntry
	if err := m.db.WithContext(ctx).Raw(statement, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
