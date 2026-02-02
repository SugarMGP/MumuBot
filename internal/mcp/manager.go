package mcp

import (
	"amu-bot/internal/tools"
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	mcptool "github.com/cloudwego/eino-ext/components/tool/mcp"
)

// MCPServerConfig MCP服务器配置
type MCPServerConfig struct {
	Name          string            `json:"name"`
	Enabled       bool              `json:"enabled"`
	Type          string            `json:"type"`           // sse 或 stdio
	URL           string            `json:"url"`            // SSE服务器URL
	Command       string            `json:"command"`        // stdio命令
	Args          []string          `json:"args"`           // stdio参数
	Env           []string          `json:"env"`            // stdio环境变量
	ToolNameList  []string          `json:"tool_name_list"` // 可选，指定要加载的工具名称列表
	CustomHeaders map[string]string `json:"custom_headers"` // 可选，自定义HTTP头
}

// MCPConfig MCP配置文件结构
type MCPConfig struct {
	Servers []MCPServerConfig `json:"servers"`
}

// MCPManager MCP客户端管理器
type MCPManager struct {
	clients []*client.Client
	tools   []tool.BaseTool
	mu      sync.Mutex
}

// NewMCPManager 创建MCP管理器
func NewMCPManager() *MCPManager {
	return &MCPManager{
		clients: make([]*client.Client, 0),
		tools:   make([]tool.BaseTool, 0),
	}
}

// LoadFromConfig 从配置文件加载MCP服务器
func (m *MCPManager) LoadFromConfig(configPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			zap.L().Debug("MCP配置文件不存在，跳过加载", zap.String("path", configPath))
			return nil
		}
		return fmt.Errorf("读取MCP配置文件失败: %w", err)
	}

	var cfg MCPConfig
	if err := sonic.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("解析MCP配置文件失败: %w", err)
	}

	ctx := context.Background()

	for _, serverCfg := range cfg.Servers {
		if !serverCfg.Enabled {
			zap.L().Debug("MCP服务器已禁用，跳过", zap.String("name", serverCfg.Name))
			continue
		}

		if err := m.connectServer(ctx, &serverCfg); err != nil {
			zap.L().Warn("连接MCP服务器失败",
				zap.String("name", serverCfg.Name),
				zap.Error(err))
			continue
		}

		zap.L().Info("已连接MCP服务器", zap.String("name", serverCfg.Name))
	}

	return nil
}

// connectServer 连接单个MCP服务器
func (m *MCPManager) connectServer(ctx context.Context, cfg *MCPServerConfig) error {
	var cli *client.Client
	var err error

	switch cfg.Type {
	case "sse":
		cli, err = client.NewSSEMCPClient(cfg.URL)
		if err != nil {
			return fmt.Errorf("创建SSE客户端失败: %w", err)
		}
	case "stdio":
		cli, err = client.NewStdioMCPClient(cfg.Command, cfg.Env, cfg.Args...)
		if err != nil {
			return fmt.Errorf("创建Stdio客户端失败: %w", err)
		}
	default:
		return fmt.Errorf("不支持的MCP服务器类型: %s", cfg.Type)
	}

	// 启动客户端
	if err := cli.Start(ctx); err != nil {
		return fmt.Errorf("启动MCP客户端失败: %w", err)
	}

	// 初始化连接
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "amu-bot",
		Version: "2.0.0",
	}

	if _, err := cli.Initialize(ctx, initRequest); err != nil {
		_ = cli.Close()
		return fmt.Errorf("初始化MCP连接失败: %w", err)
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
		return fmt.Errorf("获取MCP工具失败: %w", err)
	}

	// 包装工具以添加调用日志
	wrappedTools := make([]tool.BaseTool, 0, len(baseTools))
	for _, t := range baseTools {
		wrappedTools = append(wrappedTools, &loggingToolWrapper{
			BaseTool:   t,
			serverName: cfg.Name,
		})
	}

	m.clients = append(m.clients, cli)
	m.tools = append(m.tools, wrappedTools...)

	zap.L().Info("已加载MCP工具",
		zap.String("server", cfg.Name),
		zap.Int("tool_count", len(baseTools)))

	return nil
}

// GetTools 获取所有MCP工具
func (m *MCPManager) GetTools() []tool.BaseTool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tools
}

// Close 关闭所有MCP连接
func (m *MCPManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, cli := range m.clients {
		if err := cli.Close(); err != nil {
			zap.L().Warn("关闭MCP客户端失败", zap.Error(err))
		}
	}

	m.clients = nil
	m.tools = nil
}

// loggingToolWrapper 带日志的工具包装器
type loggingToolWrapper struct {
	tool.BaseTool
	serverName string
}

// InvokableRun 包装 InvokableRun 方法以添加调用日志
func (w *loggingToolWrapper) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	if invokable, ok := w.BaseTool.(tool.InvokableTool); ok {
		result, err := invokable.InvokableRun(ctx, argumentsInJSON, opts...)
		// 截断返回结果到100字
		truncatedResult := result
		if len(truncatedResult) > 100 {
			truncatedResult = truncatedResult[:100] + "..."
		}
		tools.LogToolCall(w.serverName, argumentsInJSON, truncatedResult, err)
		return result, err
	}
	return "", fmt.Errorf("工具不支持 InvokableRun")
}
