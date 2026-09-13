package memory

import (
	"errors"
	"fmt"
)

// ErrSnapshotChanged 表示必须重新调查，不能重试旧快照
var ErrSnapshotChanged = errors.New("原文或整理状态已变化，请在后续批次重新调查")

// ValidationError 表示模型可以通过修改提交参数解决的错误
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalidKnowledge(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}
