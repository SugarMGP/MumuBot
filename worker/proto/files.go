package workerproto

import "time"

const (
	MaxFileBytes      = 32 * 1024 * 1024
	MaxTextFileBytes  = 1 * 1024 * 1024
	MaxReadChars      = 8000
	DefaultReadChars  = 4000
	MaxWriteChars     = 1 * 1024 * 1024
	MaxImageBytes     = 10 * 1024 * 1024
	MaxListLimit      = 200
	DefaultListLimit  = 50
	MaxFilePathLength = 512
)

type FileEntry struct {
	Path       string    `json:"path"`
	IsDir      bool      `json:"is_dir"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

type FileListRequest struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type FileListResponse struct {
	Entries    []FileEntry `json:"entries"`
	NextOffset int         `json:"next_offset,omitempty"`
}

type FileReadRequest struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Length int    `json:"length,omitempty"`
}

type FileReadResponse struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset,omitempty"`
	TotalChars int    `json:"total_chars"`
	Size       int64  `json:"size"`
	Binary     bool   `json:"binary,omitempty"`
}

type FileWriteRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    string `json:"mode"`
}

type FileWriteResponse struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type FileSaveRequest struct {
	Path      string `json:"path"`
	URL       string `json:"url"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

type FileSaveResponse struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
}
