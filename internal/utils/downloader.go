package utils

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// DownloadResult 下载结果
type DownloadResult struct {
	FilePath string // 下载临时文件完整路径；最终媒体由 media 按内容哈希落盘
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

	// 下载先落唯一临时文件；最终媒体文件由 media 按内容哈希存放
	tmpFile, err := os.CreateTemp(storageDir, "download_*.tmp")
	if err != nil {
		return nil, fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmpFile.Name()
	success := false
	defer func() {
		_ = tmpFile.Close()
		if !success {
			_ = os.Remove(tmpPath)
		}
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

	// 关闭临时文件后再计算哈希
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("关闭临时文件失败: %w", err)
	}

	// 计算 MD5；临时文件由调用方消费或删除，最终媒体目录使用该哈希命名
	fileHash := hex.EncodeToString(hash.Sum(nil))
	success = true
	return &DownloadResult{
		FilePath: tmpPath,
		FileHash: fileHash,
	}, nil
}
