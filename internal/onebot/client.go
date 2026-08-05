package onebot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"mumu-bot/internal/config"
	"mumu-bot/internal/utils"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/jellydator/ttlcache/v3"
	napcat "github.com/zjutjh/napcat-sdk"
	"github.com/zjutjh/napcat-sdk/api"
	"go.uber.org/zap"
)

type groupEvent struct {
	event      map[string]interface{}
	receivedAt time.Time
	arrivalSeq uint64
}

type Client struct {
	transportCtx  context.Context
	stopTransport context.CancelFunc
	closeOnce     sync.Once

	connMu     sync.RWMutex
	sdk        *napcat.Client
	generation uint64

	mutedMu    sync.RWMutex
	mutedUntil map[int64]time.Time
	selfID     atomic.Int64

	memberInfoCache *ttlcache.Cache[string, *GroupMemberInfo]
	onMessage       func(*GroupMessage)
	onRecall        func(groupID, messageID, operatorID int64, arrivalSeq uint64)
	onConnected     func()
	transportWG     sync.WaitGroup
	eventWG         sync.WaitGroup
	seqMu           sync.Mutex
	groupSeq        map[int64]uint64
}

func NewClient() *Client {
	transportCtx, stopTransport := context.WithCancel(context.Background())
	cache := newGroupMemberInfoCache()
	c := &Client{
		transportCtx:    transportCtx,
		stopTransport:   stopTransport,
		mutedUntil:      make(map[int64]time.Time),
		memberInfoCache: cache,
		groupSeq:        make(map[int64]uint64),
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
	sdk, err := napcat.DialWebSocket(c.transportCtx, cfg.OneBot.WsURL, napcat.WithToken(cfg.OneBot.AccessToken), napcat.WithRequestTimeout(30*time.Second), napcat.WithEventBuffer(1024), napcat.WithEventDeliveryTimeout(time.Second))
	if err != nil {
		return fmt.Errorf("WebSocket连接失败: %w", err)
	}
	login, err := sdk.API().GetLoginInfo(c.transportCtx, api.GetLoginInfoRequest{})
	if err != nil {
		_ = sdk.Close()
		return fmt.Errorf("获取OneBot登录账号失败: %w", err)
	}
	if login == nil || login.UserID <= 0 || login.UserID > 1<<53-1 || math.Trunc(login.UserID) != login.UserID {
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
	old := c.sdk
	c.sdk = sdk
	c.generation++
	generation := c.generation
	c.transportWG.Add(1)
	c.connMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	if c.onConnected != nil {
		c.onConnected()
	}
	go func() {
		defer c.transportWG.Done()
		c.consumeEvents(sdk, generation)
	}()
	return nil
}

func (c *Client) consumeEvents(sdk *napcat.Client, generation uint64) {
	for ev := range sdk.Events() {
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
	receivedAt := time.Now()
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
		return
	case "notice":
		notice, _ := event["notice_type"].(string)
		sub, _ := event["sub_type"].(string)
		if notice == "group_ban" {
			c.handleNoticeEvent(event, receivedAt, 0)
			return
		}
		if notice != "group_recall" && (notice != "notify" || sub != "poke") {
			return
		}
		// 按事件类型确认对应业务回调已就绪，未就绪则直接丢弃且不分配序号，
		// 保证每个已分配序号的事件最终都能进入业务回调消费。
		if notice == "group_recall" {
			if c.onRecall == nil {
				return
			}
		} else if c.onMessage == nil {
			return
		}
	case "request":
		c.handleRequestEvent(event)
		return
	case "message":
		if event["message_type"] != "group" {
			return
		}
		if c.onMessage == nil {
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
	c.dispatchEvent(groupID, groupEvent{event: event, receivedAt: receivedAt})
}

// nextArrivalSeq 为该群分配递增到达序号，事件入口单线程调用，序号即推送顺序。
func (c *Client) nextArrivalSeq(groupID int64) uint64 {
	c.seqMu.Lock()
	defer c.seqMu.Unlock()
	c.groupSeq[groupID]++
	return c.groupSeq[groupID]
}

// dispatchEvent 每条事件直接并发处理，不做按群串行或并发上限。
// 事件入口已确认对应业务回调就绪，分发后该事件必然进入业务回调消费序号。
func (c *Client) dispatchEvent(groupID int64, event groupEvent) {
	event.arrivalSeq = c.nextArrivalSeq(groupID)
	c.eventWG.Add(1)
	go func() {
		defer c.eventWG.Done()
		c.handleGroupEvent(event)
	}()
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
	interval := time.Duration(config.Get().OneBot.ReconnectInterval) * time.Second
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
			zap.L().Info("已连接到 OneBot", zap.String("url", config.Get().OneBot.WsURL), zap.Int64("self_id", c.GetSelfID()))
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

func (c *Client) handleGroupEvent(queued groupEvent) {
	switch queued.event["post_type"] {
	case "message":
		c.handleMessageEvent(queued.event, queued.receivedAt, queued.arrivalSeq)
	case "notice":
		c.handleNoticeEvent(queued.event, queued.receivedAt, queued.arrivalSeq)
	}
}

func (c *Client) handleMessageEvent(event map[string]interface{}, receivedAt time.Time, arrivalSeq uint64) {
	msg := c.parseGroupMessage(event)
	if msg == nil {
		// 解析失败：记录现场并构造占位消息消费到达序号，避免上层提交重排器死等。
		groupID, _ := utils.ParseInt64Value(event["group_id"])
		messageID, _ := utils.ParseInt64Value(event["message_id"])
		zap.L().Warn("群消息段解析失败，已构造占位消息", zap.Int64("group_id", groupID), zap.Int64("message_id", messageID), zap.Any("post_type", event["post_type"]))
		msg = &GroupMessage{GroupID: groupID, ParseFailed: true}
	}
	msg.ReceivedAt = receivedAt
	msg.ArrivalSeq = arrivalSeq
	c.onMessage(msg)
}
func (c *Client) handleNoticeEvent(event map[string]interface{}, receivedAt time.Time, arrivalSeq uint64) {
	notice, _ := event["notice_type"].(string)
	sub, _ := event["sub_type"].(string)
	switch {
	case notice == "group_ban":
		c.handleGroupBanNotice(event, sub)
	case notice == "notify" && sub == "poke":
		c.handleGroupPokeNotice(event, receivedAt, arrivalSeq)
	case notice == "group_recall":
		c.handleGroupRecallNotice(event, arrivalSeq)
	}
}

func (c *Client) handleGroupPokeNotice(event map[string]interface{}, receivedAt time.Time, arrivalSeq uint64) {
	// 有效性由业务层校验：无效戳一戳也会通过占位路径消费序号，不在此处提前返回。
	groupID, _ := utils.ParseInt64Value(event["group_id"])
	userID, _ := utils.ParseInt64Value(event["user_id"])
	targetID, _ := utils.ParseInt64Value(event["target_id"])
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
		ReceivedAt:  receivedAt,
		ArrivalSeq:  arrivalSeq,
	})
}

func (c *Client) handleGroupRecallNotice(event map[string]interface{}, arrivalSeq uint64) {
	// 有效性由业务层校验（无效撤回会消费序号后跳过）。
	groupID, _ := utils.ParseInt64Value(event["group_id"])
	messageID, _ := utils.ParseInt64Value(event["message_id"])
	operatorID, _ := utils.ParseInt64Value(event["operator_id"])
	c.onRecall(groupID, messageID, operatorID, arrivalSeq)
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
func (c *Client) OnRecall(handler func(groupID, messageID, operatorID int64, arrivalSeq uint64)) {
	c.onRecall = handler
}
func (c *Client) OnConnected(handler func()) { c.onConnected = handler }
func (c *Client) GetSelfID() int64           { return c.selfID.Load() }
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

		// 事件入口完全退出后，等待所有已分发的并发事件处理完成。
		// 注意：不清零 selfID——Agent 停机排空提交队列时仍需用真实账号
		// 区分机器人自身消息；账号未就绪的语义由断线重连路径负责。
		c.transportWG.Wait()
		c.eventWG.Wait()

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
