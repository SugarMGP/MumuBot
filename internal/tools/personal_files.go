package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"mumu-bot/internal/workerproto"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type ListFilesInput struct {
	Path   string `json:"path,omitempty" jsonschema:"description=相对个人目录的路径，默认 ."`
	Offset int    `json:"offset,omitempty" jsonschema:"description=从第几个条目开始，默认 0"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=本页最多返回多少个条目，默认 50，最大 200"`
}

type FileEntryView struct {
	Path       string    `json:"path"`
	Type       string    `json:"type"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

type ListFilesOutput struct {
	Success    bool            `json:"success"`
	Path       string          `json:"path"`
	Entries    []FileEntryView `json:"entries"`
	NextOffset int             `json:"next_offset,omitempty"`
}

func listPersonalFiles(ctx context.Context, input *ListFilesInput) (*ListFilesOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.PersonalFiles == nil {
		return nil, NewTerminalToolError(fmt.Errorf("个人文件能力未初始化"))
	}
	if input == nil {
		input = &ListFilesInput{}
	}
	page, err := tc.PersonalFiles.ListFiles(ctx, workerproto.FileListRequest{
		Path: input.Path, Offset: input.Offset, Limit: input.Limit,
	})
	if err != nil {
		return nil, err
	}
	entries := make([]FileEntryView, 0, len(page.Entries))
	for _, item := range page.Entries {
		kind := "file"
		if item.IsDir {
			kind = "dir"
		}
		entries = append(entries, FileEntryView{Path: item.Path, Type: kind, Size: item.Size, ModifiedAt: item.ModifiedAt})
	}
	listPath := input.Path
	if strings.TrimSpace(listPath) == "" {
		listPath = "."
	}
	return &ListFilesOutput{Success: true, Path: listPath, Entries: entries, NextOffset: page.NextOffset}, nil
}

type ReadFileInput struct {
	Path    string `json:"path,omitempty" jsonschema:"description=相对个人目录的文本路径，与 file_ref 二选一"`
	FileRef string `json:"file_ref,omitempty" jsonschema:"description=file_ref 或 image_ref，与 path 二选一"`
	Offset  int    `json:"offset,omitempty" jsonschema:"description=从第几个 Unicode 字符开始，默认 0"`
	Length  int    `json:"length,omitempty" jsonschema:"description=本段最多读取多少字符，默认 4000，最大 8000"`
}

type ReadFileOutput struct {
	Success    bool   `json:"success"`
	Path       string `json:"path,omitempty"`
	FileRef    string `json:"file_ref,omitempty"`
	ImageRef   string `json:"image_ref,omitempty"`
	Name       string `json:"name,omitempty"`
	Content    string `json:"content"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset,omitempty"`
	TotalChars int    `json:"total_chars"`
	Size       int64  `json:"size"`
	Binary     bool   `json:"binary,omitempty"`
	Message    string `json:"message,omitempty"`
}

func readPersonalFile(ctx context.Context, input *ReadFileInput) (*ReadFileOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.PersonalFiles == nil {
		return nil, NewTerminalToolError(fmt.Errorf("个人文件能力未初始化"))
	}
	if input == nil || boolCount(strings.TrimSpace(input.Path) != "", strings.TrimSpace(input.FileRef) != "") != 1 {
		return nil, fmt.Errorf("path 与 file_ref 必须且只能提供一个")
	}
	if strings.TrimSpace(input.FileRef) != "" {
		if _, err := ensureReadyRef(ctx, tc, input.FileRef); err != nil {
			return nil, err
		}
		ref, content, offset, next, total, binary, err := tc.MemoryMgr.RefStore().ReadText(input.FileRef, input.Offset, input.Length)
		if err != nil {
			return nil, err
		}
		if binary {
			return &ReadFileOutput{Success: false, FileRef: ref.FileRef(), ImageRef: ref.ImageRef(), Name: ref.Name, Size: ref.Size, Binary: true,
				Message: "这不是 UTF-8 文本文件，请交给 worker 解析"}, nil
		}
		return &ReadFileOutput{Success: true, FileRef: ref.FileRef(), ImageRef: ref.ImageRef(), Name: ref.Name, Content: content,
			Offset: offset, NextOffset: next, TotalChars: total, Size: ref.Size}, nil
	}
	result, err := tc.PersonalFiles.ReadFile(ctx, workerproto.FileReadRequest{
		Path: input.Path, Offset: input.Offset, Length: input.Length,
	})
	if err != nil {
		return nil, err
	}
	if result.Binary {
		return &ReadFileOutput{
			Success: false, Path: result.Path, Size: result.Size, Binary: true,
			Message: "这不是 UTF-8 文本文件，请交给 worker 解析",
		}, nil
	}
	return &ReadFileOutput{
		Success: true, Path: result.Path, Content: result.Content,
		Offset: result.Offset, NextOffset: result.NextOffset, TotalChars: result.TotalChars, Size: result.Size,
	}, nil
}

