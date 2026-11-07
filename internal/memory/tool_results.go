package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// SaveToolResultText 把大工具结果保存到内容寻址文件，返回 r 前缀句柄
func (m *Manager) SaveToolResultText(ctx context.Context, content string) (string, error) {
	dir := filepath.Join(m.mediaDir, "tool-results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(content))
	ref := "r" + fmt.Sprintf("%x", sum[:])
	path := filepath.Join(dir, strings.TrimPrefix(ref, "r")+".txt")
	if _, err := os.Stat(path); err == nil {
		return ref, nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return ref, nil
}

// ToolResultWindow 包含正文窗口及普通续读或关键词续查的位置
type ToolResultWindow struct {
	Content     string
	Total       int
	Offset      int
	NextOffset  int
	Complete    bool
	MatchOffset *int
}

// ReadToolResultText 按字符窗口读取结果；query 非空时定位首个字面命中并返回附近正文
func (m *Manager) ReadToolResultText(ctx context.Context, ref string, offset, length int, query string) (*ToolResultWindow, error) {
	ref = strings.TrimSpace(ref)
	if len(ref) != 65 || !strings.HasPrefix(ref, "r") {
		return nil, fmt.Errorf("无效的工具结果编号")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(ref, "r")); err != nil {
		return nil, fmt.Errorf("无效的工具结果编号")
	}
	if offset < 0 {
		return nil, fmt.Errorf("offset 不能为负数")
	}
	path := filepath.Join(m.mediaDir, "tool-results", strings.TrimPrefix(ref, "r")+".txt")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w", ErrToolResultUnavailable)
	}
	if err != nil {
		return nil, err
	}
	runes := []rune(string(data))
	if length <= 0 {
		length = 4000
	}
	if length > 8000 {
		length = 8000
	}
	window := &ToolResultWindow{Total: len(runes), Offset: offset, NextOffset: offset, Complete: true}
	if offset >= window.Total {
		return window, nil
	}
	if query != "" {
		tail := string(runes[offset:])
		index := strings.Index(tail, query)
		if index < 0 {
			window.NextOffset = window.Total
			return window, nil
		}
		match := offset + utf8.RuneCountInString(tail[:index])
		window.MatchOffset = &match
		window.Offset = max(0, match-min(200, length/4))
		// 从命中后的下一个字符续查，既不会重复同一命中，也不跳过重叠匹配
		window.NextOffset = match + 1
		window.Complete = !strings.Contains(string(runes[window.NextOffset:]), query)
	}
	end := window.Offset + min(length, window.Total-window.Offset)
	window.Content = string(runes[window.Offset:end])
	if query == "" {
		window.NextOffset = end
		window.Complete = end == window.Total
	}
	return window, nil
}

var toolResultRefPattern = regexp.MustCompile(`r[0-9a-f]{64}`)

// CleanupToolResults 删除超过保留期且不在 transcript 引用闭包内的文件
func (m *Manager) CleanupToolResults(ctx context.Context, minAge time.Duration) error {
	if minAge < 0 {
		return fmt.Errorf("清理保留期不能为负数")
	}
	if strings.TrimSpace(m.mediaDir) == "" {
		return nil
	}
	dir := filepath.Join(m.mediaDir, "tool-results")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-minAge)
	oldFiles := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		ref := "r" + strings.TrimSuffix(entry.Name(), ".txt")
		oldFiles[ref] = filepath.Join(dir, entry.Name())
	}
	if len(oldFiles) == 0 {
		return nil
	}
	roots, err := m.transcriptToolResultRoots(ctx)
	if err != nil {
		return err
	}
	reachable, err := expandToolResultRefs(dir, roots)
	if err != nil {
		return err
	}
	for ref, path := range oldFiles {
		if reachable[ref] {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (m *Manager) transcriptToolResultRoots(ctx context.Context) ([]string, error) {
	rows, err := m.db.WithContext(ctx).Raw(`
		SELECT result_ref AS content FROM conversation_transcript
			WHERE result_ref<>'' AND entry_kind IN ('assistant','tool','user','observation','agent_message')
		UNION ALL SELECT payload::text FROM conversation_transcript
			WHERE entry_kind IN ('user','observation','agent_message','assistant','tool')`).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	roots := make([]string, 0)
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		for _, ref := range toolResultRefPattern.FindAllString(text, -1) {
			if !seen[ref] {
				seen[ref] = true
				roots = append(roots, ref)
			}
		}
	}
	return roots, rows.Err()
}

// expandToolResultRefs 返回根及其嵌套引用闭包，已经不存在或损坏的文件不伪造结果
func expandToolResultRefs(dir string, roots []string) (map[string]bool, error) {
	reachable := map[string]bool{}
	var visit func(string) error
	visit = func(ref string) error {
		if !toolResultRefPattern.MatchString(ref) || reachable[ref] {
			return nil
		}
		path := filepath.Join(dir, strings.TrimPrefix(ref, "r")+".txt")
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		reachable[ref] = true
		for _, nested := range toolResultRefPattern.FindAllString(string(data), -1) {
			if err := visit(nested); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range roots {
		if err := visit(root); err != nil {
			return nil, err
		}
	}
	return reachable, nil
}

var (
	// ErrToolResultUnavailable 表示结果文件已按保留规则清理
	ErrToolResultUnavailable = errors.New("工具结果已清理或不可用")
)

// ReleaseToolResults 在 transcript 删除提交后释放只属于这批行的结果文件
func (m *Manager) ReleaseToolResults(ctx context.Context, refs []string) (int, error) {
	if len(refs) == 0 || strings.TrimSpace(m.mediaDir) == "" {
		return 0, nil
	}
	dir := filepath.Join(m.mediaDir, "tool-results")
	candidates, err := expandToolResultRefs(dir, refs)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	roots, err := m.transcriptToolResultRoots(ctx)
	if err != nil {
		return 0, err
	}
	reachable, err := expandToolResultRefs(dir, roots)
	if err != nil {
		return 0, err
	}
	removed := 0
	for ref := range candidates {
		if reachable[ref] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, strings.TrimPrefix(ref, "r")+".txt")); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
