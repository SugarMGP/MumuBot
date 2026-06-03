package onebot

import (
	"context"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/utils"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gorilla/websocket"
	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
)

// Client OneBot WebSocket客户端
type Client struct {
	conn      *websocket.Conn
	connMu    sync.Mutex
	handlers  map[string][]EventHandler
	selfID    int64
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once

	mutedMu    sync.RWMutex
	mutedUntil map[int64]time.Time

	memberInfoCache *ttlcache.Cache[string, *GroupMemberInfo]

	// 消息回调
	onMessage func(*GroupMessage)

	// 重连控制
	reconnecting bool

	// API 调用响应等待
	echoCounter uint64
	pendingReqs sync.Map // map[string]chan *APIResponse
}

// NewClient 创建OneBot客户端
func NewClient() *Client {
	ctx, cancel := context.WithCancel(context.Background())
	memberInfoCache := newGroupMemberInfoCache()
	client := &Client{
		handlers:        make(map[string][]EventHandler),
		ctx:             ctx,
		cancel:          cancel,
		mutedUntil:      make(map[int64]time.Time),
		memberInfoCache: memberInfoCache,
	}
	go memberInfoCache.Start()
	return client
}

// Connect 连接到OneBot服务
func (c *Client) Connect() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	cfg := config.Get()
	header := make(map[string][]string)
	if cfg.OneBot.AccessToken != "" {
		header["Authorization"] = []string{"Bearer " + cfg.OneBot.AccessToken}
	}

	conn, _, err := websocket.DefaultDialer.Dial(cfg.OneBot.WsURL, header)
	if err != nil {
		return fmt.Errorf("WebSocket连接失败: %w", err)
	}

	c.conn = conn
	c.reconnecting = false

	// 启动消息接收循环
	go c.receiveLoop()

	zap.L().Info("已连接到 OneBot", zap.String("url", cfg.OneBot.WsURL))
	return nil
}

// receiveLoop 消息接收循环
func (c *Client) receiveLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		_, message, err := c.conn.ReadMessage()
		if err != nil {
			zap.L().Error("读取消息失败", zap.Error(err))
			c.handleDisconnect()
			return
		}

		go c.handleMessage(message)
	}
}

// handleMessage 处理收到的消息
func (c *Client) handleMessage(data []byte) {
	var event map[string]interface{}
	if err := sonic.Unmarshal(data, &event); err != nil {
		zap.L().Error("解析消息失败", zap.Error(err))
		return
	}

	// 检查是否是 API 响应（有 echo 字段）
	if echo, ok := event["echo"].(string); ok && echo != "" {
		c.handleAPIResponse(event, echo)
		return
	}

	// 处理事件
	if postType, ok := event["post_type"].(string); ok {
		switch postType {
		case "meta_event":
			c.handleMetaEvent(event)
		case "message":
			c.handleMessageEvent(event)
		case "notice":
			c.handleNoticeEvent(event)
		case "request":
			c.handleRequestEvent(event)
		}
	}
}

// handleAPIResponse 处理 API 响应
func (c *Client) handleAPIResponse(event map[string]interface{}, echo string) {
	if ch, ok := c.pendingReqs.Load(echo); ok {
		resp := &APIResponse{Echo: echo}
		if status, ok := event["status"].(string); ok {
			resp.Status = status
		}
		if retCode, ok := parseInt(event["retcode"]); ok {
			resp.RetCode = retCode
		}
		// Data 可能是 map 或 array
		resp.Data = event["data"]
		if msg, ok := event["message"].(string); ok {
			resp.Message = msg
		}
		ch.(chan *APIResponse) <- resp
	}
}

// handleMetaEvent 处理元事件
func (c *Client) handleMetaEvent(event map[string]interface{}) {
	metaType, _ := event["meta_event_type"].(string)

	if metaType == "lifecycle" {
		subType, _ := event["sub_type"].(string)
		if subType == "connect" {
			if selfID, ok := utils.ParseInt64Value(event["self_id"]); ok {
				c.selfID = selfID
				zap.L().Info("Bot 已上线", zap.Int64("qq", c.selfID))
			}
		}
	}
}

