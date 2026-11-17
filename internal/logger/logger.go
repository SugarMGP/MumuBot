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

type logEntry struct {
	seq  uint64
	raw  string
	once sync.Once
	line Line
	ok   bool
}

func (e *logEntry) value() (Line, bool) {
	e.once.Do(func() {
		var fields map[string]any
		if err := logJSONAPI.UnmarshalFromString(e.raw, &fields); err != nil {
			return
		}
		e.line = formatLogLine(fields)
		e.line.Seq = e.seq
		e.ok = true
	})
	return e.line, e.ok
}

type logBuffer struct {
	mu      sync.RWMutex
	lines   []*logEntry
	written uint64
	max     int
}

func newLogBuffer(max int) *logBuffer { return &logBuffer{max: max} }

func (b *logBuffer) Write(p []byte) (int, error) {
	raw := strings.TrimSpace(string(p))
	if raw == "" {
		return len(p), nil
	}
	b.mu.Lock()
	b.written++
	b.lines = append(b.lines, &logEntry{seq: b.written, raw: raw})
	if len(b.lines) > b.max {
		b.lines = b.lines[len(b.lines)-b.max:]
	}
	b.mu.Unlock()
	return len(p), nil
}

func (b *logBuffer) Sync() error { return nil }

// snapshotEntries 返回当前缓冲条目的只读副本和绝对序号范围
func (b *logBuffer) snapshotEntries() ([]*logEntry, uint64, uint64) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	lines := make([]*logEntry, len(b.lines))
	copy(lines, b.lines)
	first := b.written + 1
	if len(lines) > 0 {
		first = lines[0].seq
	}
	return lines, first, b.written
}

var recentLogs = newLogBuffer(300)
var logJSONAPI = sonic.Config{UseNumber: true, SortMapKeys: true}.Froze()

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

	// 控制台与后台日志页共享同一条日志流，内存缓冲有界，重启后自然清空
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
	FirstSeq uint64
	LastSeq  uint64
}

type Line struct {
	Seq     uint64
	Time    string
	Level   string
	Message string
	Fields  []Field
	Text    string
}

type Field struct {
	Name           string
	Value          string
	FormattedValue string
}

func Query(keyword, minimumLevel string) QueryResult {
	return QueryAfter(keyword, minimumLevel, 0)
}

// QueryAfter 只返回序号大于 afterSeq 的匹配日志，同时给出整个缓冲区的计数和序号范围
func QueryAfter(keyword, minimumLevel string, afterSeq uint64) QueryResult {
	entries, firstSeq, lastSeq := recentLogs.snapshotEntries()
	result := QueryResult{Total: len(entries), Lines: make([]Line, 0, len(entries)), FirstSeq: firstSeq, LastSeq: lastSeq}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	minimum := zapcore.DebugLevel
	if err := minimum.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(minimumLevel)))); err != nil {
		minimum = zapcore.DebugLevel
	}
	for _, entry := range entries {
		line, ok := entry.value()
		if !ok {
			continue
		}
		var level zapcore.Level
		if level.UnmarshalText([]byte(strings.ToLower(line.Level))) != nil || level < minimum {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(line.Text), keyword) {
			continue
		}
		result.Filtered++
		if line.Seq > afterSeq {
			result.Lines = append(result.Lines, line)
		}
	}
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
		value := logFieldText(fields[key])
		formatted := readableLogValue(fields[key])
		if formatted == value {
			formatted = ""
		}
		logFields = append(logFields, Field{Name: key, Value: value, FormattedValue: formatted})
		fieldParts = append(fieldParts, key+"="+value)
	}
	fieldText := strings.Join(fieldParts, " ")
	text := strings.Join([]string{timeText, levelText, message}, " ")
	if fieldText != "" {
		text += " " + fieldText
	}
	return Line{Time: timeText, Level: levelText, Message: message, Fields: logFields, Text: text}
}
