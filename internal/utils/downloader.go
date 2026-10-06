package utils

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// DownloadResult 下载结果
type DownloadResult struct {
	FilePath string // 本地文件完整路径
	FileName string // 文件名（uuid.ext）
	FileHash string // 文件MD5哈希
}

// DownloadImage 下载图片到指定目录
// url: 图片URL
// storageDir: 存储目录
// maxSizeMB: 最大文件大小限制（MB），0表示不限制
func DownloadImage(ctx context.Context, url string, storageDir string, maxSizeMB int) (*DownloadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	// 确保存储目录存在
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		return nil, fmt.Errorf("创建存储目录失败: %w", err)
	}

	resp, err := OpenPublicHTTP(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("下载图片失败: %w", err)
	}
	defer resp.Body.Close()

	// 检查文件大小
	if maxSizeMB > 0 && resp.ContentLength > int64(maxSizeMB)*1024*1024 {
		return nil, fmt.Errorf("文件大小超过限制: %d MB > %d MB", resp.ContentLength/1024/1024, maxSizeMB)
	}

	// 获取文件扩展名
	ext := getExtensionFromURL(resp.Request.URL.String())
	if ext == "" {
		switch ct := resp.Header.Get("Content-Type"); {
		case strings.Contains(ct, "jpeg"):
			ext = ".jpg"
		case strings.Contains(ct, "png"):
			ext = ".png"
		case strings.Contains(ct, "gif"):
			ext = ".gif"
		case strings.Contains(ct, "webp"):
			ext = ".webp"
		case strings.Contains(ct, "bmp"):
			ext = ".bmp"
		}
	}
	if ext == "" {
		ext = ".jpg" // 默认扩展名
	}

	// 生成唯一文件名
	fileName := uuid.New().String() + ext
	filePath := filepath.Join(storageDir, fileName)

	// 创建临时文件
	tmpFile, err := os.CreateTemp(storageDir, "download_*.tmp")
	if err != nil {
		return nil, fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath) // 清理临时文件
	}()

	// 同时计算MD5和写入文件
	hash := md5.New()
	writer := io.MultiWriter(tmpFile, hash)

	// 限制读取大小
	var reader io.Reader = resp.Body
	if maxSizeMB > 0 {
		reader = io.LimitReader(resp.Body, int64(maxSizeMB)*1024*1024+1)
	}

	written, err := io.Copy(writer, reader)
	if err != nil {
		return nil, fmt.Errorf("写入文件失败: %w", err)
	}

	// 再次检查文件大小
	if maxSizeMB > 0 && written > int64(maxSizeMB)*1024*1024 {
		return nil, fmt.Errorf("文件大小超过限制")
	}
	if written == 0 {
		return nil, fmt.Errorf("下载内容为空")
	}

	// 关闭临时文件后再移动，确保内容已刷新
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("关闭临时文件失败: %w", err)
	}

	// 计算MD5哈希
	fileHash := hex.EncodeToString(hash.Sum(nil))

	// 移动临时文件到目标位置
	if err := os.Rename(tmpPath, filePath); err != nil {
		return nil, fmt.Errorf("保存文件失败: %w", err)
	}

	return &DownloadResult{
		FilePath: filePath,
		FileName: fileName,
		FileHash: fileHash,
	}, nil
}

// getExtensionFromURL 从URL获取文件扩展名
func getExtensionFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	ext := strings.ToLower(path.Ext(parsed.Path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
		return ext
	}
	return ""
}
