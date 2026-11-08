package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"mumu-bot/internal/agent"
	"mumu-bot/internal/config"
	"mumu-bot/internal/logger"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/migration"
	"mumu-bot/internal/modelprofiles"
	"mumu-bot/internal/modelstats"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/secrets"
	webapp "mumu-bot/internal/web/app"
	"mumu-bot/internal/web/services"

	"go.uber.org/zap"
)

// contactSyncInterval 是联系人兜底同步间隔：好友删除没有事件，只能靠定期拉取纠正
const contactSyncInterval = 5 * time.Minute

// syncConversationContacts 拉取并保存 QQ 联系人与群列表，允许真实空列表更新会话状态
func syncConversationContacts(botClient *onebot.Client, memoryMgr *memory.Manager) error {
	fetchedAt := time.Now()
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
			RemoteRemark: contact.RemoteRemark, Active: contact.Active,
		})
	}
	return memoryMgr.SyncConversationContacts(context.Background(), rows, fetchedAt)
}

func main() {
	boot, err := config.LoadBootstrap()
	if err != nil {
		fmt.Printf("加载启动配置失败: %v\n", err)
		os.Exit(1)
	}
	logger.Init(boot.LogLevel, boot.Debug)
	zap.L().Info("启动配置已加载")
	if err := secrets.Configure(boot.SecretKey); err != nil {
		zap.L().Warn("加密密钥不可用，模型和 MCP 配置暂时无法应用", zap.Error(err))
	}

	db, err := memory.OpenDB(boot.DatabaseDSN)
	if err != nil {
		zap.L().Fatal("打开 PostgreSQL 失败", zap.Error(err))
	}
	if err := migration.RunMigrations(db); err != nil {
		zap.L().Fatal("数据库迁移失败", zap.Error(err))
	}
	if err := config.InitStore(db); err != nil {
		zap.L().Fatal("加载动态配置失败", zap.Error(err))
	}
	modelStore := modelprofiles.NewStore(db)
	mcpStore := mcp.NewStore(db)

	statsRecorder := modelstats.NewRecorder()
	modelstats.SetDefault(statsRecorder)
	statsRecorder.Start(db)

	memoryMgr, err := memory.NewManager(db, boot.MediaDir)
	if err != nil {
		zap.L().Fatal("记忆管理器创建失败", zap.Error(err))
	}
	defer memoryMgr.Close()
	defer statsRecorder.Close()
	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := bootstrapPersonaPresets(bootstrapCtx, memoryMgr); err != nil {
		bootstrapCancel()
		zap.L().Fatal("初始化人格预设失败", zap.Error(err))
	}
	bootstrapCancel()
	if err := config.Reload(); err != nil {
		zap.L().Fatal("加载人格预设失败", zap.Error(err))
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := memoryMgr.CleanupToolResults(cleanupCtx, 24*time.Hour); err != nil {
		zap.L().Warn("清理无引用工具结果失败", zap.Error(err))
	}
	if _, _, err := memoryMgr.CleanupSnapshotRefs(cleanupCtx, 24*time.Hour); err != nil {
		zap.L().Warn("清理无引用文件快照失败", zap.Error(err))
	}
	cleanupCancel()
	zap.L().Info("记忆系统已初始化")

	botClient := onebot.NewClient(boot)
	botClient.OnConnected(func() {
		if err := syncConversationContacts(botClient, memoryMgr); err != nil {
			zap.L().Warn("重连后同步 QQ 联系人失败，沿用现有会话状态", zap.Error(err))
		}
	})
	botClient.OnGroupLeft(func(groupID int64) {
		if err := memoryMgr.MarkConversationActive(context.Background(), memory.ConversationKindGroup, groupID, false); err != nil {
			zap.L().Warn("标记群聊已离开失败", zap.Int64("group_id", groupID), zap.Error(err))
			return
		}
		zap.L().Info("机器人已不在该群，标记为已离开", zap.Int64("group_id", groupID))
	})
	botClient.OnFriendAdd(func(userID int64) {
		if err := memoryMgr.MarkConversationActive(context.Background(), memory.ConversationKindPrivate, userID, true); err != nil {
			zap.L().Warn("标记新好友可用失败", zap.Int64("user_id", userID), zap.Error(err))
		}
	})
	botClient.Connect()
	defer botClient.Close()

	adminService := services.NewAdminService(memoryMgr, boot.StickerDir)
	app := webapp.New(boot, adminService, memoryMgr, nil, modelStore, mcpStore)
	app.SetBotStatus(func() (bool, int64) { return botClient.IsConnected(), botClient.GetSelfID() })
	httpServer := app.Server()
	go func() {
		zap.L().Info("管理后台启动", zap.String("addr", app.Addr()))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			zap.L().Error("管理后台异常退出", zap.Error(err))
		}
	}()

	// 后台先可用；QQ 账号和 Agent 在独立协程就绪后再挂上运行态
	initCtx, initCancel := context.WithCancel(context.Background())
	var agentMu sync.Mutex
	var mumuAgent *agent.Agent
	shuttingDown := false
	go func() {
		selfID, waitErr := botClient.WaitSelfID(initCtx)
		if waitErr != nil {
			zap.L().Warn("等待 OneBot 登录账号失败，后台继续保持可用", zap.Error(waitErr))
			return
		}
		zap.L().Info("OneBot 登录账号已就绪", zap.Int64("self_id", selfID))
		if err := syncConversationContacts(botClient, memoryMgr); err != nil {
			zap.L().Warn("同步 QQ 联系人失败，后台仍可用，等待重连或下次同步", zap.Error(err))
		}
		if initCtx.Err() != nil {
			return
		}
		newAgent, newErr := agent.New(memoryMgr, botClient, modelStore, mcpStore, boot.WorkerExecutorURL)
		if newErr != nil {
			zap.L().Warn("Agent 创建失败，后台保持可用", zap.Error(newErr))
			return
		}
		if err := newAgent.Start(); err != nil {
			newAgent.Stop()
			zap.L().Warn("Agent 启动失败，后台保持可用", zap.Error(err))
			return
		}
		if initCtx.Err() != nil {
			newAgent.Stop()
			return
		}
		agentMu.Lock()
		if shuttingDown {
			agentMu.Unlock()
			newAgent.Stop()
			return
		}
		mumuAgent = newAgent
		agentMu.Unlock()
		app.SetAgent(newAgent)
		if _, activeErr := modelStore.Active(context.Background()); activeErr == nil {
			if err := newAgent.Activate(); err != nil {
				zap.L().Warn("首次模型激活失败，等待后台修正", zap.Error(err))
			}
		} else {
			zap.L().Info("尚未配置模型，等待后台设置")
		}
	}()

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

	zap.L().Info("后台已启动，等待 QQ 登录后启用聊天")
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	zap.L().Info("正在关闭...")
	initCancel()
	close(contactSyncStop)
	agentMu.Lock()
	shuttingDown = true
	currentAgent := mumuAgent
	agentMu.Unlock()
	if currentAgent != nil {
		currentAgent.Stop()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		zap.L().Warn("关闭管理后台失败", zap.Error(err))
	}
	zap.L().Info("再见！")
}

// bootstrapPersonaPresets 用代码内置文本维护内置预设，空库时它同时是当前预设
func bootstrapPersonaPresets(ctx context.Context, mgr *memory.Manager) error {
	_, err := mgr.EnsureBuiltinPersonaPreset(ctx, config.BuiltinPersonaTexts())
	return err
}
