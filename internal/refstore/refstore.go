// Package refstore 保存 file_ref/image_ref 的不可变字节快照与小型元数据
package refstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"mumu-bot/internal/media"
)

const (
	StateReady   = "ready"
	StatePending = "remote_pending"
	imageMaxSize = 10 * 1024 * 1024
)

var refTokenPattern = regexp.MustCompile(`(?:file_|image_)[0-9a-f]{32}`)

// ExtractRefs 从正文中提取 file_ref/image_ref token
func ExtractRefs(text string) []string {
	return refTokenPattern.FindAllString(text, -1)
}

var (
	// ErrNotFound 表示 ref 元数据不存在
	ErrNotFound = errors.New("文件引用不存在或已回收")
	// ErrNotReady 表示 QQ 附件尚未下载固定
	ErrNotReady = errors.New("文件引用尚未保存，需要先取得真实字节")
	// ErrUnavailable 表示快照字节缺失或损坏
	ErrUnavailable = errors.New("文件快照不可用")
)

// Locator 是 QQ 附件的真实定位信息，只在 pending 首次固定前使用
type Locator struct {
	ConversationKind string `json:"conversation_kind"`
	TargetID         int64  `json:"target_id"`
	FileID           string `json:"file_id"`
	FileHash         string `json:"file_hash,omitempty"`
	BusID            *int64 `json:"busid,omitempty"`
	Name             string `json:"name,omitempty"`
	Size             *int64 `json:"size,omitempty"`
}

// Ref 是一份 file_ref/image_ref 的元数据；ID 本身不带前缀
type Ref struct {
	FormatVersion int       `json:"format_version"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	State         string    `json:"state"`
	Size          int64     `json:"size,omitempty"`
	SHA256        string    `json:"sha256,omitempty"`
	MIME          string    `json:"mime,omitempty"`
	Image         bool      `json:"image,omitempty"`
	Sticker       bool      `json:"sticker,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	Locator       *Locator  `json:"locator,omitempty"`
}

// FileRef 返回普通文件视图
func (r Ref) FileRef() string { return "file_" + r.ID }

// ImageRef 返回图片视图；非图片返回空
func (r Ref) ImageRef() string {
	if !r.Image {
		return ""
	}
	return "image_" + r.ID
}

// Store 是 MediaDir 下的小型快照仓库；不依赖数据库或 worker
type Store struct {
	root string
	mu   sync.Mutex
	gcMu sync.Mutex
	once map[string]*sync.Mutex
}

func New(mediaDir string) *Store {
	root := strings.TrimSpace(mediaDir)
	if root == "" {
		root = "./media"
	}
	return &Store{root: root, once: map[string]*sync.Mutex{}}
}

func (s *Store) refsDir() string  { return filepath.Join(s.root, "files", "refs") }
func (s *Store) blobsDir() string { return filepath.Join(s.root, "files", "blobs") }
func (s *Store) tempDir() string  { return filepath.Join(s.root, "files", "tmp") }

func (s *Store) init() error {
	return os.MkdirAll(s.refsDir(), 0o755)
}

func (s *Store) blobPath(hash string) string { return filepath.Join(s.blobsDir(), hash) }
func (s *Store) metaPath(id string) string   { return filepath.Join(s.refsDir(), id+".json") }

// Parse 把 file_/image_ token 解析成底层 ID
func Parse(token string) (string, bool, error) {
	value := strings.TrimSpace(token)
	var id string
	var image bool
	switch {
	case strings.HasPrefix(value, "file_"):
		id = strings.TrimPrefix(value, "file_")
	case strings.HasPrefix(value, "image_"):
		id = strings.TrimPrefix(value, "image_")
		image = true
	default:
		return "", false, fmt.Errorf("文件引用格式无效")
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id) {
		return "", false, fmt.Errorf("文件引用格式无效")
	}
	return id, image, nil
}

func newID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(raw[:])
}

// CreatePending 登记一个尚未下载的 QQ 附件
func (s *Store) CreatePending(name string, locator Locator) (*Ref, error) {
	if strings.TrimSpace(locator.FileID) == "" {
		return nil, fmt.Errorf("QQ 文件缺少 file_id")
	}
	if err := s.init(); err != nil {
		return nil, err
	}
	id := newID()
	if id == "" {
		return nil, fmt.Errorf("生成文件引用失败")
	}
	ref := &Ref{
		FormatVersion: 1, ID: id, Name: safeName(name), State: StatePending,
		CreatedAt: time.Now().UTC(), Locator: &locator,
	}
	if locator.Size != nil && *locator.Size > 0 {
		ref.Size = *locator.Size
	}
	if err := s.writeMeta(ref); err != nil {
		return nil, err
	}
	return ref, nil
}

