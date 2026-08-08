package logger

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type logBuffer struct {
	mu    sync.RWMutex
	lines []string
	max   int
}

func newLogBuffer(max int) *logBuffer { return &logBuffer{max: max} }

func (b *logBuffer) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if line == "" {
		return len(p), nil
	}
	b.mu.Lock()
	b.lines = append(b.lines, line)
	if len(b.lines) > b.max {
		b.lines = b.lines[len(b.lines)-b.max:]
	}
	b.mu.Unlock()
	return len(p), nil
}

func (b *logBuffer) Sync() error { return nil }

func (b *logBuffer) Snapshot() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]string, len(b.lines))
	copy(result, b.lines)
	return result
}

var recentLogs = newLogBuffer(300)

// Init 初始化日志系统
func Init(level string, debug bool) {
	// 解析日志级别
	var zapLevel zapcore.Level
	switch level {
	case "debug":
		zapLevel = zapcore.DebugLevel
	case "info":
		zapLevel = zapcore.InfoLevel
	case "warn":
		zapLevel = zapcore.WarnLevel
	case "error":
		zapLevel = zapcore.ErrorLevel
	default:
		if debug {
			zapLevel = zapcore.DebugLevel
		} else {
			zapLevel = zapcore.InfoLevel
		}
	}

	// 编码器配置
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalColorLevelEncoder,
		EncodeTime:     zapcore.TimeEncoderOfLayout("15:04:05"),
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// 控制台与后台日志页共享同一条日志流，内存缓冲有界，重启后自然清空。
	consoleEncoder := zapcore.NewConsoleEncoder(encoderConfig)
	bufferConfig := encoderConfig
	bufferConfig.EncodeLevel = zapcore.LowercaseLevelEncoder
	bufferConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	bufferEncoder := zapcore.NewJSONEncoder(bufferConfig)
	consoleCore := zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), zapLevel)
	bufferCore := zapcore.NewCore(bufferEncoder, recentLogs, zapLevel)
	zap.ReplaceGlobals(zap.New(zapcore.NewTee(consoleCore, bufferCore)))
}

type QueryResult struct {
	Lines    []Line
	Total    int
	Filtered int
}

type Line struct {
	Time    string
	Level   string
	Message string
	Fields  []Field
	Text    string
}

type Field struct {
	Name  string
	Value string
}

func Query(keyword, minimumLevel string) QueryResult {
	raw := recentLogs.Snapshot()
	result := QueryResult{Total: len(raw), Lines: make([]Line, 0, len(raw))}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	minimum := zapcore.DebugLevel
	if err := minimum.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(minimumLevel)))); err != nil {
		minimum = zapcore.DebugLevel
	}
	for _, line := range raw {
		var fields map[string]any
		if err := sonic.UnmarshalString(line, &fields); err != nil {
			continue
		}
		levelText, _ := fields["level"].(string)
		var level zapcore.Level
		if level.UnmarshalText([]byte(levelText)) != nil || level < minimum {
			continue
		}
		formatted := formatLogLine(fields)
		if keyword != "" && !strings.Contains(strings.ToLower(formatted.Text), keyword) {
			continue
		}
		result.Lines = append(result.Lines, formatted)
	}
	result.Filtered = len(result.Lines)
	return result
}

func formatLogLine(fields map[string]any) Line {
	timeText := fmt.Sprint(fields["time"])
	levelText := strings.ToUpper(fmt.Sprint(fields["level"]))
	message := fmt.Sprint(fields["msg"])
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if key != "time" && key != "level" && key != "msg" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	logFields := make([]Field, 0, len(keys))
	fieldParts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := fmt.Sprint(fields[key])
		logFields = append(logFields, Field{Name: key, Value: value})
		fieldParts = append(fieldParts, key+"="+value)
	}
	fieldText := strings.Join(fieldParts, " ")
	text := strings.Join([]string{timeText, levelText, message}, " ")
	if fieldText != "" {
		text += " " + fieldText
	}
	return Line{Time: timeText, Level: levelText, Message: message, Fields: logFields, Text: text}
}
