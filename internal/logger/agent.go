package logger

import (
	"context"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
)

type agentLogKey struct{}
type agentLog struct {
	log     *zap.Logger
	content bool
}

// WithSession 给模型、工具和摘要共用的日志附加一次运行的来源
func WithSession(ctx context.Context, kind string, targetID int64, runID string, content bool) context.Context {
	log := zap.L().With(zap.String("session", fmt.Sprintf("%s:%d", kind, targetID)), zap.String("run_id", runID))
	return context.WithValue(ctx, agentLogKey{}, agentLog{log: log, content: content})
}

// FromContext 返回当前运行的日志来源，非会话调用沿用全局日志
func FromContext(ctx context.Context) *zap.Logger {
	if scope, ok := ctx.Value(agentLogKey{}).(agentLog); ok {
		return scope.log
	}
	return zap.L()
}

// ContentEnabled 只控制正文，运行状态始终记录
func ContentEnabled(ctx context.Context) bool {
	if scope, ok := ctx.Value(agentLogKey{}).(agentLog); ok {
		return scope.content
	}
	return true
}

// Payload 记录完整文本和媒体元信息，不改变真正传给模型的消息
func Payload(value any) string {
	var decoded any
	if text, ok := value.(string); ok {
		if !sonic.ValidString(text) {
			return text
		}
		if err := logJSONAPI.UnmarshalFromString(text, &decoded); err != nil {
			return text
		}
	} else {
		raw, err := sonic.Marshal(value)
		if err != nil {
			return fmt.Sprintf("[日志内容编码失败: %v]", err)
		}
		if err := logJSONAPI.Unmarshal(raw, &decoded); err != nil {
			return fmt.Sprintf("[日志内容解析失败: %v]", err)
		}
	}
	stripBinary(decoded, "")
	raw, err := sonic.MarshalString(decoded)
	if err != nil {
		return fmt.Sprintf("[日志内容编码失败: %v]", err)
	}
	return raw
}

func stripBinary(value any, field string) {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if text, ok := item.(string); ok && (key == "base64data" || key == "base64_data" || (key == "signature" && field == "assistant_gen_reasoning") || key == "encrypted_content" || ((key == "url" || key == "image_url") && strings.HasPrefix(text, "data:"))) {
				value[key] = fmt.Sprintf("[二进制/加密内容，%d 字符]", len(text))
				continue
			}
			stripBinary(item, key)
		}
	case []any:
		for _, item := range value {
			stripBinary(item, field)
		}
	}
}
