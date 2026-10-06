package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/bytedance/sonic"
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

// Config MCP 配置文件结构
type Config struct {
	Servers []ServerConfig `json:"servers"`
}

// Manager MCP 客户端管理器
type Manager struct {
	clients  []*client.Client
	tools    []tool.BaseTool
	readOnly map[string]bool
	mu       sync.RWMutex
}

// NewMCPManager 创建 MCP 管理器
func NewMCPManager() *Manager {
	return &Manager{readOnly: make(map[string]bool)}
}

// LoadFromConfig 从配置文件加载 MCP 服务器
func (m *Manager) LoadFromConfig(ctx context.Context, configPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			zap.L().Debug("MCP 配置文件不存在，跳过加载", zap.String("path", configPath))
			return nil
		}
		return fmt.Errorf("读取 MCP 配置文件失败: %w", err)
	}

	var cfg Config
	if err := sonic.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("解析 MCP 配置文件失败: %w", err)
	}

	if ctx == nil {
		ctx = context.Background()
	}

	var loadErrors []error
	for _, serverCfg := range cfg.Servers {
		if !serverCfg.Enabled {
			zap.L().Debug("MCP 服务器已禁用，跳过", zap.String("name", serverCfg.Name))
			continue
		}

		if err := m.connectServer(ctx, &serverCfg); err != nil {
			loadErrors = append(loadErrors, fmt.Errorf("加载 MCP 服务器 %q 失败: %w", serverCfg.Name, err))
			zap.L().Warn("连接 MCP 服务器失败",
				zap.String("name", serverCfg.Name),
				zap.Error(err))
			continue
		}

		zap.L().Info("已连接 MCP 服务器", zap.String("name", serverCfg.Name))
	}

	return errors.Join(loadErrors...)
}

// connectServer 连接单个 MCP 服务器
func (m *Manager) connectServer(ctx context.Context, cfg *ServerConfig) error {
	var cli *client.Client
	var err error

	switch cfg.Type {
	case "sse":
		cli, err = client.NewSSEMCPClient(cfg.URL)
		if err != nil {
			return fmt.Errorf("创建 SSE 客户端失败: %w", err)
		}
	case "stdio":
		cli, err = client.NewStdioMCPClient(cfg.Command, cfg.Env, cfg.Args...)
		if err != nil {
			return fmt.Errorf("创建 Stdio 客户端失败: %w", err)
		}
	default:
		return fmt.Errorf("不支持的 MCP 服务器类型: %s", cfg.Type)
	}

	// 启动客户端
	if err := cli.Start(ctx); err != nil {
		_ = cli.Close()
		return fmt.Errorf("启动 MCP 客户端失败: %w", err)
	}

	// 初始化连接
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mumu-bot",
		Version: "2.0.0",
	}

	if _, err := cli.Initialize(ctx, initRequest); err != nil {
		_ = cli.Close()
		return fmt.Errorf("初始化 MCP 连接失败: %w", err)
	}

	// 获取工具 - 使用 MCPClient 接口
	mcpToolCfg := &mcptool.Config{
		Cli:           cli,
		ToolNameList:  cfg.ToolNameList,
		CustomHeaders: cfg.CustomHeaders,
	}

	baseTools, err := mcptool.GetTools(ctx, mcpToolCfg)
	if err != nil {
		_ = cli.Close()
		return fmt.Errorf("获取 MCP 工具失败: %w", err)
	}

	renamed, err := renameMCPTools(ctx, baseTools, cfg.Name, m.tools)
	if err != nil {
		_ = cli.Close()
		return err
	}
	readOnly := listReadOnlyTools(ctx, cli, cfg)
	m.clients = append(m.clients, cli)
	m.tools = append(m.tools, renamed...)
	for name, ro := range readOnly {
		m.readOnly[name] = ro
	}

	zap.L().Info("已加载 MCP 工具",
		zap.String("server", cfg.Name),
		zap.Int("tool_count", len(baseTools)))

	return nil
}

// listReadOnlyTools 读取 MCP 自带的只读标注；未标注或读取失败按可能有副作用处理
func listReadOnlyTools(ctx context.Context, cli *client.Client, cfg *ServerConfig) map[string]bool {
	result := make(map[string]bool)
	header := http.Header{}
	for k, v := range cfg.CustomHeaders {
		header.Set(k, v)
	}
	list, err := cli.ListTools(ctx, mcp.ListToolsRequest{Header: header})
	if err != nil {
		zap.L().Warn("读取 MCP 工具只读标注失败，按可能有副作用处理", zap.String("server", cfg.Name), zap.Error(err))
		return result
	}
	allowed := make(map[string]struct{}, len(cfg.ToolNameList))
	for _, name := range cfg.ToolNameList {
		allowed[name] = struct{}{}
	}
	for _, item := range list.Tools {
		if len(allowed) > 0 {
			if _, ok := allowed[item.Name]; !ok {
				continue
			}
		}
		hint := item.Annotations.ReadOnlyHint
		result[mcpToolName(cfg.Name, item.Name)] = hint != nil && *hint
	}
	return result
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

// GetTools 获取所有MCP工具
func (m *Manager) GetTools() []tool.BaseTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tools
}

// ReadOnlyTools 返回带只读标注的 MCP 工具名；其余工具在失败轮次按可能有副作用处理
func (m *Manager) ReadOnlyTools() map[string]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]bool, len(m.readOnly))
	for name, ro := range m.readOnly {
		result[name] = ro
	}
	return result
}

// Close 关闭所有MCP连接
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, cli := range m.clients {
		if err := cli.Close(); err != nil {
			zap.L().Warn("关闭 MCP 客户端失败", zap.Error(err))
		}
	}

	m.clients = nil
	m.tools = nil
}
