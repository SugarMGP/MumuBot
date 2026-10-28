package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mumu-bot/internal/config"
)

// SaveToolResultText 把大工具结果保存到内容寻址文件，返回 r 前缀句柄
func (m *Manager) SaveToolResultText(ctx context.Context, content string) (string, error) {
	dir := filepath.Join(config.Get().Media.StoragePath, "tool-results")
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

// ReadToolResultText 按字符窗口读取已经保存的工具结果
func (m *Manager) ReadToolResultText(ctx context.Context, ref string, offset, length int) (string, int, bool, error) {
	ref = strings.TrimSpace(ref)
	if len(ref) != 65 || !strings.HasPrefix(ref, "r") {
		return "", 0, false, fmt.Errorf("无效的工具结果编号")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(ref, "r")); err != nil {
		return "", 0, false, fmt.Errorf("无效的工具结果编号")
	}
	path := filepath.Join(config.Get().Media.StoragePath, "tool-results", strings.TrimPrefix(ref, "r")+".txt")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, false, err
	}
	runes := []rune(string(data))
	if offset < 0 {
		return "", len(runes), false, fmt.Errorf("offset 不能为负数")
	}
	if length <= 0 {
		length = 4000
	}
	if length > 8000 {
		length = 8000
	}
	if offset >= len(runes) {
		return "", len(runes), true, nil
	}
	end := min(len(runes), offset+length)
	return string(runes[offset:end]), len(runes), end >= len(runes), nil
}

// CleanupToolResults 删除超过保留期且没有被工作集、话题或记忆引用的工具结果文件
func (m *Manager) CleanupToolResults(ctx context.Context, minAge time.Duration) error {
	if minAge < 0 {
		return fmt.Errorf("清理保留期不能为负数")
	}
	dir := filepath.Join(config.Get().Media.StoragePath, "tool-results")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var refs []string
	if err := m.db.WithContext(ctx).Model(&TranscriptEntry{}).Where("result_ref<>''").Distinct().Pluck("result_ref", &refs).Error; err != nil {
		return err
	}
	var topicTexts []string
	if err := m.db.WithContext(ctx).Model(&TopicSummaryRow{}).Select("summary_json::text").Scan(&topicTexts).Error; err != nil {
		return err
	}
	var sourceTexts []string
	if err := m.db.WithContext(ctx).Model(&KnowledgeRecord{}).Select("source_meta::text").Scan(&sourceTexts).Error; err != nil {
		return err
	}
	referenceText := strings.Join(refs, "\n") + "\n" + strings.Join(topicTexts, "\n") + "\n" + strings.Join(sourceTexts, "\n")
	cutoff := time.Now().Add(-minAge)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || info.ModTime().After(cutoff) {
			continue
		}
		ref := "r" + strings.TrimSuffix(entry.Name(), ".txt")
		if strings.Contains(referenceText, ref) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
