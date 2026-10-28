package memory

import "errors"

var (
	ErrSnapshotChanged         = errors.New("原文或整理状态已变化，请重新读取")
	ErrConversationUnavailable = errors.New("当前会话已停用或拉黑")
)
