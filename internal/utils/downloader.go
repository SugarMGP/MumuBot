package utils

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DownloadResult 下载结果
type DownloadResult struct {
	FilePath string // 本地文件完整路径
	FileName string // 文件名（uuid.ext）
	FileHash string // 文件MD5哈希
	FileSize int64  // 文件大小
}

// DownloadImage 下载图片到指定目录
// url: 图片URL
// storageDir: 存储目录
// maxSizeMB: 最大文件大小限制（MB），0表示不限制
func DownloadImage(url string, storageDir string, maxSizeMB int) (*DownloadResult, error) {
	// 确保存储目录存在
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		return nil, fmt.Errorf("创建存储目录失败: %w", err)
	}

	// 创建HTTP客户端
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// 发起请求
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("下载图片失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载图片失败: HTTP %d", resp.StatusCode)
	}

	// 检查文件大小
	if maxSizeMB > 0 && resp.ContentLength > int64(maxSizeMB)*1024*1024 {
		return nil, fmt.Errorf("文件大小超过限制: %d MB > %d MB", resp.ContentLength/1024/1024, maxSizeMB)
	}

	// 获取文件扩展名
	ext := getExtensionFromURL(url)
	if ext == "" {
		ext = getExtensionFromContentType(resp.Header.Get("Content-Type"))
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
		tmpFile.Close()
		os.Remove(tmpPath) // 清理临时文件
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

	// 关闭临时文件
	tmpFile.Close()

	// 计算MD5哈希
	fileHash := hex.EncodeToString(hash.Sum(nil))

	// 移动临时文件到目标位置
	if err := os.Rename(tmpPath, filePath); err != nil {
		// 如果重命名失败（可能跨文件系统），尝试复制
		if err := copyFile(tmpPath, filePath); err != nil {
			return nil, fmt.Errorf("保存文件失败: %w", err)
		}
	}

	return &DownloadResult{
		FilePath: filePath,
		FileName: fileName,
		FileHash: fileHash,
		FileSize: written,
	}, nil
}

// getExtensionFromURL 从URL获取文件扩展名
func getExtensionFromURL(url string) string {
	// 移除查询参数
	if idx := strings.Index(url, "?"); idx > 0 {
		url = url[:idx]
	}

	// 获取扩展名
	ext := filepath.Ext(url)
	ext = strings.ToLower(ext)

	// 验证是否是有效的图片扩展名
	validExts := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true,
	}
	if validExts[ext] {
		return ext
	}
	return ""
}

// getExtensionFromContentType 从Content-Type获取扩展名
func getExtensionFromContentType(contentType string) string {
	switch {
	case strings.Contains(contentType, "jpeg"):
		return ".jpg"
	case strings.Contains(contentType, "png"):
		return ".png"
	case strings.Contains(contentType, "gif"):
		return ".gif"
	case strings.Contains(contentType, "webp"):
		return ".webp"
	case strings.Contains(contentType, "bmp"):
		return ".bmp"
	default:
		return ""
	}
}

// copyFile 复制文件
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// CalculateFileHash 计算文件的MD5哈希
func CalculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
