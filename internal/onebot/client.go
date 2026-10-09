package onebot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"mumu-bot/internal/config"

	"github.com/jellydator/ttlcache/v3"
	ob "github.com/zjutjh/onebot-sdk"
	"github.com/zjutjh/onebot-sdk/api"
	"go.uber.org/zap"
)

type Client struct {
	transportCtx  context.Context
	stopTransport context.CancelFunc
	closeOnce     sync.Once

	connMu     sync.RWMutex
	sdk        *ob.Client
	generation uint64

	mutedMu    sync.RWMutex
	mutedUntil map[int64]time.Time
	selfID     atomic.Int64
	selfReady  chan struct{}
	selfOnce   sync.Once
	eventGate  chan struct{}
	gateOnce   sync.Once

	memberInfoCache *ttlcache.Cache[string, *GroupMemberInfo]
	onMessage       func(*ConversationMessage)
	onRecall        func(kind string, targetID, messageID int64, arrivalSeq uint64)
	connectedMu     sync.RWMutex
	onConnected     []func()
	onFriendRequest func(FriendRequestEvent)
	onGroupLeft     func(groupID int64)
	onFriendAdd     func(userID int64)
	transportWG     sync.WaitGroup
	eventWG         sync.WaitGroup
	seqMu           sync.Mutex
	scopeSeq        map[string]uint64
}

func NewClient() *Client {
	transportCtx, stopTransport := context.WithCancel(context.Background())
	cache := newGroupMemberInfoCache()
	c := &Client{
		transportCtx:    transportCtx,
		stopTransport:   stopTransport,
		mutedUntil:      make(map[int64]time.Time),
		memberInfoCache: cache,
		scopeSeq:        make(map[string]uint64),
		selfReady:       make(chan struct{}),
		eventGate:       make(chan struct{}),
	}
	go cache.Start()
	return c
}

func (c *Client) Connect() {
	c.startConnectLoop(0)
}

func (c *Client) connect() error {
	if c.transportCtx.Err() != nil {
		return context.Canceled
	}
	cfg := config.Get()
	// 由 SDK 通过 get_version_info 自动检测后端方言，兼容 NapCat 与 SnowLuma
	sdk, err := ob.DialWebSocket(c.transportCtx, cfg.OneBot.WsURL, ob.WithToken(cfg.OneBot.AccessToken), ob.WithRequestTimeout(30*time.Second), ob.WithEventBuffer(1024), ob.WithEventDeliveryTimeout(time.Second))
	if err != nil {
		return fmt.Errorf("WebSocket连接失败: %w", err)
	}
	login, err := sdk.API().GetLoginInfo(c.transportCtx, api.GetLoginInfoRequest{})
	if err != nil {
		_ = sdk.Close()
		return fmt.Errorf("获取OneBot登录账号失败: %w", err)
	}
	// UserID 为 int64，JSON 整数精确解码，不再需要 float64 精度校验
	if login == nil || login.UserID <= 0 {
		_ = sdk.Close()
		return fmt.Errorf("OneBot返回无效的登录账号")
	}
	selfID := int64(login.UserID)
	c.connMu.Lock()
	if c.transportCtx.Err() != nil {
		c.connMu.Unlock()
		_ = sdk.Close()
		return context.Canceled
	}
	c.selfID.Store(selfID)
	c.selfOnce.Do(func() { close(c.selfReady) })
	old := c.sdk
	c.sdk = sdk
	c.generation++
	generation := c.generation
	c.transportWG.Add(1)
	c.connMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	c.runConnectedHandlers()
	zap.L().Info("OneBot 后端方言", zap.String("dialect", sdk.Dialect(c.transportCtx).Name))
	go func() {
		defer c.transportWG.Done()
		c.consumeEvents(sdk, generation)
	}()
	return nil
}

func (c *Client) consumeEvents(sdk *ob.Client, generation uint64) {
	select {
	case <-c.eventGate:
	case <-c.transportCtx.Done():
		return
	}
	for ev := range sdk.Events() {
		c.enqueueEvent(ev)
	}
	err := sdk.Err()
	c.connMu.RLock()
	current := c.sdk == sdk && c.generation == generation
	c.connMu.RUnlock()
	switch {
	case c.transportCtx.Err() != nil || !current:
		zap.L().Debug("OneBot 事件流已主动关闭", zap.Error(err))
	case errors.Is(err, ob.ErrEventBackpressure):
		zap.L().Error("OneBot SDK 事件背压超时，连接将重建", zap.Error(err))
	case err != nil:
		zap.L().Warn("OneBot 网络事件流中断", zap.Error(err))
	default:
		zap.L().Warn("OneBot 事件流意外结束")
	}
	c.startReconnect(sdk, generation)
}

