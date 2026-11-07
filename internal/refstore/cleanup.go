package refstore

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Store) CleanupCandidates(roots map[string]bool, candidates []string) (int, int, error) {
	s.gcMu.Lock()
	defer s.gcMu.Unlock()
	removedRefs := 0
	hashes := make([]string, 0)
	seen := map[string]bool{}
	for _, token := range candidates {
		id, _, err := Parse(token)
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true
		ref, getErr := s.get(id)
		if getErr != nil {
			continue
		}
		if roots[ref.FileRef()] || roots[ref.ImageRef()] {
			continue
		}
		if err := os.Remove(s.metaPath(id)); err != nil && !os.IsNotExist(err) {
			return removedRefs, 0, err
		}
		removedRefs++
		if ref.SHA256 != "" {
			hashes = append(hashes, ref.SHA256)
		}
	}
	removedBlobs := 0
	for _, hash := range hashes {
		if s.hashReferenced(hash) {
			continue
		}
		if err := os.Remove(s.blobPath(hash)); err != nil && !os.IsNotExist(err) {
			return removedRefs, removedBlobs, err
		}
		removedBlobs++
	}
	return removedRefs, removedBlobs, nil
}

// Cleanup 只删除超过宽限期且没有现存引用的 ref；blob 在所有 ref 都不再引用时删除
func (s *Store) Cleanup(roots map[string]bool, minAge time.Duration) (int, int, error) {
	s.gcMu.Lock()
	defer s.gcMu.Unlock()
	entries, err := os.ReadDir(s.refsDir())
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	cutoff := time.Now().Add(-minAge)
	liveHashes := map[string]bool{}
	removedRefs := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return removedRefs, 0, infoErr
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		ref, getErr := s.get(id)
		if getErr != nil {
			continue
		}
		if roots[ref.FileRef()] || roots[ref.ImageRef()] || info.ModTime().After(cutoff) {
			if ref.SHA256 != "" {
				liveHashes[ref.SHA256] = true
			}
			continue
		}
		if err := os.Remove(filepath.Join(s.refsDir(), entry.Name())); err != nil && !os.IsNotExist(err) {
			return removedRefs, 0, err
		}
		removedRefs++
	}
	// 中断遗留的临时字节和元数据不属于任何 ref，按固定宽限期清理
	s.cleanupTempFiles(24 * time.Hour)
	blobEntries, err := os.ReadDir(s.blobsDir())
	if os.IsNotExist(err) {
		return removedRefs, 0, nil
	}
	if err != nil {
		return removedRefs, 0, err
	}
	removedBlobs := 0
	for _, entry := range blobEntries {
		if entry.IsDir() {
			continue
		}
		if liveHashes[entry.Name()] {
			continue
		}
		// 元数据仍存在但尚未参与 roots 的共享 hash 不能删除；再扫一遍 refs 兜底
		if s.hashReferenced(entry.Name()) {
			continue
		}
		if err := os.Remove(filepath.Join(s.blobsDir(), entry.Name())); err != nil && !os.IsNotExist(err) {
			return removedRefs, removedBlobs, err
		}
		removedBlobs++
	}
	return removedRefs, removedBlobs, nil
}

// cleanupTempFiles 清理建快照中断留下的临时文件和元数据临时文件
func (s *Store) cleanupTempFiles(minAge time.Duration) {
	cutoff := time.Now().Add(-minAge)
	for _, dir := range []string{s.tempDir(), s.refsDir()} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if dir == s.refsDir() && !strings.HasPrefix(name, ".meta-") {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil || info.ModTime().After(cutoff) {
				continue
			}
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func (s *Store) hashReferenced(hash string) bool {
	entries, err := os.ReadDir(s.refsDir())
	if err != nil {
		return true
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		ref, getErr := s.get(strings.TrimSuffix(entry.Name(), ".json"))
		if getErr == nil && ref.SHA256 == hash {
			return true
		}
	}
	return false
}
