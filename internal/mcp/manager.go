package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"mumu-bot/internal/refstore"

	"go.uber.org/zap"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcptool "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// ServerConfig MCP 服务器配置
type ServerConfig struct {
	Name          string            `json:"name"`
	Enabled       bool              `json:"enabled"`
	Type          string            `json:"type"`           // sse 或 stdio
	URL           string            `json:"url"`            // SSE 服务器 URL
	Command       string            `json:"command"`        // stdio 命令
	Args          []string          `json:"args"`           // stdio 参数
	Env           []string          `json:"env"`            // stdio 环境变量
	ToolNameList  []string          `json:"tool_name_list"` // 可选，指定要加载的工具名称列表
	CustomHeaders map[string]string `json:"custom_headers"` // 可选，自定义 HTTP 头
}

// ServerStatus 是单个 MCP 服务的真实加载状态
type ServerStatus struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
	ToolCount int    `json:"tool_count"`
	ErrorCode string `json:"error_code,omitempty"`
}

// Manager MCP 客户端管理器
type Manager struct {
	toolsets []*mcptool.ToolSet
	tools    []tool.Tool
	toolSet  map[string]bool
	refs     *refstore.Store
	statuses []ServerStatus
	mu       sync.RWMutex
}

// NewMCPManager 创建 MCP 管理器
func NewMCPManager(refs *refstore.Store) *Manager {
	return &Manager{refs: refs, toolSet: map[string]bool{}}
}

// Load 连接指定的 MCP 服务器；单个失败只让该服务不可用，不阻断其它能力
func (m *Manager) Load(ctx context.Context, servers []ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	const initTimeout = 30 * time.Second
	usedNames := map[string]int{}
	for _, server := range servers {
		if !server.Enabled {
			continue
		}
		status := ServerStatus{Name: server.Name, Enabled: true}
		if server.Type != "sse" && server.Type != "stdio" {
			status.ErrorCode = "connect_failed"
			m.statuses = append(m.statuses, status)
			continue
		}
		initCtx, cancel := context.WithTimeout(ctx, initTimeout)
		// 函数名只允许安全字符，服务名先规范化并保证唯一，避免模型侧参数校验失败
		setName := sanitizeName(server.Name)
		usedNames[setName]++
		if usedNames[setName] > 1 {
			setName = fmt.Sprintf("%s-%d", setName, usedNames[setName])
		}
		toolset := mcptool.NewMCPToolSet(connectionConfig(server), mcptool.WithName(setName))
		err := toolset.Init(initCtx)
		if err != nil {
			cancel()
			_ = toolset.Close()
			status.ErrorCode = "connect_failed"
			m.statuses = append(m.statuses, status)
			zap.L().Warn("连接 MCP 服务器失败", zap.String("name", server.Name), zap.Error(err))
			continue
		}
		items := toolset.Tools(initCtx)
		cancel()
		items = filterTools(ctx, items, setName, server.ToolNameList)
		m.toolsets = append(m.toolsets, toolset)
		for _, item := range items {
			m.tools = append(m.tools, item)
			m.toolSet[item.Declaration().Name] = true
		}
		status.Connected = true
		status.ToolCount = len(items)
		m.statuses = append(m.statuses, status)
		zap.L().Info("已连接 MCP 服务器", zap.String("name", server.Name), zap.Int("tools", len(items)))
	}
	return nil
}

func connectionConfig(server ServerConfig) mcptool.ConnectionConfig {
	config := mcptool.ConnectionConfig{Transport: server.Type, Timeout: 30 * time.Second}
	switch server.Type {
	case "sse":
		config.ServerURL = server.URL
		config.Headers = server.CustomHeaders
	case "stdio":
		command, args := server.Command, server.Args
		// trpc toolset 的 stdio 配置不携带环境变量，用 env 前缀保留原有能力
		if len(server.Env) > 0 {
			args = append(append(append([]string{}, server.Env...), command), server.Args...)
			command = "env"
		}
		config.Command = command
		config.Args = args
	}
	return config
}

func filterTools(ctx context.Context, items []tool.Tool, server string, names []string) []tool.Tool {
	if len(names) == 0 {
		return items
	}
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[strings.TrimSpace(name)] = true
	}
	filtered := make([]tool.Tool, 0, len(items))
	for _, item := range items {
		declaration := item.Declaration()
		if declaration == nil {
			continue
		}
		name := strings.TrimPrefix(declaration.Name, server+"_")
		if allowed[name] || allowed[declaration.Name] {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func sanitizeName(raw string) string {
	var builder strings.Builder
	for _, item := range strings.TrimSpace(raw) {
		switch {
		case item >= 'a' && item <= 'z', item >= 'A' && item <= 'Z', item >= '0' && item <= '9', item == '_':
			builder.WriteRune(item)
		default:
			builder.WriteRune('-')
		}
	}
	name := strings.Trim(builder.String(), "-")
	if name == "" {
		return "unknown"
	}
	return name
}

// IsTool 判断工具名是否来自已加载的 MCP 服务
func (m *Manager) IsTool(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.toolSet[name]
}

// SanitizeResult 把 MCP 图片结果固定为 image_ref，避免 Base64 挤进文本上下文
func (m *Manager) SanitizeResult(ctx context.Context, name string, raw any) (any, error) {
	if m == nil || m.refs == nil || !m.IsTool(name) {
		return raw, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return raw, nil
	}
	var items []map[string]any
	if json.Unmarshal(encoded, &items) != nil {
		return raw, nil
	}
	changed := false
	for index, item := range items {
		if item["type"] != "image" {
			continue
		}
		data, _ := item["data"].(string)
		reader := base64.NewDecoder(base64.StdEncoding, strings.NewReader(data))
		ref, refErr := m.refs.CreateReady(ctx, name, reader, 10*1024*1024, nil)
		text := ""
		switch {
		case refErr != nil:
			text = fmt.Sprintf("图片保存失败：%v", refErr)
		case !ref.Image:
			text = "图片保存失败：返回内容不是支持的位图"
		default:
			text = "图片：" + ref.ImageRef() + "，可用 viewImage 按需查看"
		}
		items[index] = map[string]any{"type": "text", "text": text}
		changed = true
	}
	if !changed {
		return raw, nil
	}
	return items, nil
}

// Statuses 返回当前加载状态
func (m *Manager) Statuses() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]ServerStatus(nil), m.statuses...)
}

// GetTools 返回已加载的工具
func (m *Manager) GetTools() []tool.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]tool.Tool(nil), m.tools...)
}

// Close 关闭所有 MCP 连接
func (m *Manager) Close() {
	if m == nil {
		return
	}
	for _, toolset := range m.toolsets {
		_ = toolset.Close()
	}
}
