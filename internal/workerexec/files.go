package workerexec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mumu-bot/internal/workerproto"
)

func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req workerproto.FileListRequest
	if err := decodeFileRequest(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: err.Error()})
		return
	}
	resp, err := s.listFiles(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleFileRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req workerproto.FileReadRequest
	if err := decodeFileRequest(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: err.Error()})
		return
	}
	resp, err := s.readFile(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req workerproto.FileWriteRequest
	if err := decodeFileRequest(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: err.Error()})
		return
	}
	resp, err := s.writeFile(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleFileSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req workerproto.FileSaveRequest
	if err := decodeFileRequest(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: err.Error()})
		return
	}
	resp, err := s.saveFile(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		s.handleFileImport(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel, err := cleanFilePath(r.URL.Query().Get("path"))
	if err != nil || rel == "." {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	file, err := s.root.Open(rel)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "not a regular file", http.StatusBadRequest)
		return
	}
	if info.Size() > workerproto.MaxFileBytes {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	head := make([]byte, 512)
	n, readErr := file.Read(head)
	if readErr != nil && readErr != io.EOF {
		http.Error(w, "read failed", http.StatusInternalServerError)
		return
	}
	if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
		http.Error(w, "read failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(head[:n]))
	w.Header().Set("X-Mumu-File-Size", strconv.FormatInt(info.Size(), 10))
	name := strings.NewReplacer("\r", "", "\n", "").Replace(path.Base(rel))
	w.Header().Set("X-Mumu-File-Name", name)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	if _, copyErr := io.Copy(w, io.LimitReader(file, workerproto.MaxFileBytes+1)); copyErr != nil {
		// 响应头已经发出，无法再改状态；客户端会因长度不足发现截断
		return
	}
}

func decodeFileRequest(w http.ResponseWriter, r *http.Request, target any) error {
	body := http.MaxBytesReader(w, r.Body, 2*1024*1024)
	return json.NewDecoder(body).Decode(target)
}

// handleFileImport 接收主服务快照的原始字节并发布到 home
func (s *Server) handleFileImport(w http.ResponseWriter, r *http.Request) {
	rel, err := cleanFilePath(r.URL.Query().Get("path"))
	if err != nil || rel == "." {
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: "保存路径无效"})
		return
	}
	overwrite := r.URL.Query().Get("overwrite") == "true"
	if err := s.root.MkdirAll(path.Dir(rel), 0o755); err != nil {
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: "创建父目录失败"})
		return
	}
	if !overwrite {
		if _, statErr := s.root.Stat(rel); statErr == nil {
			writeJSON(w, http.StatusConflict, workerproto.ErrorResponse{Error: "目标文件已存在"})
			return
		}
	}
	temp := path.Join(path.Dir(rel), ".mumu-import-"+randomSuffix())
	file, createErr := s.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if createErr != nil {
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: "创建临时文件失败"})
		return
	}
	written, copyErr := io.Copy(file, io.LimitReader(r.Body, workerproto.MaxFileBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > workerproto.MaxFileBytes || (r.ContentLength >= 0 && written != r.ContentLength) {
		_ = s.root.Remove(temp)
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: "写入文件失败、长度不符或超过 32 MiB"})
		return
	}
	if !overwrite {
		if _, statErr := s.root.Stat(rel); statErr == nil {
			_ = s.root.Remove(temp)
			writeJSON(w, http.StatusConflict, workerproto.ErrorResponse{Error: "目标文件已存在"})
			return
		}
	}
	if err := s.root.Rename(temp, rel); err != nil {
		_ = s.root.Remove(temp)
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: "发布文件失败"})
		return
	}
	writeJSON(w, http.StatusOK, workerproto.FileWriteResponse{Path: rel, Size: written})
}

