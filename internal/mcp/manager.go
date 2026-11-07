package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"mumu-bot/internal/refstore"
	"mumu-bot/internal/version"

	mcptool "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
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
	Error     string `json:"error,omitempty"`
}

// Manager MCP 客户端管理器
type Manager struct {
	clients  []*client.Client
	cancels  []context.CancelFunc
	refs     *refstore.Store
	tools    []tool.BaseTool
	statuses []ServerStatus
	mu       sync.RWMutex
}

// NewMCPManager 创建 MCP 管理器
func NewMCPManager(refs *refstore.Store) *Manager {
	return &Manager{refs: refs}
}

// Load 连接指定的 MCP 服务器；单个失败只让该服务不可用，不阻断其它能力
func (m *Manager) Load(ctx context.Context, servers []ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}

	for _, serverCfg := range servers {
		if !serverCfg.Enabled {
			zap.L().Debug("MCP 服务器已禁用，跳过", zap.String("name", serverCfg.Name))
			continue
		}
		toolCount, err := m.connectServer(ctx, &serverCfg)
		if err != nil {
			m.statuses = append(m.statuses, ServerStatus{Name: serverCfg.Name, Enabled: true, Error: err.Error()})
			zap.L().Warn("连接 MCP 服务器失败", zap.String("name", serverCfg.Name), zap.Error(err))
			continue
		}
		m.statuses = append(m.statuses, ServerStatus{Name: serverCfg.Name, Enabled: true, Connected: true, ToolCount: toolCount})
		zap.L().Info("已连接 MCP 服务器", zap.String("name", serverCfg.Name))
	}

	return nil
}

// connectServer 连接单个 MCP 服务器
func (m *Manager) connectServer(ctx context.Context, cfg *ServerConfig) (int, error) {
	var cli *client.Client
	var err error
	const initTimeout = 30 * time.Second
	connectionCtx, cancelConnection := context.WithCancel(ctx)
	initCtx, cancelInit := context.WithTimeout(ctx, initTimeout)
	defer cancelInit()
	stopInitWatch := context.AfterFunc(initCtx, cancelConnection)
	defer stopInitWatch()
	connected := false
	defer func() {
		if !connected {
			cancelConnection()
		}
	}()

	switch cfg.Type {
	case "sse":
		cli, err = client.NewSSEMCPClient(cfg.URL, client.WithHeaders(cfg.CustomHeaders))
		if err != nil {
			return 0, fmt.Errorf("创建 SSE 客户端失败: %w", err)
		}
	case "stdio":
		cli, err = client.NewStdioMCPClient(cfg.Command, cfg.Env, cfg.Args...)
		if err != nil {
			return 0, fmt.Errorf("创建 Stdio 客户端失败: %w", err)
		}
	default:
		return 0, fmt.Errorf("不支持的 MCP 服务器类型: %s", cfg.Type)
	}

	// 启动客户端
	if err := cli.Start(connectionCtx); err != nil {
		_ = cli.Close()
		return 0, fmt.Errorf("启动 MCP 客户端失败: %w", err)
	}

	// 初始化连接
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mumu-bot",
		Version: version.String(),
	}

	if _, err := cli.Initialize(initCtx, initRequest); err != nil {
		_ = cli.Close()
		return 0, fmt.Errorf("初始化 MCP 连接失败: %w", err)
	}

	// 获取工具 - 使用 MCPClient 接口
	mcpToolCfg := &mcptool.Config{
		Cli:                   cli,
		ToolNameList:          cfg.ToolNameList,
		ToolCallResultHandler: m.saveResultImages,
	}

	baseTools, err := mcptool.GetTools(initCtx, mcpToolCfg)
	if err != nil {
		_ = cli.Close()
		return 0, fmt.Errorf("获取 MCP 工具失败: %w", err)
	}

	renamed, err := renameMCPTools(ctx, baseTools, cfg.Name, m.tools)
	if err != nil {
		_ = cli.Close()
		return 0, err
	}
	m.clients = append(m.clients, cli)
	m.cancels = append(m.cancels, cancelConnection)
	m.tools = append(m.tools, renamed...)
	connected = true

	zap.L().Info("已加载 MCP 工具",
		zap.String("server", cfg.Name),
		zap.Int("tool_count", len(baseTools)))

	return len(renamed), nil
}

// renamedTool 把 MCP 工具改名成 mcp-服务名-工具名，调用能力原样透传
// 所有 MCP 工具均进入保留的 mcp- 命名空间，与内置工具的原名隔离
type renamedTool struct {
	tool.InvokableTool
	name string
}

func (t renamedTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := t.InvokableTool.Info(ctx)
	if err != nil || info == nil {
		return info, err
	}
	cloned := *info
	cloned.Name = t.name
	return &cloned, nil
}

// renameMCPTools 规范化后检查所有已加载工具的名字，拒绝重名及未能进入命名空间的工具
func renameMCPTools(ctx context.Context, items []tool.BaseTool, server string, existing []tool.BaseTool) ([]tool.BaseTool, error) {
	names := make(map[string]struct{}, len(existing)+len(items))
	for _, item := range existing {
		info, err := item.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("读取已加载工具名称失败: %w", err)
		}
		if info == nil || info.Name == "" {
			return nil, fmt.Errorf("已加载工具名称为空")
		}
		if _, ok := names[info.Name]; ok {
			return nil, fmt.Errorf("已加载工具重名: %q", info.Name)
		}
		names[info.Name] = struct{}{}
	}
	out := make([]tool.BaseTool, 0, len(items))
	for _, item := range items {
		if item == nil {
			return nil, fmt.Errorf("MCP 服务器 %q 返回空工具", server)
		}
		info, err := item.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("读取 MCP 服务器 %q 的工具名称失败: %w", server, err)
		}
		if info == nil || strings.TrimSpace(info.Name) == "" {
			return nil, fmt.Errorf("MCP 服务器 %q 返回的工具名称为空", server)
		}
		invokable, ok := item.(tool.InvokableTool)
		if !ok {
			return nil, fmt.Errorf("MCP 工具 %q 不可调用", info.Name)
		}
		name := mcpToolName(server, info.Name)
		if _, exists := names[name]; exists {
			return nil, fmt.Errorf("MCP 工具规范化后重名 %q（服务器 %q，原工具 %q）", name, server, info.Name)
		}
		names[name] = struct{}{}
		out = append(out, renamedTool{InvokableTool: invokable, name: name})
	}
	return out, nil
}

// mcpToolName 生成 mcp-服务名-工具名，非法字符统一替换成连字符
func mcpToolName(server string, toolName string) string {
	return "mcp-" + sanitizeMCPName(server) + "-" + sanitizeMCPName(toolName)
}

func sanitizeMCPName(raw string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	trimmed := strings.Trim(b.String(), "-")
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}

// Statuses 返回本次加载的逐个服务实际状态
func (m *Manager) Statuses() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]ServerStatus(nil), m.statuses...)
}

// GetTools 获取所有MCP工具
func (m *Manager) GetTools() []tool.BaseTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tools
}

// Close 关闭所有MCP连接
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cancel := range m.cancels {
		cancel()
	}

	for _, cli := range m.clients {
		if err := cli.Close(); err != nil {
			zap.L().Warn("关闭 MCP 客户端失败", zap.Error(err))
		}
	}

	m.clients = nil
	m.cancels = nil
	m.tools = nil
	m.statuses = nil
}