type WriteFileInput struct {
	Path    string `json:"path" jsonschema:"description=相对个人目录的文件路径"`
	Content string `json:"content" jsonschema:"description=要写入的 UTF-8 文本"`
	Mode    string `json:"mode,omitempty" jsonschema:"description=create（默认，存在即报错）、append 或 replace"`
}

type WriteFileOutput struct {
	Success bool   `json:"success"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
}

func writePersonalFile(ctx context.Context, input *WriteFileInput) (*WriteFileOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.PersonalFiles == nil {
		return nil, NewTerminalToolError(fmt.Errorf("个人文件能力未初始化"))
	}
	if input == nil || strings.TrimSpace(input.Path) == "" {
		return nil, fmt.Errorf("请提供文件路径")
	}
	if !utf8.ValidString(input.Content) {
		return nil, fmt.Errorf("写入内容必须是 UTF-8 文本")
	}
	result, err := tc.PersonalFiles.WriteFile(ctx, workerproto.FileWriteRequest{
		Path: input.Path, Content: input.Content, Mode: input.Mode,
	})
	if err != nil {
		return nil, err
	}
	return &WriteFileOutput{Success: true, Path: result.Path, Size: result.Size}, nil
}

type SaveFileInput struct {
	Path      string `json:"path" jsonschema:"description=保存到个人目录的相对路径"`
	URL       string `json:"url,omitempty" jsonschema:"description=HTTP 或 HTTPS 下载地址，与 file_ref 二选一"`
	FileRef   string `json:"file_ref,omitempty" jsonschema:"description=要导入个人目录的 ref，与 url 二选一"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"description=目标已存在时是否明确替换，默认 false"`
}

type SaveFileOutput struct {
	Success     bool   `json:"success"`
	Path        string `json:"path"`
	FileRef     string `json:"file_ref,omitempty"`
	ImageRef    string `json:"image_ref,omitempty"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
}

func savePersonalFile(ctx context.Context, input *SaveFileInput) (*SaveFileOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.PersonalFiles == nil || tc.SnapshotFiles == nil {
		return nil, NewTerminalToolError(fmt.Errorf("个人文件能力未初始化"))
	}
	if input == nil || strings.TrimSpace(input.Path) == "" {
		return nil, fmt.Errorf("请提供保存路径")
	}
	hasURL := strings.TrimSpace(input.URL) != ""
	hasRef := strings.TrimSpace(input.FileRef) != ""
	if hasURL == hasRef {
		return nil, fmt.Errorf("url 与 file_ref 必须且只能提供一个")
	}
	if hasRef {
		if _, err := ensureReadyRef(ctx, tc, input.FileRef); err != nil {
			return nil, err
		}
		if err := tc.SnapshotFiles.ImportSnapshot(ctx, input.FileRef, input.Path, input.Overwrite); err != nil {
			return nil, err
		}
		ref, err := tc.MemoryMgr.RefStore().Get(input.FileRef)
		if err != nil {
			return nil, err
		}
		return &SaveFileOutput{Success: true, Path: input.Path, FileRef: ref.FileRef(), ImageRef: ref.ImageRef(), Size: ref.Size, ContentType: ref.MIME}, nil
	}
	result, err := tc.PersonalFiles.SaveFile(ctx, workerproto.FileSaveRequest{
		Path: input.Path, URL: input.URL, Overwrite: input.Overwrite,
	})
	if err != nil {
		return nil, err
	}
	return &SaveFileOutput{Success: true, Path: result.Path, Size: result.Size, ContentType: result.ContentType}, nil
}