// cleanFilePath 只接受 home 内的相对路径，绝对路径、上跳和 NUL 直接拒绝
func cleanFilePath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = "."
	}
	if len(value) > workerproto.MaxFilePathLength || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("路径无效")
	}
	value = strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(value, "/") || (len(value) >= 2 && value[1] == ':') {
		return "", fmt.Errorf("路径必须相对个人目录")
	}
	clean := path.Clean(value)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("路径不能离开个人目录")
	}
	return clean, nil
}

func (s *Server) listFiles(req workerproto.FileListRequest) (*workerproto.FileListResponse, error) {
	rel, err := cleanFilePath(req.Path)
	if err != nil {
		return nil, err
	}
	dir, err := s.root.Open(rel)
	if err != nil {
		return nil, fmt.Errorf("打开目录失败: %w", err)
	}
	defer dir.Close()
	items, err := dir.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败: %w", err)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Name() < items[j].Name() })
	all := make([]workerproto.FileEntry, 0, len(items))
	for _, item := range items {
		entryPath := item.Name()
		if rel != "." {
			entryPath = path.Join(rel, item.Name())
		}
		info, statErr := s.root.Stat(entryPath)
		if statErr != nil {
			continue
		}
		all = append(all, workerproto.FileEntry{
			Path: entryPath, IsDir: info.IsDir(), Size: info.Size(), ModifiedAt: info.ModTime().UTC(),
		})
	}
	offset := max(req.Offset, 0)
	limit := req.Limit
	if limit <= 0 {
		limit = workerproto.DefaultListLimit
	}
	limit = min(limit, workerproto.MaxListLimit)
	if offset >= len(all) {
		return &workerproto.FileListResponse{Entries: []workerproto.FileEntry{}}, nil
	}
	end := min(offset+limit, len(all))
	resp := &workerproto.FileListResponse{Entries: all[offset:end]}
	if end < len(all) {
		resp.NextOffset = end
	}
	return resp, nil
}

func (s *Server) readFile(req workerproto.FileReadRequest) (*workerproto.FileReadResponse, error) {
	rel, err := cleanFilePath(req.Path)
	if err != nil || rel == "." {
		return nil, fmt.Errorf("文件路径无效")
	}
	info, err := s.root.Stat(rel)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("文件不存在或不是普通文件")
	}
	if info.Size() > workerproto.MaxTextFileBytes {
		return nil, fmt.Errorf("文本文件超过 1 MiB，请交给 worker 解析")
	}
	data, err := s.root.ReadFile(rel)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}
	resp := &workerproto.FileReadResponse{Path: rel, Size: int64(len(data))}
	if !utf8.Valid(data) {
		resp.Binary = true
		return resp, nil
	}
	runes := []rune(string(data))
	total := len(runes)
	offset := min(max(req.Offset, 0), total)
	length := req.Length
	if length <= 0 {
		length = workerproto.DefaultReadChars
	}
	length = min(length, workerproto.MaxReadChars)
	end := min(offset+length, total)
	resp.Content = string(runes[offset:end])
	resp.Offset = offset
	resp.TotalChars = total
	if end < total {
		resp.NextOffset = end
	}
	return resp, nil
}

