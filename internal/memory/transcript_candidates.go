package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mumu-bot/internal/refstore"

	"gorm.io/gorm"
)

// TranscriptCandidates 是一次 transcript 删除事务产生的引用候选
type TranscriptCandidates struct {
	ToolRefs     []string
	SnapshotRefs []string
}

func (c TranscriptCandidates) Empty() bool {
	return len(c.ToolRefs) == 0 && len(c.SnapshotRefs) == 0
}

// transcriptCandidates 在删除前收集本批行的工具结果和快照引用
func transcriptCandidates(tx *gorm.DB, condition string, args ...any) (TranscriptCandidates, error) {
	var rows []struct {
		ResultRef string `gorm:"column:result_ref"`
		Payload   string `gorm:"column:payload"`
	}
	if err := tx.Raw(`SELECT result_ref,payload::text AS payload FROM conversation_transcript WHERE `+condition, args...).Scan(&rows).Error; err != nil {
		return TranscriptCandidates{}, err
	}
	out := TranscriptCandidates{}
	seenTool := map[string]bool{}
	seenSnapshot := map[string]bool{}
	add := func(text string) {
		for _, ref := range toolResultRefPattern.FindAllString(text, -1) {
			if !seenTool[ref] {
				seenTool[ref] = true
				out.ToolRefs = append(out.ToolRefs, ref)
			}
		}
		for _, token := range refstore.ExtractRefs(text) {
			if !seenSnapshot[token] {
				seenSnapshot[token] = true
				out.SnapshotRefs = append(out.SnapshotRefs, token)
			}
		}
	}
	for _, row := range rows {
		add(row.ResultRef)
		add(row.Payload)
	}
	return out, nil
}

// ExpandToolResultSnapshots 在删除 r 文本前取出其嵌套 file/image 引用
func (m *Manager) ExpandToolResultSnapshots(ctx context.Context, toolRefs []string) ([]string, error) {
	if len(toolRefs) == 0 {
		return nil, nil
	}
	dir := filepath.Join(m.mediaDir, "tool-results")
	reachable, err := expandToolResultRefs(dir, toolRefs)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]string, 0)
	for ref := range reachable {
		data, readErr := os.ReadFile(filepath.Join(dir, strings.TrimPrefix(ref, "r")+".txt"))
		if readErr != nil {
			return nil, fmt.Errorf("读取工具结果 %s 失败: %w", ref, readErr)
		}
		for _, token := range refstore.ExtractRefs(string(data)) {
			if !seen[token] {
				seen[token] = true
				out = append(out, token)
			}
		}
	}
	return out, nil
}

// CleanupSnapshotCandidates 只释放本批删除动作的候选，不扫描其它孤儿
func (m *Manager) CleanupSnapshotCandidates(ctx context.Context, candidates []string) (int, int, error) {
	if m == nil || m.refs == nil || len(candidates) == 0 {
		return 0, 0, nil
	}
	roots, err := m.TranscriptSnapshotRoots(ctx)
	if err != nil {
		return 0, 0, err
	}
	return m.refs.CleanupCandidates(roots, candidates)
}
