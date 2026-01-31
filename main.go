package main

import (
	"amu-bot/internal/agent"
	"amu-bot/internal/config"
	"amu-bot/internal/llm"
	"amu-bot/internal/logger"
	"amu-bot/internal/memory"
	"amu-bot/internal/onebot"
	"amu-bot/internal/persona"
	"amu-bot/internal/server"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
)

func main() {
	fmt.Println("=================================")
	fmt.Println("    阿沐 - 赛博QQ群友 v2.0")
	fmt.Println("    (powered by eino ReAct)")
	fmt.Println("=================================")

	// 加载配置
	configPath := "config/config.yaml"
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Printf("加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志系统
	logger.Init(cfg.App.LogLevel, cfg.App.Debug)

	zap.L().Info("配置已加载", zap.String("path", configPath))

	// 创建Embedding客户端
	embeddingClient, err := llm.NewEmbeddingClient(cfg)
	if err != nil {
		zap.L().Warn("Embedding客户端创建失败，向量检索不可用", zap.Error(err))
		embeddingClient = nil
	}

	// 创建记忆管理器
	memoryMgr, err := memory.NewManager(cfg, embeddingClient)
	if err != nil {
		zap.L().Fatal("记忆管理器创建失败", zap.Error(err))
	}
	defer memoryMgr.Close()
	zap.L().Info("记忆系统已初始化")

	// 创建LLM客户端
	llmClient, err := llm.NewClient(cfg)
	if err != nil {
		zap.L().Fatal("LLM客户端创建失败", zap.Error(err))
	}
	zap.L().Info("LLM已连接", zap.String("model", cfg.LLM.Model), zap.String("base_url", cfg.LLM.BaseURL))

	// 创建Vision客户端（多模态图片理解）
	var visionClient *llm.VisionClient
	if cfg.VisionLLM.Enabled {
		visionClient, err = llm.NewVisionClient(&cfg.VisionLLM)
		if err != nil {
			zap.L().Warn("Vision客户端创建失败，图片理解不可用", zap.Error(err))
		} else {
			zap.L().Info("Vision已启用", zap.String("model", cfg.VisionLLM.Model))
		}
	}

	// 创建OneBot客户端
	botClient := onebot.NewClient(cfg)
	if err := botClient.Connect(); err != nil {
		zap.L().Fatal("OneBot连接失败", zap.Error(err))
	}
	defer botClient.Close()

	// 创建人格
	amuPersona := persona.NewPersona(&cfg.Persona)
	zap.L().Info("人格已加载", zap.String("name", amuPersona.GetName()))

	// 获取底层 ChatModel 作为 ToolCallingChatModel
	chatModel := llmClient.GetModel()

	// 创建 Agent (使用 eino ReAct)
	amuAgent, err := agent.New(cfg, amuPersona, memoryMgr, chatModel, visionClient, botClient)
	if err != nil {
		zap.L().Fatal("Agent创建失败", zap.Error(err))
	}
	amuAgent.Start()

	// 启动HTTP服务（用于健康检查等）
	httpServer := server.NewServer(cfg, memoryMgr)
	go httpServer.Start()

	// 等待退出信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	zap.L().Info("阿沐已上线，按 Ctrl+C 退出")
	<-quit

	zap.L().Info("正在关闭...")
	amuAgent.Stop()
	httpServer.Stop()
	zap.L().Info("再见！")
}
