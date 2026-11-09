package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"mumu-bot/internal/utils"
)

const MaxForwardFileBytes int64 = 32 * 1024 * 1024

// ForwardFilePath 只接受内容哈希，不信任节点里的本机路径
func ForwardFilePath(dir, hash string) (string, error) {
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("转发原件哈希无效")
	}
	return filepath.Join(dir, "forward-file", hash), nil
}

// CacheForwardFile 在临时链接失效前固定原件，节点而非快照引用负责其保留期限
func CacheForwardFile(ctx context.Context, dir, url string, declaredSize *int64) (string, int64, error) {
	if _, err := utils.ParseHTTPURL(url); err != nil {
		return "", 0, err
	}
	if declaredSize != nil && *declaredSize > MaxForwardFileBytes {
		return "", 0, fmt.Errorf("转发文件超过32 MiB")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("转发文件下载失败：网络请求未完成")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("下载转发文件返回 HTTP %d", resp.StatusCode)
	}
	root := filepath.Join(dir, "forward-file")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", 0, err
	}
	file, err := os.CreateTemp(root, ".download-*.tmp")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(file.Name())
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, MaxForwardFileBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return "", 0, copyErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	if size == 0 || size > MaxForwardFileBytes {
		return "", 0, fmt.Errorf("转发文件为空或超过32 MiB")
	}
	if declaredSize != nil && *declaredSize > 0 && size != *declaredSize {
		return "", 0, fmt.Errorf("转发文件大小不符：声明%d，实际%d", *declaredSize, size)
	}
	key := hex.EncodeToString(hash.Sum(nil))
	target := filepath.Join(root, key)
	if err := os.Rename(file.Name(), target); err != nil {
		return "", 0, err
	}
	return key, size, nil
}
