package onebot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/jellydator/ttlcache/v3"
	ob "github.com/zjutjh/onebot-sdk"
	"github.com/zjutjh/onebot-sdk/api"
	"github.com/zjutjh/onebot-sdk/message"
)

// apiClient 取当前连接的强类型 API 客户端
func (c *Client) apiClient() (*api.Client, error) {
	sdk, err := c.currentSDK()
	if err != nil {
		return nil, err
	}
	return sdk.API(), nil
}

// rawUnion 把取值编码进生成代码的原始联合字段
func rawUnion[T ~struct {
	Raw json.RawMessage
}](value any) T {
	raw, _ := sonic.Marshal(value)
	return T{Raw: raw}
}

// rawUnionPtr 同 rawUnion，用于可选的指针形联合字段
func rawUnionPtr[T ~struct {
	Raw json.RawMessage
}](value any) *T {
	v := rawUnion[T](value)
	return &v
}

func oneBotID(id int64) string { return strconv.FormatInt(id, 10) }

// sendChain 发送消息段链并返回消息编号与该会话的运行时顺序序号
func (c *Client) sendChain(ctx context.Context, kind string, targetID int64, chain message.Chain) (int64, uint64, error) {
	client, err := c.apiClient()
	if err != nil {
		return 0, 0, err
	}
	msgUnion, err := api.NewOB11Message(chain)
	if err != nil {
		return 0, 0, fmt.Errorf("编码消息段失败: %w", err)
	}
	var msgID int64
	switch kind {
	case "group":
		resp, err := client.SendGroupMsg(ctx, api.SendGroupMsgRequest{GroupID: message.ID(targetID), Message: msgUnion})
		if err != nil {
			return 0, 0, err
		}
		msgID = int64(resp.MessageID)
	case "private":
		resp, err := client.SendPrivateMsg(ctx, api.SendPrivateMsgRequest{UserID: message.ID(targetID), Message: msgUnion})
		if err != nil {
			return 0, 0, err
		}
		msgID = int64(resp.MessageID)
	default:
		return 0, 0, fmt.Errorf("未知会话类型: %s", kind)
	}
	return msgID, c.nextArrivalSeq(kind, targetID), nil
}

// SendGroupMessage 发送群消息并返回群内运行时顺序序号
func (c *Client) SendGroupMessage(ctx context.Context, groupID int64, content string, replyTo int64, mentions []int64) (int64, uint64, error) {
	var chain message.Chain
	if replyTo != 0 {
		chain = append(chain, message.Reply(replyTo))
	}
	for _, uid := range mentions {
		if uid <= 0 {
			continue
		}
		chain = append(chain, message.At(uid), message.Text(" "))
	}
	if content != "" {
		chain = append(chain, message.Text(content))
	}
	return c.sendChain(ctx, "group", groupID, chain)
}

// SendPrivateMessage 发送好友私聊消息并返回私聊会话顺序
func (c *Client) SendPrivateMessage(ctx context.Context, userID int64, content string, replyTo int64) (int64, uint64, error) {
	if userID <= 0 {
		return 0, 0, fmt.Errorf("好友 QQ 无效")
	}
	var chain message.Chain
	if replyTo != 0 {
		chain = append(chain, message.Reply(replyTo))
	}
	chain = append(chain, message.Text(content))
	return c.sendChain(ctx, "private", userID, chain)
}

// buildImageSegment 构造图片/表情包消息段：本地文件统一转 base64，避免后端读不到路径
func buildImageSegment(filePath string, isSticker bool) (message.Segment, error) {
	file := filePath
	trimmed := strings.ToLower(strings.TrimSpace(filePath))
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return message.Segment{}, fmt.Errorf("读取待发送图片失败: %w", err)
		}
		file = "base64://" + base64.StdEncoding.EncodeToString(data)
	}
	subType := 0
	if isSticker {
		subType = 1
	}
	return message.Segment{Type: "image", Data: message.ImageData{
		File:    file,
		SubType: message.StrNum(strconv.Itoa(subType)),
	}}, nil
}

