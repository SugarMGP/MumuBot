package onebot

import (
	"amu-bot/internal/config"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// Client OneBot WebSocket客户端
type Client struct {
	cfg      *config.Config
	conn     *websocket.Conn
	connMu   sync.Mutex
	handlers map[string][]EventHandler
	selfID   int64

	// 消息回调
	onMessage func(*GroupMessage)

	// 重连控制
	reconnecting bool
	stopCh       chan struct{}

	// API 调用响应等待
	echoCounter uint64
	pendingReqs sync.Map // map[string]chan *APIResponse
}

// EventHandler 事件处理器
type EventHandler func(event map[string]interface{})

// APIResponse OneBot API 响应
type APIResponse struct {
	Status  string      `json:"status"`  // ok / failed
	RetCode int         `json:"retcode"` // 0 表示成功
	Data    interface{} `json:"data"`    // 可以是 map 或 array
	Echo    string      `json:"echo"`
	Message string      `json:"message,omitempty"` // 错误信息
}

// DataMap 获取响应数据为 map 类型（用于普通 API）
func (r *APIResponse) DataMap() map[string]interface{} {
	if r.Data == nil {
		return nil
	}
	if m, ok := r.Data.(map[string]interface{}); ok {
		return m
	}
	return nil
}

// DataList 获取响应数据为数组类型（用于列表 API）
func (r *APIResponse) DataList() []interface{} {
	if r.Data == nil {
		return nil
	}
	if arr, ok := r.Data.([]interface{}); ok {
		return arr
	}
	return nil
}

// GroupMessage 群消息
type GroupMessage struct {
	MessageID   int64       `json:"message_id"`
	GroupID     int64       `json:"group_id"`
	UserID      int64       `json:"user_id"`
	Nickname    string      `json:"nickname"`
	Card        string      `json:"card"`              // 群名片
	Role        string      `json:"role"`              // 角色: owner/admin/member
	Content     string      `json:"content"`           // 纯文本内容
	RawMessage  string      `json:"raw_message"`       // 原始消息（CQ码格式）
	MentionAmu  bool        `json:"mention_amu"`       // 是否@机器人
	MentionAll  bool        `json:"mention_all"`       // 是否@全体成员
	Time        time.Time   `json:"time"`              // 消息时间
	MessageType string      `json:"message_type"`      // 消息类型
	Images      []ImageInfo `json:"images,omitempty"`  // 图片列表
	Faces       []FaceInfo  `json:"faces,omitempty"`   // 表情列表
	AtList      []int64     `json:"at_list,omitempty"` // @的用户列表
	Reply       *ReplyInfo  `json:"reply,omitempty"`   // 回复信息
}

// ImageInfo 图片信息
type ImageInfo struct {
	URL     string `json:"url"`
	File    string `json:"file"`
	Summary string `json:"summary,omitempty"` // 图片摘要/描述
	SubType int    `json:"sub_type"`          // 0普通图片 1表情包
}

// FaceInfo 表情信息
type FaceInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// ReplyInfo 回复信息
type ReplyInfo struct {
	MessageID int64  `json:"message_id"`
	Content   string `json:"content,omitempty"`   // 被回复消息内容
	SenderID  int64  `json:"sender_id,omitempty"` // 被回复消息发送者ID
	Nickname  string `json:"nickname,omitempty"`  // 被回复消息发送者昵称
}

// CardMessage 卡片消息解析结果
type CardMessage struct {
	App   string `json:"app"`   // 应用标识
	Title string `json:"title"` // 标题
	Desc  string `json:"desc"`  // 描述
	URL   string `json:"url"`   // 链接
}

// Format 格式化卡片消息为可读文本
func (c *CardMessage) Format() string {
	if c.URL != "" {
		return fmt.Sprintf("[卡片:%s - %s 链接:%s]", c.Title, c.Desc, c.URL)
	}
	if c.Desc != "" {
		return fmt.Sprintf("[卡片:%s - %s]", c.Title, c.Desc)
	}
	return fmt.Sprintf("[卡片:%s]", c.Title)
}

// EmojiReaction 表情回应
type EmojiReaction struct {
	EmojiID int `json:"emoji_id"`
	Count   int `json:"count"`
}

// GroupNotice 群公告
type GroupNotice struct {
	NoticeID    string `json:"notice_id"`
	SenderID    int64  `json:"sender_id"`
	PublishTime int64  `json:"publish_time"`
	Content     string `json:"content"`
}

// EssenceMessage 群精华消息
type EssenceMessage struct {
	MessageID    int64  `json:"message_id"`
	SenderID     int64  `json:"sender_id"`
	SenderNick   string `json:"sender_nick"`
	OperatorID   int64  `json:"operator_id"`
	OperatorNick string `json:"operator_nick"`
	OperatorTime int64  `json:"operator_time"`
	Content      string `json:"content"`
}

// GroupInfo 群信息
type GroupInfo struct {
	GroupID        int64  `json:"group_id"`
	GroupName      string `json:"group_name"`
	MemberCount    int    `json:"member_count"`
	MaxMemberCount int    `json:"max_member_count"`
}

// GroupMemberInfo 群成员信息
type GroupMemberInfo struct {
	GroupID         int64  `json:"group_id"`
	UserID          int64  `json:"user_id"`
	Nickname        string `json:"nickname"`
	Card            string `json:"card"`
	Role            string `json:"role"` // owner/admin/member
	JoinTime        int64  `json:"join_time"`
	LastSentTime    int64  `json:"last_sent_time"`
	Level           string `json:"level"`
	Title           string `json:"title"` // 专属头衔
	TitleExpireTime int64  `json:"title_expire_time"`
}

// LoginInfo 登录信息
type LoginInfo struct {
	UserID   int64  `json:"user_id"`
	Nickname string `json:"nickname"`
}

// NewClient 创建OneBot客户端
func NewClient(cfg *config.Config) *Client {
	return &Client{
		cfg:      cfg,
		handlers: make(map[string][]EventHandler),
		stopCh:   make(chan struct{}),
	}
}

// Connect 连接到OneBot服务
func (c *Client) Connect() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	header := make(map[string][]string)
	if c.cfg.OneBot.AccessToken != "" {
		header["Authorization"] = []string{"Bearer " + c.cfg.OneBot.AccessToken}
	}

	conn, _, err := websocket.DefaultDialer.Dial(c.cfg.OneBot.WsURL, header)
	if err != nil {
		return fmt.Errorf("WebSocket连接失败: %w", err)
	}

	c.conn = conn
	c.reconnecting = false

	// 启动消息接收循环
	go c.receiveLoop()

	zap.L().Info("已连接到OneBot", zap.String("url", c.cfg.OneBot.WsURL))
	return nil
}

