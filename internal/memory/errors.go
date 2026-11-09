package memory

import "errors"

var (
	ErrSnapshotChanged         = errors.New("原文或整理状态已变化，请重新读取")
	ErrConversationUnavailable = errors.New("当前会话已停用或拉黑")
	ErrMemoryVersion           = errors.New("记忆版本已变化，请重新读取")
	ErrMemoryNotEditable       = errors.New("记忆已归档或到期，不能再修改")
	ErrMemoryDuplicate         = errors.New("已有相同内容的记忆，请读取现有条目后再合并")
	ErrMemoryDeleteActive      = errors.New("只能永久删除已归档的记忆")
)
