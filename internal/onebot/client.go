package onebot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/utils"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/jellydator/ttlcache/v3"
	napcat "github.com/zjutjh/napcat-sdk"
	"go.uber.org/zap"
)

const groupEventQueueSize = 256

type Client struct {
	transportCtx  context.Context
	stopTransport context.CancelFunc
	closeOnce     sync.Once

	connMu     sync.RWMutex
	sdk        *napcat.Client
	generation uint64

	mutedMu    sync.RWMutex
	mutedUntil map[int64]time.Time
	selfMu     sync.RWMutex
	selfID     int64

	memberInfoCache *ttlcache.Cache[string, *GroupMemberInfo]
	onMessage       func(*GroupMessage)
	onRecall        func(groupID, messageID, operatorID int64)
	workersMu       sync.Mutex
	workers         map[int64]chan []byte
	workerWG        sync.WaitGroup
	transportWG     sync.WaitGroup
}

func NewClient() *Client {
	transportCtx, stopTransport := context.WithCancel(context.Background())
	cache := newGroupMemberInfoCache()
	c := &Client{
		transportCtx:    transportCtx,
		stopTransport:   stopTransport,
		mutedUntil:      make(map[int64]time.Time),
		memberInfoCache: cache,
		workers:         make(map[int64]chan []byte),
	}
	go cache.Start()
	return c
}

func (c *Client) Connect() error {
	if err := c.connect(); err != nil {
		return err
	}
	zap.L().Info("已连接到 OneBot", zap.String("url", config.Get().OneBot.WsURL))
	return nil
}

func (c *Client) connect() error {
	if c.transportCtx.Err() != nil {
		return context.Canceled
	}
	cfg := config.Get()
	sdk, err := napcat.DialWebSocket(c.transportCtx, cfg.OneBot.WsURL, napcat.WithToken(cfg.OneBot.AccessToken), napcat.WithRequestTimeout(30*time.Second), napcat.WithEventBuffer(1024), napcat.WithEventDeliveryTimeout(time.Second))
	if err != nil {
		return fmt.Errorf("WebSocket连接失败: %w", err)
	}
	c.connMu.Lock()
	if c.transportCtx.Err() != nil {
		c.connMu.Unlock()
		_ = sdk.Close()
		return context.Canceled
	}
	old := c.sdk
	c.sdk = sdk
	c.generation++
	generation := c.generation
	c.transportWG.Add(1)
	c.connMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	go func() {
		defer c.transportWG.Done()
		c.consumeEvents(sdk, generation)
	}()
	return nil
}

func (c *Client) consumeEvents(sdk *napcat.Client, generation uint64) {
	for ev := range sdk.Events() {
		selfID := ev.SelfID()
		if selfID > 0 {
			c.selfMu.Lock()
			c.selfID = selfID
			c.selfMu.Unlock()
		}
		c.enqueueEvent(ev.Raw())
	}
	err := sdk.Err()
	c.connMu.RLock()
	current := c.sdk == sdk && c.generation == generation
	c.connMu.RUnlock()
	switch {
	case c.transportCtx.Err() != nil || !current:
		zap.L().Debug("OneBot 事件流已主动关闭", zap.Error(err))
	case errors.Is(err, napcat.ErrEventBackpressure):
		zap.L().Error("OneBot SDK 事件背压超时，连接将重建", zap.Error(err))
	case err != nil:
		zap.L().Warn("OneBot 网络事件流中断", zap.Error(err))
	default:
		zap.L().Warn("OneBot 事件流意外结束")
	}
	c.startReconnect(sdk, generation)
}

func (c *Client) enqueueEvent(raw []byte) {
	var event map[string]interface{}
	decoder := sonic.ConfigDefault.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&event); err != nil {
		zap.L().Warn("解析事件分组键失败", zap.Error(err))
		return
	}
	postType, _ := event["post_type"].(string)
	switch postType {
	case "meta_event":
		c.handleMetaEvent(event)
		return
	case "notice":
		notice, _ := event["notice_type"].(string)
		sub, _ := event["sub_type"].(string)
		if notice == "group_ban" {
			c.handleNoticeEvent(event)
			return
		}
		if notice != "group_recall" && (notice != "notify" || sub != "poke") {
			return
		}
	case "request":
		c.handleRequestEvent(event)
		return
	case "message":
		if event["message_type"] != "group" {
			return
		}
	default:
		return
	}
	groupID, groupOK := utils.ParseInt64Value(event["group_id"])
	if !groupOK || groupID <= 0 {
		zap.L().Warn("忽略缺少有效群号的群事件")
		return
	}
	if postType == "message" {
		messageID, messageOK := utils.ParseInt64Value(event["message_id"])
		if !messageOK || messageID <= 0 {
			zap.L().Warn("忽略缺少有效编号的群消息")
			return
		}
	}
	c.enqueueGroupEvent(groupID, raw)
}

func (c *Client) enqueueGroupEvent(groupID int64, raw []byte) {
	c.workersMu.Lock()
	queue := c.workers[groupID]
	if queue == nil {
		queue = make(chan []byte, groupEventQueueSize)
		c.workers[groupID] = queue
		c.workerWG.Add(1)
		go c.runEventWorker(queue)
	}
	c.workersMu.Unlock()
	select {
	case queue <- raw:
	case <-c.transportCtx.Done():
	}
}

func (c *Client) runEventWorker(queue <-chan []byte) {
	defer c.workerWG.Done()
	for raw := range queue {
		c.handleGroupEvent(raw)
	}
}