func (s *Server) writeFile(req workerproto.FileWriteRequest) (*workerproto.FileWriteResponse, error) {
	rel, err := cleanFilePath(req.Path)
	if err != nil || rel == "." || strings.HasSuffix(rel, "/") {
		return nil, fmt.Errorf("文件路径无效")
	}
	if !utf8.ValidString(req.Content) {
		return nil, fmt.Errorf("写入内容必须是 UTF-8 文本")
	}
	if len(req.Content) > workerproto.MaxWriteChars {
		return nil, fmt.Errorf("写入内容超过 1 MiB")
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "create"
	}
	if err := s.root.MkdirAll(path.Dir(rel), 0o755); err != nil {
		return nil, fmt.Errorf("创建父目录失败: %w", err)
	}
	switch mode {
	case "create":
		file, openErr := s.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if openErr != nil {
			return nil, fmt.Errorf("文件已存在或无法创建: %w", openErr)
		}
		if _, writeErr := file.WriteString(req.Content); writeErr != nil {
			file.Close()
			_ = s.root.Remove(rel)
			return nil, fmt.Errorf("写入文件失败: %w", writeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return nil, fmt.Errorf("关闭文件失败: %w", closeErr)
		}
		return &workerproto.FileWriteResponse{Path: rel, Size: int64(len(req.Content))}, nil
	case "append", "replace":
		existing := ""
		if mode == "append" {
			if info, statErr := s.root.Stat(rel); statErr == nil && info.Mode().IsRegular() {
				if info.Size() > workerproto.MaxWriteChars {
					return nil, fmt.Errorf("追加前文件已超过 1 MiB")
				}
				data, readErr := s.root.ReadFile(rel)
				if readErr != nil {
					return nil, fmt.Errorf("读取原文件失败: %w", readErr)
				}
				existing = string(data)
			}
		}
		combined := existing + req.Content
		if len(combined) > workerproto.MaxWriteChars {
			return nil, fmt.Errorf("写入后文件超过 1 MiB")
		}
		temp := path.Join(path.Dir(rel), ".mumu-write-"+randomSuffix())
		file, createErr := s.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if createErr != nil {
			return nil, fmt.Errorf("创建临时文件失败: %w", createErr)
		}
		if _, writeErr := file.WriteString(combined); writeErr != nil {
			file.Close()
			_ = s.root.Remove(temp)
			return nil, fmt.Errorf("写入临时文件失败: %w", writeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			_ = s.root.Remove(temp)
			return nil, fmt.Errorf("关闭临时文件失败: %w", closeErr)
		}
		if renameErr := s.root.Rename(temp, rel); renameErr != nil {
			_ = s.root.Remove(temp)
			return nil, fmt.Errorf("替换文件失败: %w", renameErr)
		}
		return &workerproto.FileWriteResponse{Path: rel, Size: int64(len(combined))}, nil
	default:
		return nil, fmt.Errorf("写入模式无效")
	}
}

func (s *Server) saveFile(ctx context.Context, req workerproto.FileSaveRequest) (*workerproto.FileSaveResponse, error) {
	rel, err := cleanFilePath(req.Path)
	if err != nil || rel == "." {
		return nil, fmt.Errorf("保存路径无效")
	}
	if _, statErr := s.root.Stat(rel); statErr == nil && !req.Overwrite {
		return nil, fmt.Errorf("目标文件已存在")
	}
	parsed, err := url.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("下载地址必须是 HTTP 或 HTTPS")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("构造下载请求失败: %w", err)
	}
	client := &http.Client{Timeout: 120 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("下载返回 HTTP %d", response.StatusCode)
	}
	if err := s.root.MkdirAll(path.Dir(rel), 0o755); err != nil {
		return nil, fmt.Errorf("创建父目录失败: %w", err)
	}
	temp := path.Join(path.Dir(rel), ".mumu-save-"+randomSuffix())
	file, createErr := s.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if createErr != nil {
		return nil, fmt.Errorf("创建临时文件失败: %w", createErr)
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, workerproto.MaxFileBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > workerproto.MaxFileBytes {
		_ = s.root.Remove(temp)
		if written > workerproto.MaxFileBytes {
			return nil, fmt.Errorf("文件超过 32 MiB")
		}
		return nil, fmt.Errorf("保存文件失败")
	}
	if !req.Overwrite {
		if _, statErr := s.root.Stat(rel); statErr == nil {
			_ = s.root.Remove(temp)
			return nil, fmt.Errorf("目标文件已存在")
		}
	}
	if err := s.root.Rename(temp, rel); err != nil {
		_ = s.root.Remove(temp)
		return nil, fmt.Errorf("保存文件失败: %w", err)
	}
	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return &workerproto.FileSaveResponse{Path: rel, Size: written, ContentType: contentType}, nil
}

func randomSuffix() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(raw[:])
}
