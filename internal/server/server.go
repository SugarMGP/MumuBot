package server

import (
	"amu-bot/internal/config"
	"amu-bot/internal/memory"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Server HTTP服务
type Server struct {
	cfg       *config.Config
	memoryMgr *memory.Manager
	server    *http.Server
}

// NewServer 创建HTTP服务
func NewServer(cfg *config.Config, memoryMgr *memory.Manager) *Server {
	return &Server{
		cfg:       cfg,
		memoryMgr: memoryMgr,
	}
}

// Start 启动HTTP服务
func (s *Server) Start() {
	if !s.cfg.App.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()

	// 健康检查
	r.GET("/health", s.healthCheck)

	// API路由
	api := r.Group("/api")
	{
		// 记忆相关
		api.GET("/memories", s.listMemories)
		api.GET("/memories/:id", s.getMemory)
		api.DELETE("/memories/:id", s.deleteMemory)

		// 成员画像
		api.GET("/members", s.listMembers)
		api.GET("/members/:user_id", s.getMember)

		// 消息记录
		api.GET("/messages", s.listMessages)

		// 统计信息
		api.GET("/stats", s.getStats)

		// 状态
		api.GET("/status", s.getStatus)
	}

	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	s.server = &http.Server{
		Addr:    addr,
		Handler: r,
	}

	zap.L().Info("HTTP服务启动", zap.String("addr", addr))
	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		zap.L().Error("HTTP服务异常", zap.Error(err))
	}
}

// Stop 停止HTTP服务
func (s *Server) Stop() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.server.Shutdown(ctx)
	}
}

// healthCheck 健康检查
func (s *Server) healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"name":   "amu_bot",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// parsePageParams 解析分页参数
func parsePageParams(c *gin.Context) (page, pageSize int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ = strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return
}

// listMemories 列出记忆
func (s *Server) listMemories(c *gin.Context) {
	groupID, _ := strconv.ParseInt(c.DefaultQuery("group_id", "0"), 10, 64)
	memType := c.DefaultQuery("type", "")
	page, pageSize := parsePageParams(c)

	memories, total, err := s.memoryMgr.ListMemories(groupID, memType, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":      memories,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// getMemory 获取单个记忆
func (s *Server) getMemory(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的ID"})
		return
	}

	var mem memory.Memory
	if err := s.memoryMgr.GetDB().First(&mem, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "记忆不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": mem})
}

// deleteMemory 删除记忆
func (s *Server) deleteMemory(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的ID"})
		return
	}

	if err := s.memoryMgr.GetDB().Delete(&memory.Memory{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
}

// listMembers 列出成员画像
func (s *Server) listMembers(c *gin.Context) {
	groupID, _ := strconv.ParseInt(c.DefaultQuery("group_id", "0"), 10, 64)
	page, pageSize := parsePageParams(c)

	profiles, total, err := s.memoryMgr.ListMemberProfiles(groupID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":      profiles,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// getMember 获取单个成员画像
func (s *Server) getMember(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的用户ID"})
		return
	}

	groupID, _ := strconv.ParseInt(c.DefaultQuery("group_id", "0"), 10, 64)

	var profile memory.MemberProfile
	query := s.memoryMgr.GetDB().Where("user_id = ?", userID)
	if groupID > 0 {
		query = query.Where("group_id = ?", groupID)
	}

	if err := query.First(&profile).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "成员不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": profile})
}

// listMessages 列出消息记录
func (s *Server) listMessages(c *gin.Context) {
	groupID, _ := strconv.ParseInt(c.DefaultQuery("group_id", "0"), 10, 64)
	page, pageSize := parsePageParams(c)

	messages, total, err := s.memoryMgr.ListMessageLogs(groupID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":      messages,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// getStats 获取统计信息
func (s *Server) getStats(c *gin.Context) {
	stats := s.memoryMgr.GetStats()
	c.JSON(http.StatusOK, gin.H{"data": stats})
}

// getStatus 获取状态
func (s *Server) getStatus(c *gin.Context) {
	stats := s.memoryMgr.GetStats()

	c.JSON(http.StatusOK, gin.H{
		"status":  "running",
		"persona": s.cfg.Persona.Name,
		"groups":  len(s.cfg.Groups),
		"uptime":  time.Now().Format(time.RFC3339),
		"stats":   stats,
		"config": gin.H{
			"think_interval": s.cfg.Agent.ThinkInterval,
			"speak_cooldown": s.cfg.Agent.SpeakCooldown,
			"observe_window": s.cfg.Agent.ObserveWindow,
			"llm_model":      s.cfg.LLM.Model,
		},
	})
}
