// Package media 负责聊天媒体的本地缓存：普通图片归一化为 webp，表情包保留原件
package media

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mumu-bot/internal/utils"

	"github.com/disintegration/imaging"
	"github.com/kolesa-team/go-webp/encoder"
	"golang.org/x/image/webp"
)

// Ref 指向一份已缓存的媒体
type Ref struct {
	Hash      string `json:"hash"`
	Path      string `json:"path"`
	Mime      string `json:"mime"`
	Bytes     int64  `json:"bytes"`
	Sticker   bool   `json:"sticker"`
	Collected bool   `json:"collected"` // 表情包是否已在收藏库
	Index     int    `json:"index"`     // 在消息自身图片里的序号，合并转发里的图片为 -1
}

// Refs 有序媒体引用列表，落库为 jsonb
type Refs []Ref

// Value 把媒体引用写成 jsonb
func (r Refs) Value() (driver.Value, error) {
	if len(r) == 0 {
		return nil, nil
	}
	return json.Marshal(r)
}

// Scan 从 jsonb 读出媒体引用
func (r *Refs) Scan(src any) error {
	if src == nil {
		*r = nil
		return nil
	}
	var raw []byte
	switch value := src.(type) {
	case []byte:
		raw = value
	case string:
		raw = []byte(value)
	default:
		return fmt.Errorf("无法解析媒体引用: %T", src)
	}
	if len(raw) == 0 {
		*r = nil
		return nil
	}
	return json.Unmarshal(raw, r)
}

const (
	imageDir   = "image"
	stickerDir = "sticker"
	tempDir    = "tmp"

	webpMime = "image/webp"
)

// hashPattern 匹配内容 md5，OneBot 的图片段 file 字段形如 <md5>.png
var hashPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// Options 描述媒体缓存目录与归一化参数
type Options struct {
	Dir        string
	MaxBytesMB int
	Quality    float32
	NarrowEdge int
}

// Source 是一条消息里的媒体来源
type Source struct {
	URL     string
	File    string
	Sticker bool
}

// Manager 管理媒体缓存，目录内按图片与表情包分开存放
type Manager struct {
	opts Options
}

// New 创建媒体缓存管理器
func New(opts Options) *Manager {
	if strings.TrimSpace(opts.Dir) == "" {
		opts.Dir = "./media"
	}
	if opts.MaxBytesMB <= 0 {
		opts.MaxBytesMB = 10
	}
	if opts.Quality <= 0 {
		opts.Quality = 75
	}
	if opts.NarrowEdge <= 0 {
		opts.NarrowEdge = 1080
	}
	return &Manager{opts: opts}
}

// Ensure 下载并缓存媒体；同一内容只下载、编码和落盘一次
func (m *Manager) Ensure(ctx context.Context, src Source) (Ref, error) {
	url := strings.TrimSpace(src.URL)
	if url == "" {
		return Ref{}, fmt.Errorf("媒体地址为空")
	}
	// 事件里已带内容 md5 时可以先查缓存，命中就不必下载
	if hash := hashFromFile(src.File); hash != "" {
		if ref, ok := m.cached(hash, src.Sticker); ok {
			return ref, nil
		}
	}
	download, err := utils.DownloadImage(ctx, url, filepath.Join(m.opts.Dir, tempDir), m.opts.MaxBytesMB)
	if err != nil {
		return Ref{}, fmt.Errorf("下载媒体失败: %w", err)
	}
	hash := download.FileHash
	if ref, ok := m.cached(hash, src.Sticker); ok {
		_ = os.Remove(download.FilePath)
		return ref, nil
	}
	if src.Sticker {
		return m.storeSticker(hash, download.FilePath)
	}
	defer os.Remove(download.FilePath)
	return m.storeImage(hash, download.FilePath)
}

// cached 查缓存命中，普通图片落在 image 目录，表情包落在 sticker 目录
func (m *Manager) cached(hash string, sticker bool) (Ref, bool) {
	path := m.pathFor(hash, sticker)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return Ref{}, false
	}
	mime := webpMime
	if sticker {
		mime = sniffMime(path)
	}
	return Ref{Hash: hash, Path: path, Mime: mime, Bytes: info.Size(), Sticker: sticker}, true
}