func (c *Client) startReconnect(disconnected *ob.Client, generation uint64) {
	c.connMu.Lock()
	if c.transportCtx.Err() != nil || c.sdk != disconnected || c.generation != generation {
		c.connMu.Unlock()
		return
	}
	c.sdk = nil
	c.connMu.Unlock()
	_ = disconnected.Close()
	c.startConnectLoop(generation)
}

func (c *Client) startConnectLoop(generation uint64) {
	c.transportWG.Add(1)
	go func() {
		defer c.transportWG.Done()
		c.connectLoop(generation)
	}()
}

func (c *Client) connectLoop(generation uint64) {
	cfg := config.Get()
	interval := time.Duration(cfg.OneBot.ReconnectInterval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	firstAttempt := true
	for {
		c.connMu.RLock()
		valid := c.sdk == nil && c.generation == generation
		c.connMu.RUnlock()
		if !valid {
			return
		}
		err := c.connect()
		if err == nil {
			zap.L().Info("已连接到 OneBot", zap.String("url", cfg.OneBot.WsURL), zap.Int64("self_id", c.GetSelfID()))
			return
		}
		if c.transportCtx.Err() != nil {
			return
		}
		if firstAttempt {
			zap.L().Warn("OneBot 连接失败，将在后台重试", zap.Error(err))
		} else {
			zap.L().Debug("OneBot 重连失败", zap.Error(err))
		}
		firstAttempt = false

		timer := time.NewTimer(interval)
		select {
		case <-c.transportCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
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
func (c *Client) OnMessage(handler func(*ConversationMessage)) { c.onMessage = handler }
func (c *Client) OnRecall(handler func(kind string, targetID, messageID int64, arrivalSeq uint64)) {
	c.onRecall = handler
}
func (c *Client) OnConnected(handler func()) {
	if handler != nil {
		c.connectedMu.Lock()
		c.onConnected = append(c.onConnected, handler)
		c.connectedMu.Unlock()
	}
}

// runConnectedHandlers 复制已注册回调后释放锁，允许回调内继续注册或执行业务
func (c *Client) runConnectedHandlers() {
	c.connectedMu.RLock()
	handlers := append([]func(){}, c.onConnected...)
	c.connectedMu.RUnlock()
	for _, handler := range handlers {
		handler()
	}
}

func (c *Client) OnFriendRequest(handler func(FriendRequestEvent)) { c.onFriendRequest = handler }
func (c *Client) OnGroupLeft(handler func(groupID int64))          { c.onGroupLeft = handler }
func (c *Client) OnFriendAdd(handler func(userID int64))           { c.onFriendAdd = handler }
func (c *Client) GetSelfID() int64                                 { return c.selfID.Load() }
func (c *Client) WaitSelfID(ctx context.Context) (int64, error) {
	if selfID := c.GetSelfID(); selfID > 0 {
		return selfID, nil
	}
	select {
	case <-c.selfReady:
		return c.GetSelfID(), nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (c *Client) ReleaseEventGate() { c.gateOnce.Do(func() { close(c.eventGate) }) }
func (c *Client) IsConnected() bool {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.sdk != nil
}

func (c *Client) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		// 先封死连接发布并终止拨号、重连和 SDK 事件流
		c.stopTransport()
		c.connMu.Lock()
		sdk := c.sdk
		c.sdk = nil
		c.connMu.Unlock()
		if sdk != nil {
			closeErr = sdk.Close()
		}

		// 事件入口完全退出后，等待所有已分发的并发事件处理完成
		// 注意：不清零 selfID——Agent 停机排空提交队列时仍需用真实账号
		// 区分机器人自身消息；账号未就绪的语义由断线重连路径负责
		c.transportWG.Wait()
		c.eventWG.Wait()

		if c.memberInfoCache != nil {
			c.memberInfoCache.Stop()
		}
	})
	return closeErr
}

func (c *Client) currentSDK() (*ob.Client, error) {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	if c.sdk == nil {
		return nil, errors.New("未连接到 OneBot 服务")
	}
	return c.sdk, nil
}