// SendImageMessage 发送图片/表情包到群聊
// filePath: 本地文件绝对路径或图片 URL
// isSticker: true 时作为表情包发送 (sub_type=1)
func (c *Client) SendImageMessage(ctx context.Context, groupID int64, filePath string, isSticker bool) (int64, uint64, error) {
	segment, err := buildImageSegment(filePath, isSticker)
	if err != nil {
		return 0, 0, err
	}
	return c.sendChain(ctx, "group", groupID, message.ChainOf(segment))
}

// SendPrivateImageMessage 发送图片/表情包到好友私聊
func (c *Client) SendPrivateImageMessage(ctx context.Context, userID int64, filePath string, isSticker bool) (int64, uint64, error) {
	if userID <= 0 {
		return 0, 0, fmt.Errorf("私聊图片目标无效")
	}
	segment, err := buildImageSegment(filePath, isSticker)
	if err != nil {
		return 0, 0, err
	}
	return c.sendChain(ctx, "private", userID, message.ChainOf(segment))
}

// DeleteMsg 撤回消息
func (c *Client) DeleteMsg(ctx context.Context, messageID int64) error {
	client, err := c.apiClient()
	if err != nil {
		return err
	}
	// message_id 是不透明外部定位符，保持字符串形态等值传递
	_, err = client.DeleteMsg(ctx, api.DeleteMsgRequest{
		MessageID: rawUnion[api.DeleteMsgRequestMessageIDUnion](oneBotID(messageID)),
	})
	return err
}

// GetMsg 获取消息详情
func (c *Client) GetMsg(ctx context.Context, messageID int64) (*api.GetMsgResponse, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	return client.GetMsg(ctx, api.GetMsgRequest{
		MessageID: rawUnion[api.GetMsgRequestMessageIDUnion](oneBotID(messageID)),
	})
}

// GetGroupInfo 获取群信息
func (c *Client) GetGroupInfo(ctx context.Context, groupID int64) (*GroupInfo, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.GetGroupInfo(ctx, api.GetGroupInfoRequest{GroupID: message.ID(groupID)})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.GroupID <= 0 {
		return nil, fmt.Errorf("get_group_info 返回无效的 group_id")
	}
	info := &GroupInfo{GroupID: int64(resp.GroupID), GroupName: resp.GroupName}
	if resp.MemberCount != nil {
		info.MemberCount = int(*resp.MemberCount)
	}
	if resp.MaxMemberCount != nil {
		info.MaxMemberCount = int(*resp.MaxMemberCount)
	}
	return info, nil
}

