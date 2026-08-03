package main

import (
	"context"
	"fmt"
	"mumu-bot/internal/agent"
	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/logger"
	"mumu-bot/internal/memory"
	webapp "mumu-bot/internal/web/app"
	"mumu-bot/internal/web/services"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
)

func main() {
	configPath := "config/config.yaml"
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Printf("加载配置失败: %v\n", err)
		os.Exit(1)
	}

	logger.Init(cfg.App.LogLevel, cfg.App.Debug)
	zap.L().Info("配置已加载", zap.String("path", configPath))

	embeddingClient, err := llm.NewEmbeddingClient()
	if err != nil {
		zap.L().Fatal("Embedding 客户端创建失败", zap.Error(err))
	}

	claimModel, err := llm.NewClientForTier(llm.TierLow)
	if err != nil {
		zap.L().Fatal("记忆结构化提取模型创建失败", zap.Error(err))
	}
	zap.L().Info("记忆结构化提取模型已就绪", zap.String("tier", "low"))

	memoryMgr, err := memory.NewManager(embeddingClient, claimModel)
	if err != nil {
		zap.L().Fatal("记忆管理器创建失败", zap.Error(err))
	}
	defer memoryMgr.Close()
	zap.L().Info("记忆系统已初始化")

	mumuAgent, err := agent.New(memoryMgr)
	if err != nil {
		zap.L().Fatal("Agent 创建失败", zap.Error(err))
	}
	mumuAgent.Start()

	stickerDir := cfg.Sticker.StoragePath
	adminService := services.NewAdminService(memoryMgr, stickerDir, mumuAgent.ReloadJargons)
	app := webapp.New(cfg, adminService, memoryMgr, mumuAgent)
	httpServer := app.Server()

	go func() {
		zap.L().Info("管理后台启动", zap.String("addr", app.Addr()))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			zap.L().Error("管理后台异常退出", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	zap.L().Info("沐沐已上线，按 Ctrl+C 退出")
	<-quit

	zap.L().Info("正在关闭...")
	mumuAgent.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		zap.L().Warn("关闭管理后台失败", zap.Error(err))
	}

	zap.L().Info("再见！")
}
