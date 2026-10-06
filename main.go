package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mumu-bot/internal/agent"
	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/logger"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/migration"
	"mumu-bot/internal/modelstats"
	"mumu-bot/internal/onebot"
	webapp "mumu-bot/internal/web/app"
	"mumu-bot/internal/web/services"

	"go.uber.org/zap"
)

// contactSyncInterval 是联系人兜底同步间隔：好友删除没有事件，只能靠定期拉取纠正
const contactSyncInterval = 5 * time.Minute

// syncConversationContacts 拉取并保存 QQ 联系人与群列表，允许真实空列表更新会话状态
func syncConversationContacts(botClient *onebot.Client, memoryMgr *memory.Manager) error {
	contacts, err := botClient.GetConversationContacts(context.Background())
	if err != nil {
		return err
	}
	if len(contacts) == 0 {
		zap.L().Warn("同步到的群与好友为空，按真实空列表更新会话状态")
	}
	rows := make([]memory.ContactSnapshot, 0, len(contacts))
	for _, contact := range contacts {
		rows = append(rows, memory.ContactSnapshot{
			Kind: contact.Kind, TargetID: contact.TargetID, Name: contact.Name,
			RemoteRemark: contact.RemoteRemark, Active: contact.Active, LastSeenAt: contact.LastSeenAt,
		})
	}
	return memoryMgr.SyncConversationContacts(context.Background(), rows)
}

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
	if err := migration.RunMigrations(db, selfID, cfg.Embedding.Dimensions); err != nil {
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

	memoryMgr, err := memory.NewManager(db, embeddingClient)
	if err != nil {
		_ = botClient.Close()
		zap.L().Fatal("记忆管理器创建失败", zap.Error(err))
	}
	defer memoryMgr.Close()
	defer statsRecorder.Close()
	zap.L().Info("记忆系统已初始化")
	if err := syncConversationContacts(botClient, memoryMgr); err != nil {
		_ = botClient.Close()
		zap.L().Fatal("同步 QQ 联系人失败", zap.Error(err))
	}
	// 重连、定期和事件补正三条路都会刷新联系人状态；同步失败只保留旧状态，不让所有事件被误判为拉黑
	botClient.OnConnected(func() {
		if err := syncConversationContacts(botClient, memoryMgr); err != nil {
			zap.L().Warn("重连后同步 QQ 联系人失败，沿用现有会话状态", zap.Error(err))
		}
	})
	// 机器人自己退群、被踢或群解散时立即标记为已离开；普通成员进出群不影响联系人状态
	botClient.OnGroupLeft(func(groupID int64) {
		if err := memoryMgr.MarkConversationActive(context.Background(), memory.ConversationKindGroup, groupID, false); err != nil {
			zap.L().Warn("标记群聊已离开失败", zap.Int64("group_id", groupID), zap.Error(err))
			return
		}
		zap.L().Info("机器人已不在该群，标记为已离开", zap.Int64("group_id", groupID))
	})
	// 新好友建立关系后立即放行，不必等下一次联系人同步
	botClient.OnFriendAdd(func(userID int64) {
		if err := memoryMgr.MarkConversationActive(context.Background(), memory.ConversationKindPrivate, userID, true); err != nil {
			zap.L().Warn("标记新好友可用失败", zap.Int64("user_id", userID), zap.Error(err))
		}
	})
	botClient.OnFriendRequest(func(request onebot.FriendRequestEvent) {
		if request.UserID <= 0 || strings.TrimSpace(request.Flag) == "" {
			return
		}
		// 好友申请不去重：flag 是申请发起时间戳，不同的人可能落在同一秒；
		// 过期或已在别处处理的申请会在后台处理失败时删除，对方重新申请会作为新事件入库
		row := memory.FriendRequest{
			Flag: request.Flag, UserID: request.UserID, Nickname: request.Nickname,
			Comment: request.Comment, Status: "pending", ReceivedAt: request.ReceivedAt,
		}
		if err := db.WithContext(context.Background()).Create(&row).Error; err != nil {
			zap.L().Warn("保存好友申请失败", zap.Error(err))
		}
	})

	mumuAgent, err := agent.New(memoryMgr, botClient)
	if err != nil {
		_ = botClient.Close()
		zap.L().Fatal("Agent 创建失败", zap.Error(err))
	}
	if err := mumuAgent.Start(); err != nil {
		mumuAgent.Stop()
		zap.L().Fatal("恢复聊天上下文失败", zap.Error(err))
	}

	stickerDir := cfg.Sticker.StoragePath
	adminService := services.NewAdminService(memoryMgr, stickerDir)
	app := webapp.New(cfg, adminService, memoryMgr, mumuAgent)
	httpServer := app.Server()
	botClient.ReleaseEventGate()

	// 好友删除没有 OneBot 事件，靠定期同步兜底；被踢出群即使漏了事件也会在这里纠正
	contactSyncStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(contactSyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-contactSyncStop:
				return
			case <-ticker.C:
				if !botClient.IsConnected() {
					continue
				}
				if err := syncConversationContacts(botClient, memoryMgr); err != nil {
					zap.L().Warn("定期同步 QQ 联系人失败，等待下次重试", zap.Error(err))
				}
			}
		}
	}()

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
	close(contactSyncStop)
	mumuAgent.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		zap.L().Warn("关闭管理后台失败", zap.Error(err))
	}

	zap.L().Info("再见！")
}