func (c *Client) startReconnect(disconnected *napcat.Client, generation uint64) {
	c.connMu.Lock()
	if c.transportCtx.Err() != nil || c.sdk != disconnected || c.generation != generation {
		c.connMu.Unlock()
		return
	}
	c.sdk = nil
	c.connMu.Unlock()
	_ = disconnected.Close()
	zap.L().Warn("连接断开，尝试重连")
	interval := time.Duration(config.Get().OneBot.ReconnectInterval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		timer := time.NewTimer(interval)
		select {
		case <-c.transportCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		c.connMu.RLock()
		valid := c.sdk == nil && c.generation == generation
		c.connMu.RUnlock()
		if !valid {
			return
		}
		if err := c.connect(); err == nil {
			zap.L().Info("重连成功")
			return
		} else {
			zap.L().Warn("重连失败", zap.Error(err))
		}
	}
}

func (c *Client) handleGroupEvent(data []byte) {
	var event map[string]interface{}
	decoder := sonic.ConfigDefault.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&event); err != nil {
		zap.L().Error("解析消息失败", zap.Error(err))
		return
	}
	switch event["post_type"] {
	case "message":
		c.handleMessageEvent(event)
	case "notice":
		c.handleNoticeEvent(event)
	}
}

func (c *Client) handleMetaEvent(event map[string]interface{}) {
	if event["meta_event_type"] == "lifecycle" && event["sub_type"] == "connect" {
		if id, ok := utils.ParseInt64Value(event["self_id"]); ok {
			c.selfMu.Lock()
			c.selfID = id
			c.selfMu.Unlock()
		}
	}
}
func (c *Client) handleMessageEvent(event map[string]interface{}) {
	if event["message_type"] != "group" {
		return
	}
	if msg := c.parseGroupMessage(event); msg != nil && c.onMessage != nil {
		c.onMessage(msg)
	}
}
func (c *Client) handleNoticeEvent(event map[string]interface{}) {
	notice, _ := event["notice_type"].(string)
	sub, _ := event["sub_type"].(string)
	switch {
	case notice == "group_ban":
		c.handleGroupBanNotice(event, sub)
	case notice == "notify" && sub == "poke":
		c.handleGroupPokeNotice(event)
	case notice == "group_recall":
		c.handleGroupRecallNotice(event)
	}
}

func (c *Client) handleGroupPokeNotice(event map[string]interface{}) {
	groupID, groupOK := utils.ParseInt64Value(event["group_id"])
	userID, userOK := utils.ParseInt64Value(event["user_id"])
	targetID, targetOK := utils.ParseInt64Value(event["target_id"])
	if !groupOK || groupID <= 0 || !userOK || userID <= 0 || !targetOK || targetID <= 0 || c.onMessage == nil {
		return
	}
	eventTime := time.Now()
	if seconds, ok := utils.ParseInt64Value(event["time"]); ok && seconds > 0 {
		eventTime = time.Unix(seconds, 0)
	}
	selfID := c.GetSelfID()
	c.onMessage(&GroupMessage{
		GroupID:     groupID,
		UserID:      userID,
		AtList:      []int64{targetID},
		IsMentioned: targetID == selfID && userID != selfID,
		Time:        eventTime,
	})
}

func (c *Client) handleGroupRecallNotice(event map[string]interface{}) {
	groupID, groupOK := utils.ParseInt64Value(event["group_id"])
	messageID, messageOK := utils.ParseInt64Value(event["message_id"])
	operatorID, _ := utils.ParseInt64Value(event["operator_id"])
	if groupOK && groupID > 0 && messageOK && messageID > 0 && c.onRecall != nil {
		c.onRecall(groupID, messageID, operatorID)
	}
}
func (c *Client) handleRequestEvent(event map[string]interface{}) {
	request, _ := event["request_type"].(string)
	zap.L().Debug("收到请求", zap.String("type", request))
}

func (c *Client) handleGroupBanNotice(event map[string]interface{}, subType string) {
	groupID, ok := utils.ParseInt64Value(event["group_id"])
	if !ok || groupID == 0 {
		return
	}
	userID, ok := utils.ParseInt64Value(event["user_id"])
	if !ok || userID != c.GetSelfID() {
		return
	}
	if subType == "lift_ban" {
		c.clearSelfMuted(groupID)
		return
	}
	if subType != "ban" {
		return
	}
	if seconds, ok := utils.ParseInt64Value(event["duration"]); ok && seconds > 0 {
		c.setSelfMutedUntil(groupID, time.Now().Add(time.Duration(seconds)*time.Second))
		return
	}
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
func (c *Client) OnMessage(handler func(*GroupMessage)) { c.onMessage = handler }
func (c *Client) OnRecall(handler func(groupID, messageID, operatorID int64)) {
	c.onRecall = handler
}
func (c *Client) GetSelfID() int64 { c.selfMu.RLock(); defer c.selfMu.RUnlock(); return c.selfID }
func (c *Client) IsConnected() bool {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.sdk != nil
}

func (c *Client) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		// 先封死连接发布并终止拨号、重连和 SDK 事件流。
		c.stopTransport()
		c.connMu.Lock()
		sdk := c.sdk
		c.sdk = nil
		c.connMu.Unlock()
		if sdk != nil {
			closeErr = sdk.Close()
		}

		// 事件入口完全退出后，队列不会再新增或写入，可以安全关闭并排空。
		c.transportWG.Wait()
		c.workersMu.Lock()
		for groupID, queue := range c.workers {
			close(queue)
			delete(c.workers, groupID)
		}
		c.workersMu.Unlock()
		c.workerWG.Wait()

		if c.memberInfoCache != nil {
			c.memberInfoCache.Stop()
		}
	})
	return closeErr
}

func (c *Client) currentSDK() (*napcat.Client, error) {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	if c.sdk == nil {
		return nil, errors.New("未连接到 OneBot 服务")
	}
	return c.sdk, nil
}