// storeSticker 表情包原样落盘，不缩放也不转码
func (m *Manager) storeSticker(hash, tempPath string) (Ref, error) {
	target := m.pathFor(hash, true)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return Ref{}, fmt.Errorf("创建表情包目录失败: %w", err)
	}
	if _, err := os.Stat(target); err == nil {
		_ = os.Remove(tempPath)
		if ref, ok := m.cached(hash, true); ok {
			return ref, nil
		}
	}
	if err := os.Rename(tempPath, target); err != nil {
		return Ref{}, fmt.Errorf("保存表情包失败: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return Ref{}, fmt.Errorf("读取表情包信息失败: %w", err)
	}
	return Ref{Hash: hash, Path: target, Mime: sniffMime(target), Bytes: info.Size(), Sticker: true}, nil
}

// storeImage 普通图片按 EXIF 方向摆正，窄边缩到上限后转 webp
func (m *Manager) storeImage(hash, tempPath string) (Ref, error) {
	img, err := decodeImage(tempPath)
	if err != nil {
		return Ref{}, fmt.Errorf("解析图片失败: %w", err)
	}
	img = m.resize(img)

	target := m.pathFor(hash, false)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return Ref{}, fmt.Errorf("创建图片目录失败: %w", err)
	}
	opts, err := encoder.NewLossyEncoderOptions(encoder.PresetDefault, m.opts.Quality)
	if err != nil {
		return Ref{}, fmt.Errorf("初始化 webp 编码失败: %w", err)
	}
	enc, err := encoder.NewEncoder(img, opts)
	if err != nil {
		return Ref{}, fmt.Errorf("创建 webp 编码器失败: %w", err)
	}
	tempTarget := target + ".tmp"
	file, err := os.Create(tempTarget)
	if err != nil {
		return Ref{}, fmt.Errorf("创建临时文件失败: %w", err)
	}
	if err := enc.Encode(file); err != nil {
		_ = file.Close()
		_ = os.Remove(tempTarget)
		return Ref{}, fmt.Errorf("webp 编码失败: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempTarget)
		return Ref{}, fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tempTarget, target); err != nil {
		_ = os.Remove(tempTarget)
		return Ref{}, fmt.Errorf("保存图片失败: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return Ref{}, fmt.Errorf("读取图片信息失败: %w", err)
	}
	return Ref{Hash: hash, Path: target, Mime: webpMime, Bytes: info.Size()}, nil
}

// resize 窄边超过上限时等比缩放，窄边本来就小于上限时不处理
func (m *Manager) resize(img image.Image) image.Image {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return img
	}
	narrow := min(width, height)
	if narrow <= m.opts.NarrowEdge {
		return img
	}
	scale := float64(m.opts.NarrowEdge) / float64(narrow)
	return imaging.Resize(img, int(float64(width)*scale), int(float64(height)*scale), imaging.Lanczos)
}

// pathFor 缓存路径按类型分开，表情包不带扩展名，类型由文件内容判断
func (m *Manager) pathFor(hash string, sticker bool) string {
	if sticker {
		return filepath.Join(m.opts.Dir, stickerDir, hash)
	}
	return filepath.Join(m.opts.Dir, imageDir, hash+".webp")
}

// decodeImage 优先用 imaging 解码并按 EXIF 自动摆正，webp 走 x/image 解码
func decodeImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if img, err := imaging.Decode(file, imaging.AutoOrientation(true)); err == nil {
		return img, nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return webp.Decode(file)
}

// hashFromFile 从 OneBot 的 file 字段取内容 md5
func hashFromFile(file string) string {
	name := strings.TrimSpace(file)
	if name == "" {
		return ""
	}
	if idx := strings.Index(name, "."); idx > 0 {
		name = name[:idx]
	}
	if !hashPattern.MatchString(name) {
		return ""
	}
	return strings.ToLower(name)
}

// sniffMime 按文件内容判断媒体类型
func sniffMime(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer file.Close()
	buf := make([]byte, 512)
	n, _ := io.ReadFull(file, buf)
	switch http.DetectContentType(buf[:n]) {
	case "image/jpeg":
		return "image/jpeg"
	case "image/png":
		return "image/png"
	case "image/gif":
		return "image/gif"
	case "image/webp":
		return "image/webp"
	case "image/bmp":
		return "image/bmp"
	default:
		return "application/octet-stream"
	}
}