// receiveLoop 消息接收循环
func (c *Client) receiveLoop() {
	for {
		select {
		case <-c.stopCh:
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
	if err := json.Unmarshal(data, &event); err != nil {
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
		if retCode, ok := event["retcode"].(float64); ok {
			resp.RetCode = int(retCode)
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
			if selfID, ok := event["self_id"].(float64); ok {
				c.selfID = int64(selfID)
				zap.L().Info("Bot已上线", zap.Int64("qq", c.selfID))
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
}

// handleRequestEvent 处理请求事件（加群/加好友请求）
func (c *Client) handleRequestEvent(event map[string]interface{}) {
	requestType, _ := event["request_type"].(string)
	zap.L().Debug("收到请求", zap.String("type", requestType))
}

// parseGroupMessage 解析群消息
func (c *Client) parseGroupMessage(event map[string]interface{}) *GroupMessage {
	msg := &GroupMessage{}

	// 消息时间
	if t, ok := event["time"].(float64); ok {
		msg.Time = time.Unix(int64(t), 0)
	} else {
		msg.Time = time.Now()
	}

	// 消息ID
	if msgID, ok := event["message_id"].(float64); ok {
		msg.MessageID = int64(msgID)
	}

	// 群ID
	if groupID, ok := event["group_id"].(float64); ok {
		msg.GroupID = int64(groupID)
	}

	// 发送者信息
	if sender, ok := event["sender"].(map[string]interface{}); ok {
		if userID, ok := sender["user_id"].(float64); ok {
			msg.UserID = int64(userID)
		}
		if nickname, ok := sender["nickname"].(string); ok {
			msg.Nickname = nickname
		}
		if card, ok := sender["card"].(string); ok {
			msg.Card = card
		}
		if role, ok := sender["role"].(string); ok {
			msg.Role = role
		}
	}

	// 原始消息
	if rawMsg, ok := event["raw_message"].(string); ok {
		msg.RawMessage = rawMsg
	}

	// 解析消息段，提取各类信息
	c.parseMessageSegments(event, msg)

	// 检查是否@机器人
	for _, atID := range msg.AtList {
		if atID == c.selfID {
			msg.MentionAmu = true
			break
		}
	}

	return msg
}

// parseMessageSegments 解析消息段，填充消息各字段
func (c *Client) parseMessageSegments(event map[string]interface{}, msg *GroupMessage) {
	message, ok := event["message"].([]interface{})
	if !ok {
		if raw, ok := event["raw_message"].(string); ok {
			msg.Content = raw
		}
		return
	}

	var textParts []string

	for _, seg := range message {
		segMap, ok := seg.(map[string]interface{})
		if !ok {
			continue
		}

		segType, _ := segMap["type"].(string)
		data, _ := segMap["data"].(map[string]interface{})
		if data == nil {
			continue
		}

		switch segType {
		case "text":
			if t, ok := data["text"].(string); ok {
				textParts = append(textParts, t)
			}

		case "image":
			img := ImageInfo{}
			if url, ok := data["url"].(string); ok {
				img.URL = url
			}
			if file, ok := data["file"].(string); ok {
				img.File = file
			}
			if summary, ok := data["summary"].(string); ok {
				img.Summary = summary
			}
			if subType, ok := data["sub_type"].(float64); ok {
				img.SubType = int(subType)
			}
			if img.URL != "" || img.File != "" {
				msg.Images = append(msg.Images, img)
			}

		case "face":
			face := FaceInfo{}
			// ID
			if id, ok := data["id"].(string); ok {
				face.ID, _ = strconv.Atoi(id)
			} else if id, ok := data["id"].(float64); ok {
				face.ID = int(id)
			}
			// 表情名称（NapCat 扩展字段）
			if name, ok := data["name"].(string); ok && name != "" {
				face.Name = name
			} else if text, ok := data["text"].(string); ok && text != "" {
				face.Name = text
			} else if raw, ok := data["raw"].(string); ok && raw != "" {
				face.Name = raw
			}
			msg.Faces = append(msg.Faces, face)

		case "at":
			if qq, ok := data["qq"].(string); ok {
				if qq == "all" {
					msg.MentionAll = true
				} else if qqID, err := strconv.ParseInt(qq, 10, 64); err == nil {
					msg.AtList = append(msg.AtList, qqID)
				}
			} else if qq, ok := data["qq"].(float64); ok {
				msg.AtList = append(msg.AtList, int64(qq))
			}

		case "reply":
			var replyMsgID int64
			if id, ok := data["id"].(string); ok {
				replyMsgID, _ = strconv.ParseInt(id, 10, 64)
			} else if id, ok := data["id"].(float64); ok {
				replyMsgID = int64(id)
			}
			if replyMsgID > 0 {
				msg.Reply = &ReplyInfo{MessageID: replyMsgID}
				// 同步获取被回复消息内容
				if replyData, err := c.GetMsg(replyMsgID); err == nil && replyData != nil {
					if rawMsg, ok := replyData["raw_message"].(string); ok {
						msg.Reply.Content = rawMsg
					}
					if sender, ok := replyData["sender"].(map[string]interface{}); ok {
						if uid, ok := sender["user_id"].(float64); ok {
							msg.Reply.SenderID = int64(uid)
						}
						if nick, ok := sender["nickname"].(string); ok {
							msg.Reply.Nickname = nick
						}
					}
				}
			}

		case "mface": // 商城表情/魔法表情
			img := ImageInfo{}
			if url, ok := data["url"].(string); ok {
				img.URL = url
			}
			if summary, ok := data["summary"].(string); ok {
				img.Summary = summary
			}
			img.SubType = 1 // 标记为表情包类型
			if img.URL != "" {
				msg.Images = append(msg.Images, img)
			}

		case "record": // 语音消息
			textParts = append(textParts, "[语音]")

		case "video": // 视频消息
			textParts = append(textParts, "[视频]")

		case "file": // 文件
			if name, ok := data["name"].(string); ok {
				textParts = append(textParts, fmt.Sprintf("[文件:%s]", name))
			} else {
				textParts = append(textParts, "[文件]")
			}

		case "json": // JSON卡片消息
			if jsonStr, ok := data["data"].(string); ok {
				card := parseCardMessage(jsonStr)
				if card != nil {
					textParts = append(textParts, card.Format())
				} else {
					textParts = append(textParts, "[卡片消息]")
				}
			} else {
				textParts = append(textParts, "[卡片消息]")
			}

		case "forward": // 合并转发
			textParts = append(textParts, "[合并转发]")
		}
	}

	// 合并文本内容
	for i, part := range textParts {
		if i > 0 {
			msg.Content += " "
		}
		msg.Content += part
	}
}

// OnMessage 设置消息回调
func (c *Client) OnMessage(handler func(*GroupMessage)) {
	c.onMessage = handler
}

// SendGroupMessage 发送群消息
func (c *Client) SendGroupMessage(groupID int64, content string) (int64, error) {
	resp, err := c.callAPI(context.Background(), "send_group_msg", map[string]interface{}{
		"group_id": groupID,
		"message":  content,
	})
	if err != nil {
		return 0, err
	}
	if data := resp.DataMap(); data != nil {
		if msgID, ok := data["message_id"].(float64); ok {
			return int64(msgID), nil
		}
	}
	return 0, nil
}

// SendGroupMessageReply 发送群消息（回复）
func (c *Client) SendGroupMessageReply(groupID int64, content string, replyTo int64) (int64, error) {
	// 使用消息段数组格式，更符合 OneBot 11 标准
	var message []map[string]interface{}
	if replyTo > 0 {
		message = append(message, map[string]interface{}{
			"type": "reply",
			"data": map[string]interface{}{"id": strconv.FormatInt(replyTo, 10)},
		})
	}
	message = append(message, map[string]interface{}{
		"type": "text",
		"data": map[string]interface{}{"text": content},
	})

	resp, err := c.callAPI(context.Background(), "send_group_msg", map[string]interface{}{
		"group_id": groupID,
		"message":  message,
	})
	if err != nil {
		return 0, err
	}
	if data := resp.DataMap(); data != nil {
		if msgID, ok := data["message_id"].(float64); ok {
			return int64(msgID), nil
		}
	}
	return 0, nil
}

// SendPrivateMessage 发送私聊消息
func (c *Client) SendPrivateMessage(userID int64, content string) (int64, error) {
	resp, err := c.callAPI(context.Background(), "send_private_msg", map[string]interface{}{
		"user_id": userID,
		"message": content,
	})
	if err != nil {
		return 0, err
	}
	if data := resp.DataMap(); data != nil {
		if msgID, ok := data["message_id"].(float64); ok {
			return int64(msgID), nil
		}
	}
	return 0, nil
}

// DeleteMsg 撤回消息
func (c *Client) DeleteMsg(messageID int64) error {
	_, err := c.callAPI(context.Background(), "delete_msg", map[string]interface{}{
		"message_id": messageID,
	})
	return err
}

// GetMsg 获取消息详情
func (c *Client) GetMsg(messageID int64) (map[string]interface{}, error) {
	resp, err := c.callAPI(context.Background(), "get_msg", map[string]interface{}{
		"message_id": messageID,
	})
	if err != nil {
		return nil, err
	}
	return resp.DataMap(), nil
}

// GetLoginInfo 获取登录号信息
func (c *Client) GetLoginInfo() (*LoginInfo, error) {
	resp, err := c.callAPI(context.Background(), "get_login_info", nil)
	if err != nil {
		return nil, err
	}
	data := resp.DataMap()
	if data == nil {
		return nil, fmt.Errorf("无效的响应数据")
	}
	info := &LoginInfo{}
	if userID, ok := data["user_id"].(float64); ok {
		info.UserID = int64(userID)
	}
	if nickname, ok := data["nickname"].(string); ok {
		info.Nickname = nickname
	}
	return info, nil
}

// GetGroupInfo 获取群信息
func (c *Client) GetGroupInfo(groupID int64, noCache bool) (*GroupInfo, error) {
	resp, err := c.callAPI(context.Background(), "get_group_info", map[string]interface{}{
		"group_id": groupID,
		"no_cache": noCache,
	})
	if err != nil {
		return nil, err
	}
	data := resp.DataMap()
	if data == nil {
		return nil, fmt.Errorf("无效的响应数据")
	}
	info := &GroupInfo{}
	if gid, ok := data["group_id"].(float64); ok {
		info.GroupID = int64(gid)
	}
	if name, ok := data["group_name"].(string); ok {
		info.GroupName = name
	}
	if count, ok := data["member_count"].(float64); ok {
		info.MemberCount = int(count)
	}
	if max, ok := data["max_member_count"].(float64); ok {
		info.MaxMemberCount = int(max)
	}
	return info, nil
}

// GetGroupMemberInfo 获取群成员信息
func (c *Client) GetGroupMemberInfo(groupID, userID int64, noCache bool) (*GroupMemberInfo, error) {
	resp, err := c.callAPI(context.Background(), "get_group_member_info", map[string]interface{}{
		"group_id": groupID,
		"user_id":  userID,
		"no_cache": noCache,
	})
	if err != nil {
		return nil, err
	}
	data := resp.DataMap()
	if data == nil {
		return nil, fmt.Errorf("无效的响应数据")
	}
	info := &GroupMemberInfo{}
	if gid, ok := data["group_id"].(float64); ok {
		info.GroupID = int64(gid)
	}
	if uid, ok := data["user_id"].(float64); ok {
		info.UserID = int64(uid)
	}
	if nickname, ok := data["nickname"].(string); ok {
		info.Nickname = nickname
	}
	if card, ok := data["card"].(string); ok {
		info.Card = card
	}
	if role, ok := data["role"].(string); ok {
		info.Role = role
	}
	if joinTime, ok := data["join_time"].(float64); ok {
		info.JoinTime = int64(joinTime)
	}
	if lastSentTime, ok := data["last_sent_time"].(float64); ok {
		info.LastSentTime = int64(lastSentTime)
	}
	if level, ok := data["level"].(string); ok {
		info.Level = level
	}
	if title, ok := data["title"].(string); ok {
		info.Title = title
	}
	return info, nil
}

// GetGroupMemberList 获取群成员列表
func (c *Client) GetGroupMemberList(groupID int64, noCache bool) ([]*GroupMemberInfo, error) {
	resp, err := c.callAPI(context.Background(), "get_group_member_list", map[string]interface{}{
		"group_id": groupID,
		"no_cache": noCache,
	})
	if err != nil {
		return nil, err
	}

	// 响应的 data 是数组
	dataList, ok := resp.Data.([]interface{})
	if !ok {
		return nil, fmt.Errorf("无效的响应数据格式")
	}

	var members []*GroupMemberInfo
	for _, item := range dataList {
		data, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		info := &GroupMemberInfo{}
		if gid, ok := data["group_id"].(float64); ok {
			info.GroupID = int64(gid)
		}
		if uid, ok := data["user_id"].(float64); ok {
			info.UserID = int64(uid)
		}
		if nickname, ok := data["nickname"].(string); ok {
			info.Nickname = nickname
		}
		if card, ok := data["card"].(string); ok {
			info.Card = card
		}
		if role, ok := data["role"].(string); ok {
			info.Role = role
		}
		if joinTime, ok := data["join_time"].(float64); ok {
			info.JoinTime = int64(joinTime)
		}
		if lastSentTime, ok := data["last_sent_time"].(float64); ok {
			info.LastSentTime = int64(lastSentTime)
		}
		if level, ok := data["level"].(string); ok {
			info.Level = level
		}
		if title, ok := data["title"].(string); ok {
			info.Title = title
		}
		members = append(members, info)
	}
	return members, nil
}

// SetMsgEmojiLike 对消息贴表情
func (c *Client) SetMsgEmojiLike(messageID int64, emojiID int) error {
	_, err := c.callAPI(context.Background(), "set_msg_emoji_like", map[string]interface{}{
		"message_id": messageID,
		"emoji_id":   emojiID,
	})
	return err
}

// MarkMsgAsRead 标记消息已读
func (c *Client) MarkMsgAsRead(messageID int64) error {
	_, err := c.callAPI(context.Background(), "mark_msg_as_read", map[string]interface{}{
		"message_id": messageID,
	})
	return err
}

// GroupPoke 群戳一戳
func (c *Client) GroupPoke(groupID, userID int64) error {
	_, err := c.callAPI(context.Background(), "group_poke", map[string]interface{}{
		"group_id": groupID,
		"user_id":  userID,
	})
	return err
}

// callAPI 调用 OneBot API（同步等待响应）
func (c *Client) callAPI(ctx context.Context, action string, params map[string]interface{}) (*APIResponse, error) {
	echo := fmt.Sprintf("%d", atomic.AddUint64(&c.echoCounter, 1))

	// 创建响应通道
	respCh := make(chan *APIResponse, 1)
	c.pendingReqs.Store(echo, respCh)
	defer func() {
		c.pendingReqs.Delete(echo)
		close(respCh)
	}()

	// 发送请求
	c.connMu.Lock()
	if c.conn == nil {
		c.connMu.Unlock()
		return nil, fmt.Errorf("未连接到OneBot服务")
	}

	req := map[string]interface{}{
		"action": action,
		"params": params,
		"echo":   echo,
	}
	data, err := json.Marshal(req)
	if err != nil {
		c.connMu.Unlock()
		return nil, err
	}

	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		c.connMu.Unlock()
		return nil, err
	}
	c.connMu.Unlock()

	// 等待响应（带超时）
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("API调用超时: %s", action)
	case resp := <-respCh:
		if resp.RetCode != 0 {
			return resp, fmt.Errorf("API调用失败[%d]: %s", resp.RetCode, resp.Message)
		}
		return resp, nil
	}
}

// handleDisconnect 处理断开连接
func (c *Client) handleDisconnect() {
	if c.reconnecting {
		return
	}
	c.reconnecting = true

	zap.L().Warn("连接断开，尝试重连...")

	interval := time.Duration(c.cfg.OneBot.ReconnectInterval) * time.Second
	for {
		select {
		case <-c.stopCh:
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

// GetSelfID 获取Bot的QQ号
func (c *Client) GetSelfID() int64 {
	return c.selfID
}

// SetSelfID 设置Bot的QQ号（用于初始化）
func (c *Client) SetSelfID(id int64) {
	c.selfID = id
}

// Close 关闭连接
func (c *Client) Close() error {
	close(c.stopCh)

	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// IsConnected 检查是否已连接
func (c *Client) IsConnected() bool {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	return c.conn != nil && !c.reconnecting
}

// parseCardMessage 解析JSON卡片消息
func parseCardMessage(jsonStr string) *CardMessage {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return nil
	}

	card := &CardMessage{}

	// 获取 app 类型
	if app, ok := data["app"].(string); ok {
		card.App = app
	}

	// 尝试从 meta 中提取信息（常见结构）
	if meta, ok := data["meta"].(map[string]interface{}); ok {
		// 遍历 meta 中的第一个子对象
		for _, v := range meta {
			if detail, ok := v.(map[string]interface{}); ok {
				if title, ok := detail["title"].(string); ok {
					card.Title = title
				}
				if desc, ok := detail["desc"].(string); ok {
					card.Desc = desc
				}
				if jumpUrl, ok := detail["jumpUrl"].(string); ok {
					card.URL = jumpUrl
				} else if qqdocurl, ok := detail["qqdocurl"].(string); ok {
					card.URL = qqdocurl
				}
				break
			}
		}
	}

	// 尝试从 prompt 获取标题（备用）
	if card.Title == "" {
		if prompt, ok := data["prompt"].(string); ok {
			card.Title = prompt
		}
	}

	// 尝试从 desc 获取描述（备用）
	if card.Desc == "" {
		if desc, ok := data["desc"].(string); ok {
			card.Desc = desc
		}
	}

	if card.Title == "" && card.Desc == "" {
		return nil
	}

	return card
}

// GetGroupNotice 获取群公告
func (c *Client) GetGroupNotice(groupID int64) ([]GroupNotice, error) {
	resp, err := c.callAPI(context.Background(), "_get_group_notice", map[string]interface{}{
		"group_id": groupID,
	})
	if err != nil {
		return nil, err
	}

	dataList := resp.DataList()
	if dataList == nil {
		return nil, nil
	}

	var notices []GroupNotice
	for _, item := range dataList {
		data, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		notice := GroupNotice{}
		if noticeID, ok := data["notice_id"].(string); ok {
			notice.NoticeID = noticeID
		}
		if senderID, ok := data["sender_id"].(float64); ok {
			notice.SenderID = int64(senderID)
		}
		if publishTime, ok := data["publish_time"].(float64); ok {
			notice.PublishTime = int64(publishTime)
		}
		if msg, ok := data["message"].(map[string]interface{}); ok {
			if text, ok := msg["text"].(string); ok {
				notice.Content = text
			}
		}
		notices = append(notices, notice)
	}
	return notices, nil
}

// GetEssenceMessages 获取群精华消息
func (c *Client) GetEssenceMessages(groupID int64) ([]EssenceMessage, error) {
	resp, err := c.callAPI(context.Background(), "get_essence_msg_list", map[string]interface{}{
		"group_id": groupID,
	})
	if err != nil {
		return nil, err
	}

	dataList := resp.DataList()
	if dataList == nil {
		return nil, nil
	}

	var messages []EssenceMessage
	for _, item := range dataList {
		data, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		msg := EssenceMessage{}
		if msgID, ok := data["message_id"].(float64); ok {
			msg.MessageID = int64(msgID)
		}
		if senderID, ok := data["sender_id"].(float64); ok {
			msg.SenderID = int64(senderID)
		}
		if senderNick, ok := data["sender_nick"].(string); ok {
			msg.SenderNick = senderNick
		}
		if operatorID, ok := data["operator_id"].(float64); ok {
			msg.OperatorID = int64(operatorID)
		}
		if operatorNick, ok := data["operator_nick"].(string); ok {
			msg.OperatorNick = operatorNick
		}
		if operatorTime, ok := data["operator_time"].(float64); ok {
			msg.OperatorTime = int64(operatorTime)
		}
		// 解析消息内容
		if content, ok := data["content"].([]interface{}); ok {
			msg.Content = extractTextFromSegments(content)
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// extractTextFromSegments 从消息段中提取文本内容
func extractTextFromSegments(segments []interface{}) string {
	var parts []string
	for _, seg := range segments {
		segMap, ok := seg.(map[string]interface{})
		if !ok {
			continue
		}
		segType, _ := segMap["type"].(string)
		data, _ := segMap["data"].(map[string]interface{})
		if data == nil {
			continue
		}
		switch segType {
		case "text":
			if t, ok := data["text"].(string); ok {
				parts = append(parts, t)
			}
		case "image":
			parts = append(parts, "[图片]")
		case "face":
			parts = append(parts, "[表情]")
		}
	}
	return strings.Join(parts, "")
}

// GetMessageReactions 获取消息的表情回应
func (c *Client) GetMessageReactions(messageID int64) ([]EmojiReaction, error) {
	// 通过 get_msg 获取消息详情，其中包含 emoji_likes_list
	msgData, err := c.GetMsg(messageID)
	if err != nil {
		return nil, err
	}

	emojiList, ok := msgData["emoji_likes_list"].([]interface{})
	if !ok || len(emojiList) == 0 {
		return nil, nil
	}

	var reactions []EmojiReaction
	for _, item := range emojiList {
		emojiData, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		reaction := EmojiReaction{}
		if emojiID, ok := emojiData["emoji_id"].(float64); ok {
			reaction.EmojiID = int(emojiID)
		} else if emojiID, ok := emojiData["emoji_id"].(string); ok {
			reaction.EmojiID, _ = strconv.Atoi(emojiID)
		}
		if count, ok := emojiData["count"].(float64); ok {
			reaction.Count = int(count)
		}
		if reaction.EmojiID > 0 {
			reactions = append(reactions, reaction)
		}
	}
	return reactions, nil
}

// SendImageMessage 发送图片/表情包消息
// filePath: 本地文件绝对路径
// isSticker: true 时作为表情包发送 (sub_type=1)
func (c *Client) SendImageMessage(groupID int64, filePath string, isSticker bool) (int64, error) {
	subType := 0
	if isSticker {
		subType = 1
	}

	message := []map[string]interface{}{
		{
			"type": "image",
			"data": map[string]interface{}{
				"file":     "file:///" + filePath,
				"sub_type": subType,
			},
		},
	}

	resp, err := c.callAPI(context.Background(), "send_group_msg", map[string]interface{}{
		"group_id": groupID,
		"message":  message,
	})
	if err != nil {
		return 0, err
	}
	if data := resp.DataMap(); data != nil {
		if msgID, ok := data["message_id"].(float64); ok {
			return int64(msgID), nil
		}
	}
	return 0, nil
}