// handleMessageEvent 处理消息事件
func (c *Client) handleMessageEvent(event map[string]interface{}) {
	msgType, _ := event["message_type"].(string)

	// 只处理群消息
	if msgType != "group" {
		return
	}

	// 解析消息
	msg := c.parseGroupMessage(event)
	if msg == nil {
		return
	}

	// 调用消息回调
	if c.onMessage != nil {
		c.onMessage(msg)
	}
}

// handleNoticeEvent 处理通知事件
func (c *Client) handleNoticeEvent(event map[string]interface{}) {
	noticeType, _ := event["notice_type"].(string)
	subType, _ := event["sub_type"].(string)
	zap.L().Debug("收到通知", zap.String("type", noticeType), zap.String("sub_type", subType))

	if noticeType == "group_ban" {
		c.handleGroupBanNotice(event, subType)
	}
}

func (c *Client) handleGroupBanNotice(event map[string]interface{}, subType string) {
	groupID, ok := utils.ParseInt64Value(event["group_id"])
	if !ok || groupID == 0 {
		return
	}

	userID, ok := utils.ParseInt64Value(event["user_id"])
	if !ok || userID != c.selfID {
		return
	}

	if subType == "lift_ban" {
		c.clearSelfMuted(groupID)
		return
	}

	if subType != "ban" {
		return
	}

	if durationSec, ok := utils.ParseInt64Value(event["duration"]); ok && durationSec > 0 {
		c.setSelfMutedUntil(groupID, time.Now().Add(time.Duration(durationSec)*time.Second))
		return
	}

	// 如果没有时长或为 0，视为未禁言
	c.clearSelfMuted(groupID)
}

func (c *Client) setSelfMutedUntil(groupID int64, until time.Time) {
	c.mutedMu.Lock()
	c.mutedUntil[groupID] = until
	c.mutedMu.Unlock()
}

func (c *Client) clearSelfMuted(groupID int64) {
	c.mutedMu.Lock()
	delete(c.mutedUntil, groupID)
	c.mutedMu.Unlock()
}

// IsSelfMuted 判断当前群内机器人是否处于禁言状态
func (c *Client) IsSelfMuted(groupID int64) bool {
	c.mutedMu.RLock()
	until, ok := c.mutedUntil[groupID]
	c.mutedMu.RUnlock()
	if !ok || until.IsZero() {
		return false
	}

	if time.Now().After(until) {
		c.clearSelfMuted(groupID)
		return false
	}

	return true
}

// handleRequestEvent 处理请求事件（加群/加好友请求）
func (c *Client) handleRequestEvent(event map[string]interface{}) {
	requestType, _ := event["request_type"].(string)
	zap.L().Debug("收到请求", zap.String("type", requestType))
}

// OnMessage 设置消息回调
func (c *Client) OnMessage(handler func(*GroupMessage)) {
	c.onMessage = handler
}

// GetSelfID 获取Bot的QQ号
func (c *Client) GetSelfID() int64 {
	return c.selfID
}

// IsConnected 返回当前 OneBot WebSocket 是否已连接。
func (c *Client) IsConnected() bool {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	return c.conn != nil && !c.reconnecting
}

// Close 关闭连接
func (c *Client) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		if c.memberInfoCache != nil {
			c.memberInfoCache.Stop()
		}

		c.connMu.Lock()
		defer c.connMu.Unlock()

		if c.conn != nil {
			closeErr = c.conn.Close()
			c.conn = nil
		}
	})
	return closeErr
}

// handleDisconnect 处理断开连接
func (c *Client) handleDisconnect() {
	if c.reconnecting {
		return
	}
	c.reconnecting = true

	zap.L().Warn("连接断开，尝试重连...")

	interval := time.Duration(config.Get().OneBot.ReconnectInterval) * time.Second
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(interval):
		}

		if err := c.Connect(); err == nil {
			zap.L().Info("重连成功")
			return
		}
		zap.L().Warn("重连失败，继续尝试...")
	}
}