// GetGroupMemberInfo 获取群成员信息
func (c *Client) GetGroupMemberInfo(ctx context.Context, groupID, userID int64, noCache bool) (*GroupMemberInfo, error) {
	cacheKey := groupMemberCacheKey(groupID, userID)
	if !noCache {
		if item := c.memberInfoCache.Get(cacheKey); item != nil {
			return item.Value(), nil
		}
	}
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.GetGroupMemberInfo(ctx, api.GetGroupMemberInfoRequest{
		GroupID: message.ID(groupID),
		UserID:  message.ID(userID),
		NoCache: rawUnionPtr[api.GetGroupMemberInfoRequestNoCacheUnion](noCache),
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.GroupID <= 0 || resp.UserID <= 0 {
		return nil, fmt.Errorf("get_group_member_info 返回无效的成员标识")
	}
	info := &GroupMemberInfo{
		GroupID:  int64(resp.GroupID),
		UserID:   int64(resp.UserID),
		Nickname: resp.Nickname,
	}
	if resp.Card != nil {
		info.Card = *resp.Card
	}
	if resp.Role != nil {
		info.Role = *resp.Role
	}
	if resp.JoinTime != nil {
		info.JoinTime = int64(*resp.JoinTime)
	}
	if resp.LastSentTime != nil {
		info.LastSentTime = int64(*resp.LastSentTime)
	}
	if resp.Level != nil {
		info.Level = *resp.Level
	}
	if resp.Title != nil {
		info.Title = *resp.Title
	}
	c.memberInfoCache.Set(cacheKey, info, ttlcache.DefaultTTL)
	return info, nil
}

// SetMsgEmojiLike 对消息贴表情
func (c *Client) SetMsgEmojiLike(ctx context.Context, messageID int64, emojiID int) error {
	client, err := c.apiClient()
	if err != nil {
		return err
	}
	_, err = client.SetMsgEmojiLike(ctx, api.SetMsgEmojiLikeRequest{
		MessageID: rawUnion[api.SetMsgEmojiLikeRequestMessageIDUnion](oneBotID(messageID)),
		EmojiID:   rawUnion[api.SetMsgEmojiLikeRequestEmojiIDUnion](emojiID),
		Set:       rawUnionPtr[api.SetMsgEmojiLikeRequestSetUnion](true),
	})
	return err
}

// MarkMsgAsRead 标记消息已读
func (c *Client) MarkMsgAsRead(ctx context.Context, messageID int64) error {
	client, err := c.apiClient()
	if err != nil {
		return err
	}
	_, err = client.MarkMsgAsRead(ctx, api.MarkMsgAsReadRequest{MessageID: message.ID(messageID)})
	return err
}

// MarkPrivateMsgAsRead 标记好友私聊消息已读
func (c *Client) MarkPrivateMsgAsRead(ctx context.Context, userID, messageID int64) error {
	client, err := c.apiClient()
	if err != nil {
		return err
	}
	_, err = client.MarkPrivateMsgAsRead(ctx, api.MarkPrivateMsgAsReadRequest{
		UserID:    rawUnionPtr[api.MarkPrivateMsgAsReadRequestUserIDUnion](message.ID(userID)),
		MessageID: message.ID(messageID),
	})
	return err
}

// FriendPoke 戳一戳好友（私聊）
func (c *Client) FriendPoke(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return fmt.Errorf("好友账号无效")
	}
	client, err := c.apiClient()
	if err != nil {
		return err
	}
	_, err = client.FriendPoke(ctx, api.FriendPokeRequest{UserID: message.ID(userID)})
	return err
}

// GroupPoke 群戳一戳
func (c *Client) GroupPoke(ctx context.Context, groupID, userID int64) error {
	client, err := c.apiClient()
	if err != nil {
		return err
	}
	_, err = client.GroupPoke(ctx, api.GroupPokeRequest{GroupID: message.ID(groupID), UserID: message.ID(userID)})
	return err
}

// GetGroupNotice 获取群公告
func (c *Client) GetGroupNotice(ctx context.Context, groupID int64) ([]GroupNotice, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.UnderscoreGetGroupNotice(ctx, api.UnderscoreGetGroupNoticeRequest{GroupID: message.ID(groupID)})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	var notices []GroupNotice
	for _, item := range *resp {
		notice := GroupNotice{
			NoticeID:    item.NoticeID,
			SenderID:    int64(item.SenderID),
			PublishTime: int64(item.PublishTime),
			Content:     item.Message.Text,
		}
		notices = append(notices, notice)
	}
	return notices, nil
}

// GetEssenceMessages 获取群精华消息
func (c *Client) GetEssenceMessages(ctx context.Context, groupID int64) ([]EssenceMessage, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.GetEssenceMsgList(ctx, api.GetEssenceMsgListRequest{GroupID: message.ID(groupID)})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	var messages []EssenceMessage
	for i, item := range *resp {
		if item.MessageID == 0 {
			return nil, fmt.Errorf("get_essence_msg_list 第 %d 项返回无效的 message_id", i)
		}
		msg := EssenceMessage{
			MessageID:    int64(item.MessageID),
			SenderNick:   item.SenderNick,
			OperatorNick: item.OperatorNick,
			OperatorTime: int64(item.OperatorTime),
			Content:      extractTextFromSegments(item.Content),
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// GetMessageReactions 获取消息的表情回应，方言差异由 SDK 门面归一
func (c *Client) GetMessageReactions(ctx context.Context, messageID int64) ([]ob.EmojiReaction, error) {
	sdk, err := c.currentSDK()
	if err != nil {
		return nil, err
	}
	return sdk.GetMessageReactions(ctx, messageID)
}