// CreateReady 从 reader 固定一份不可变快照，同 hash 复用 blob
func (s *Store) CreateReady(ctx context.Context, name string, reader io.Reader, maxBytes int64, locator *Locator) (*Ref, error) {
	return s.createReady(ctx, name, reader, maxBytes, 0, locator)
}

// CreateReadyWithSize 在发布前核对调用方声明的字节数，避免半成品进入 ready
func (s *Store) CreateReadyWithSize(ctx context.Context, name string, reader io.Reader, maxBytes, declaredSize int64, locator *Locator) (*Ref, error) {
	return s.createReady(ctx, name, reader, maxBytes, declaredSize, locator)
}

func (s *Store) createReady(ctx context.Context, name string, reader io.Reader, maxBytes, declaredSize int64, locator *Locator) (*Ref, error) {
	if reader == nil {
		return nil, fmt.Errorf("文件内容为空")
	}
	if maxBytes <= 0 {
		maxBytes = 32 * 1024 * 1024
	}
	if err := s.init(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.tempDir(), 0o755); err != nil {
		return nil, err
	}
	temp, err := os.CreateTemp(s.tempDir(), "snapshot-*.tmp")
	if err != nil {
		return nil, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(reader, maxBytes+1))
	closeErr := temp.Close()
	if copyErr != nil || closeErr != nil {
		return nil, fmt.Errorf("保存文件快照失败")
	}
	if written > maxBytes {
		return nil, fmt.Errorf("文件超过 %d 字节", maxBytes)
	}
	if written == 0 {
		return nil, fmt.Errorf("文件内容为空")
	}
	if declaredSize > 0 && written != declaredSize {
		return nil, fmt.Errorf("文件长度不符: 声明 %d 字节，实际 %d 字节", declaredSize, written)
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	s.gcMu.Lock()
	defer s.gcMu.Unlock()
	if err := s.publishBlob(sum, tempPath); err != nil {
		return nil, err
	}
	mimeType, err := detectBlobMIME(s.blobPath(sum))
	if err != nil {
		return nil, err
	}
	id := newID()
	if id == "" {
		return nil, fmt.Errorf("生成文件引用失败")
	}
	ref := &Ref{
		FormatVersion: 1, ID: id, Name: safeName(name), State: StateReady,
		Size: written, SHA256: sum, MIME: mimeType, Image: viewableImage(mimeType) && written <= imageMaxSize,
		CreatedAt: time.Now().UTC(), Locator: locator,
	}
	if err := s.writeMeta(ref); err != nil {
		return nil, err
	}
	return ref, nil
}

// CreateReadyFromFile 从 worker/媒体卷中的真实文件固定快照
func (s *Store) CreateReadyFromFile(ctx context.Context, name, filePath string, maxBytes int64, locator *Locator) (*Ref, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return s.CreateReady(ctx, name, file, maxBytes, locator)
}

// EnsureReady 在首次使用时把 pending ref 固定；同一 ref 只允许一次成功发布
func (s *Store) EnsureReady(ctx context.Context, token string, fetch func(context.Context, Locator) (io.ReadCloser, error)) (*Ref, error) {
	id, _, err := Parse(token)
	if err != nil {
		return nil, err
	}
	lock := s.idLock(id)
	lock.Lock()
	defer lock.Unlock()
	ref, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if ref.State == StateReady {
		return ref, nil
	}
	if ref.Locator == nil || fetch == nil {
		return nil, ErrNotReady
	}
	body, err := fetch(ctx, *ref.Locator)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	if err := s.init(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.tempDir(), 0o755); err != nil {
		return nil, err
	}
	temp, err := os.CreateTemp(s.tempDir(), "pending-*.tmp")
	if err != nil {
		return nil, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(body, 32*1024*1024+1))
	closeErr := temp.Close()
	if copyErr != nil || closeErr != nil {
		return nil, fmt.Errorf("下载文件失败")
	}
	if written > 32*1024*1024 {
		return nil, fmt.Errorf("文件超过 32 MiB")
	}
	if written == 0 {
		return nil, fmt.Errorf("下载内容为空")
	}
	if ref.Locator.Size != nil && *ref.Locator.Size > 0 && written != *ref.Locator.Size {
		return nil, fmt.Errorf("下载文件大小不符: 声明 %d 字节，实际 %d 字节", *ref.Locator.Size, written)
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	s.gcMu.Lock()
	defer s.gcMu.Unlock()
	if err := s.publishBlob(sum, tempPath); err != nil {
		return nil, err
	}
	mimeType, err := detectBlobMIME(s.blobPath(sum))
	if err != nil {
		return nil, err
	}
	ref.State = StateReady
	ref.Size = written
	ref.SHA256 = sum
	ref.MIME = mimeType
	ref.Image = viewableImage(mimeType) && written <= imageMaxSize
	if ref.Name == "" && ref.Locator.Name != "" {
		ref.Name = safeName(ref.Locator.Name)
	}
	if err := s.writeMeta(ref); err != nil {
		return nil, err
	}
	return ref, nil
}

// MarkSticker 标记这份快照来自表情包，供 saveSticker 做来源校验
func (s *Store) MarkSticker(token string) error {
	id, _, err := Parse(token)
	if err != nil {
		return err
	}
	lock := s.idLock(id)
	lock.Lock()
	defer lock.Unlock()
	ref, err := s.get(id)
	if err != nil {
		return err
	}
	ref.Sticker = true
	return s.writeMeta(ref)
}

// Get 读取元数据
func (s *Store) Get(token string) (*Ref, error) {
	id, _, err := Parse(token)
	if err != nil {
		return nil, err
	}
	return s.get(id)
}

func (s *Store) get(id string) (*Ref, error) {
	data, err := os.ReadFile(s.metaPath(id))
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var ref Ref
	if err := json.Unmarshal(data, &ref); err != nil || ref.ID != id {
		return nil, ErrUnavailable
	}
	return &ref, nil
}

// Open 打开 ready 快照
func (s *Store) Open(token string) (*Ref, *os.File, error) {
	ref, err := s.Get(token)
	if err != nil {
		return nil, nil, err
	}
	if ref.State != StateReady || ref.SHA256 == "" {
		return ref, nil, ErrNotReady
	}
	file, err := os.Open(s.blobPath(ref.SHA256))
	if os.IsNotExist(err) {
		return ref, nil, ErrUnavailable
	}
	if err != nil {
		return ref, nil, err
	}
	return ref, file, nil
}

// ReadText 按 Unicode 字符窗口读取 UTF-8 文本
func (s *Store) ReadText(token string, offset, length int) (*Ref, string, int, int, int, bool, error) {
	ref, file, err := s.Open(token)
	if err != nil {
		return ref, "", 0, 0, 0, false, err
	}
	defer file.Close()
	if ref.Size > 1024*1024 {
		return ref, "", 0, 0, 0, false, fmt.Errorf("文本文件超过 1 MiB，请交给 worker 解析")
	}
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil {
		return ref, "", 0, 0, 0, false, err
	}
	if !utf8.Valid(data) {
		return ref, "", 0, 0, 0, true, nil
	}
	runes := []rune(string(data))
	if offset < 0 {
		offset = 0
	}
	if offset > len(runes) {
		offset = len(runes)
	}
	if length <= 0 {
		length = 4000
	}
	if length > 8000 {
		length = 8000
	}
	end := min(offset+length, len(runes))
	next := 0
	if end < len(runes) {
		next = end
	}
	return ref, string(runes[offset:end]), offset, next, len(runes), false, nil
}

// CleanupCandidates 只释放本批已知脱根候选，用于删除 transcript 后的即时回收
func (s *Store) publishBlob(hash, tempPath string) error {
	if err := os.MkdirAll(s.blobsDir(), 0o755); err != nil {
		return err
	}
	target := s.blobPath(hash)
	if _, err := os.Stat(target); err == nil {
		return nil
	}
	if err := os.Rename(tempPath, target); err != nil {
		// 临时文件已 rename，后续调用方可能仍想读取；复制失败保留真实错误
		return err
	}
	return nil
}

func (s *Store) writeMeta(ref *Ref) error {
	raw, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(s.refsDir(), ".meta-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.metaPath(ref.ID))
}

func (s *Store) idLock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.once == nil {
		s.once = map[string]*sync.Mutex{}
	}
	if s.once[id] == nil {
		s.once[id] = &sync.Mutex{}
	}
	return s.once[id]
}

func safeName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = filepath.Base(name)
	if name == "." || name == "/" || name == "" {
		return "file"
	}
	return name
}

// detectBlobMIME 只读文件头探测类型，避免为识别把大文件整份读进内存
func detectBlobMIME(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	head := make([]byte, 512)
	n, readErr := file.Read(head)
	if readErr != nil && readErr != io.EOF {
		return "", readErr
	}
	if imageMime := media.ImageMIME(head[:n]); imageMime != "" {
		return imageMime, nil
	}
	return http.DetectContentType(head[:n]), nil
}

func viewableImage(mime string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0])) {
	case "image/png", "image/jpeg", "image/webp", "image/gif", "image/bmp":
		return true
	default:
		return false
	}
}