type CreateFileRefInput struct {
	Path string `json:"path" jsonschema:"description=个人目录中的真实文件路径"`
}

type CreateFileRefOutput struct {
	Success  bool   `json:"success"`
	FileRef  string `json:"file_ref"`
	ImageRef string `json:"image_ref,omitempty"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	State    string `json:"state"`
}

func createFileRef(ctx context.Context, input *CreateFileRefInput) (*CreateFileRefOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.SnapshotFiles == nil {
		return nil, NewTerminalToolError(fmt.Errorf("文件快照能力未初始化"))
	}
	if input == nil || strings.TrimSpace(input.Path) == "" {
		return nil, fmt.Errorf("请提供个人目录中的文件路径")
	}
	ref, err := tc.SnapshotFiles.CreateSnapshot(ctx, input.Path, 32*1024*1024)
	if err != nil {
		return nil, err
	}
	return &CreateFileRefOutput{
		Success: true, FileRef: ref.FileRef(), ImageRef: ref.ImageRef(), Name: ref.Name, Size: ref.Size, State: ref.State,
	}, nil
}

func NewCreateFileRefTool() (tool.InvokableTool, error) {
	return utils.InferTool("createFileRef", "把个人目录中的真实文件冻结成不可变 file_ref；图片额外给出 image_ref，不修改原文件。", createFileRef)
}

func NewListFilesTool() (tool.InvokableTool, error) {
	return utils.InferTool("listFiles", "列出个人目录当前一层的内容，不递归。path 默认 .，offset 从 0 开始，limit 默认 50、最大 200。", listPersonalFiles)
}

func NewReadFileTool() (tool.InvokableTool, error) {
	return utils.InferTool("readFile", "读取个人目录或 file_ref 中的 UTF-8 文本窗口，默认 4000、最大 8000；path 与 file_ref 恰选一个。", readPersonalFile)
}

func NewWriteFileTool() (tool.InvokableTool, error) {
	return utils.InferTool("writeFile", "向个人目录写 UTF-8 文本，mode 为 create（默认，存在即报错）、append 或 replace，成功后返回真实路径和大小。", writePersonalFile)
}

func NewSaveFileTool() (tool.InvokableTool, error) {
	return utils.InferTool("saveFile", "把 HTTP(S) 文件或一条真实 file_ref 流式保存到个人目录；url 与 file_ref 必须且只能提供一个，overwrite 默认 false。", savePersonalFile)
}

type WorkerSaveFileInput struct {
	Path      string `json:"path" jsonschema:"description=保存到个人目录的相对路径"`
	FileRef   string `json:"file_ref" jsonschema:"description=要导入的 file_ref 或 image_ref"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"description=目标已存在时是否明确替换，默认 false"`
}

type WorkerSaveFileOutput struct {
	Success  bool   `json:"success"`
	Path     string `json:"path"`
	FileRef  string `json:"file_ref,omitempty"`
	ImageRef string `json:"image_ref,omitempty"`
	Size     int64  `json:"size"`
}

func workerSaveFile(ctx context.Context, input *WorkerSaveFileInput) (*WorkerSaveFileOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.SnapshotFiles == nil || tc.MemoryMgr == nil || input == nil || strings.TrimSpace(input.Path) == "" || strings.TrimSpace(input.FileRef) == "" {
		return nil, fmt.Errorf("请提供 file_ref 和保存路径")
	}
	if _, err := ensureReadyRef(ctx, tc, input.FileRef); err != nil {
		return nil, err
	}
	if err := tc.SnapshotFiles.ImportSnapshot(ctx, input.FileRef, input.Path, input.Overwrite); err != nil {
		return nil, err
	}
	ref, err := tc.MemoryMgr.RefStore().Get(input.FileRef)
	if err != nil {
		return nil, err
	}
	return &WorkerSaveFileOutput{Success: true, Path: input.Path, FileRef: ref.FileRef(), ImageRef: ref.ImageRef(), Size: ref.Size}, nil
}

func NewWorkerSaveFileTool() (tool.InvokableTool, error) {
	return utils.InferTool("saveFile", "把 file_ref/image_ref 的真实字节导入个人目录，生成普通可编辑副本；不访问 QQ。", workerSaveFile)
}
