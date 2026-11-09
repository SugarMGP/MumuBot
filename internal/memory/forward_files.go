package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mumu-bot/internal/media"
)

// MediaDir 返回管理器实际使用的媒体目录
func (m *Manager) MediaDir() string { return m.mediaDir }

func (m *Manager) forwardFileExists(ctx context.Context, hash string) (bool, error) {
	var exists bool
	err := m.db.WithContext(ctx).Raw(`SELECT EXISTS (
		SELECT 1 FROM message_forwards mf, jsonb_array_elements(mf.nodes) node,
		jsonb_array_elements(COALESCE(node->'files','[]'::jsonb)) attachment
		WHERE attachment->>'cache_hash'=?
	)`, hash).Scan(&exists).Error
	return exists, err
}

// ReleaseForwardFiles 只清理已失去所有父消息引用的原件，不影响 transcript 快照
func (m *Manager) ReleaseForwardFiles(ctx context.Context, hashes []string) (int, error) {
	removed := 0
	seen := make(map[string]bool)
	for _, hash := range hashes {
		if seen[hash] {
			continue
		}
		seen[hash] = true
		used, err := m.forwardFileExists(ctx, hash)
		if err != nil {
			return removed, err
		}
		if used {
			continue
		}
		path, err := media.ForwardFilePath(m.mediaDir, hash)
		if err != nil {
			return removed, err
		}
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func (m *Manager) CleanupOrphanForwardFiles(ctx context.Context, now time.Time) (int, error) {
	root := filepath.Join(m.mediaDir, "forward-file")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return removed, err
		}
		if info.ModTime().After(now.Add(-24 * time.Hour)) {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".download-") && strings.HasSuffix(entry.Name(), ".tmp") {
			if err := os.Remove(filepath.Join(root, entry.Name())); err != nil && !os.IsNotExist(err) {
				return removed, err
			}
			removed++
			continue
		}
		if _, err := media.ForwardFilePath(m.mediaDir, entry.Name()); err != nil {
			continue
		}
		count, err := m.ReleaseForwardFiles(ctx, []string{entry.Name()})
		removed += count
		if err != nil {
			return removed, err
		}
	}
	return removed, nil
}
