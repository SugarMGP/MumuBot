package main

import (
	"context"
	"fmt"
	"mumu-bot/internal/agent"
	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/logger"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelstats"
	"mumu-bot/internal/onebot"
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

	botClient := onebot.NewClient()
	botClient.Connect()
	selfID, err := botClient.WaitSelfID(context.Background())
	if err != nil {
		_ = botClient.Close()
		zap.L().Fatal("等待 OneBot 登录账号失败", zap.Error(err))
	}
	zap.L().Info("OneBot 登录账号已就绪", zap.Int64("self_id", selfID))

	db, err := memory.OpenDB()
	if err != nil {
		_ = botClient.Close()
		zap.L().Fatal("打开 PostgreSQL 失败", zap.Error(err))
	}
	if err := memory.RunMigrations(db, selfID, cfg.Embedding.Dimensions); err != nil {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
		_ = botClient.Close()
		zap.L().Fatal("数据库迁移失败", zap.Error(err))
	}
	statsRecorder := modelstats.NewRecorder()
	modelstats.SetDefault(statsRecorder)
	statsRecorder.Start(db)

	embeddingClient, err := llm.NewEmbeddingClient()
	if err != nil {
		_ = botClient.Close()
		zap.L().Fatal("Embedding 客户端创建失败", zap.Error(err))
	}

	mergeModel, err := llm.NewClientForTier(llm.TierLow)
	if err != nil {
		_ = botClient.Close()
		zap.L().Fatal("记忆合并模型创建失败", zap.Error(err))
	}

	memoryMgr, err := memory.NewManager(db, embeddingClient, mergeModel)
	if err != nil {
		_ = botClient.Close()
		zap.L().Fatal("记忆管理器创建失败", zap.Error(err))
	}
	defer memoryMgr.Close()
	defer statsRecorder.Close()
	zap.L().Info("记忆系统已初始化")

	mumuAgent, err := agent.New(memoryMgr, botClient)
	if err != nil {
		_ = botClient.Close()
		zap.L().Fatal("Agent 创建失败", zap.Error(err))
	}
	mumuAgent.Start()

	stickerDir := cfg.Sticker.StoragePath
	adminService := services.NewAdminService(memoryMgr, stickerDir, mumuAgent.ReloadJargons, mumuAgent.BotSelfID)
	app := webapp.New(cfg, adminService, memoryMgr, mumuAgent)
	httpServer := app.Server()
	botClient.ReleaseEventGate()

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
