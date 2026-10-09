package onebot

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/jellydator/ttlcache/v3"
	"github.com/zjutjh/onebot-sdk/event"
	"github.com/zjutjh/onebot-sdk/message"
)

// parseGroupMessage 按群目标解析群消息
func (c *Client) parseGroupMessage(ev *event.GroupMessage) *ConversationMessage {
	if ev.GroupID <= 0 || ev.Sender.UserID <= 0 {
		return nil
	}
	msg := &ConversationMessage{ConversationKind: "group", TargetID: int64(ev.GroupID)}
	c.parseConversationMessage(msg, ev.Time(), int64(ev.MessageID), int64(ev.Sender.UserID), ev.Sender.Nickname, ev.Sender.Card, ev.Message)
	return msg
}

// parsePrivateMessage 按好友目标解析私聊消息，target_id 由 SDK 管线按方言补齐
func (c *Client) parsePrivateMessage(ev *event.PrivateMessage) *ConversationMessage {
	if ev.TargetID <= 0 || ev.Sender.UserID <= 0 {
		return nil
	}
	msg := &ConversationMessage{ConversationKind: "private", TargetID: int64(ev.TargetID), IsMentioned: true}
	c.parseConversationMessage(msg, ev.Time(), int64(ev.MessageID), int64(ev.Sender.UserID), ev.Sender.Nickname, "", ev.Message)
	return msg
}

// parseConversationMessage 填充公共字段并解析消息段，进入前已确定会话作用域
func (c *Client) parseConversationMessage(msg *ConversationMessage, eventTime int64, messageID, userID int64, nickname, groupCard string, chain message.Chain) {
	if eventTime > 0 {
		msg.Time = time.Unix(eventTime, 0)
	} else {
		msg.Time = time.Now()
	}
	msg.MessageID = messageID
	msg.UserID = userID
	msg.Nickname = nickname
	msg.GroupCard = groupCard
	parseMessageSegments(chain, msg)
	selfID := c.GetSelfID()
	for _, atID := range msg.AtList {
		if selfID > 0 && atID == selfID {
			msg.IsMentioned = true
			break
		}
	}
}

// parseMessageSegments 解析强类型消息段，填充消息各字段
func parseMessageSegments(chain message.Chain, msg *ConversationMessage) {
	var textParts []string

	for _, seg := range chain {
		switch data := seg.Data.(type) {
		case message.TextData:
			textParts = append(textParts, data.Text)
			msg.MessageParts = append(msg.MessageParts, MessagePart{Kind: "text", Text: data.Text})

		case message.AtData:
			qqID, ok := parseAtTarget(data.QQ)
			if !ok {
				continue
			}
			msg.AtList = append(msg.AtList, qqID)
			msg.MessageParts = append(msg.MessageParts, MessagePart{Kind: "at", AtUserID: qqID})
			if qqID > 0 && data.Name != "" {
				if msg.AtNames == nil {
					msg.AtNames = make(map[int64]string)
				}
				msg.AtNames[qqID] = data.Name
			}

		case message.FaceData:
			face := FaceInfo{}
			// 表情名称不在强类型字段内，展示层回退为 [表情:ID]
			if id, ok := parseStrNum(data.ID); ok {
				face.ID = int(id)
			}
			msg.Faces = append(msg.Faces, face)
			msg.MessageParts = append(msg.MessageParts, MessagePart{Kind: "face", Index: len(msg.Faces) - 1})

		case message.ReplyData:
			msg.Reply = &ReplyInfo{MessageID: int64(data.ID)}
			msg.MessageParts = append(msg.MessageParts, MessagePart{Kind: "reply"})

		case message.ImageData:
			if data.URL == "" && data.File == "" {
				continue
			}
			img := ImageInfo{URL: data.URL, File: data.File}
			if subType, ok := parseStrNum(data.SubType); ok {
				img.SubType = int(subType)
			}
			msg.Images = append(msg.Images, img)
			msg.MessageParts = append(msg.MessageParts, MessagePart{Kind: "image", Index: len(msg.Images) - 1})

		case message.MFaceData: // 商城表情/魔法表情
			img := ImageInfo{Desc: data.Summary, SubType: 1} // 标记为表情包类型
			msg.Images = append(msg.Images, img)
			msg.MessageParts = append(msg.MessageParts, MessagePart{Kind: "image", Index: len(msg.Images) - 1})

		case message.RecordData: // 语音消息
			msg.MessageParts = append(msg.MessageParts, MessagePart{Kind: "record"})

		case message.VideoData:
			if data.URL == "" && data.File == "" {
				continue
			}
			vid := VideoInfo{URL: data.URL, File: data.File}
			msg.Videos = append(msg.Videos, vid)
			msg.MessageParts = append(msg.MessageParts, MessagePart{Kind: "video", Index: len(msg.Videos) - 1})

		case message.FileData: // 文件
			// 强类型文件段只有 file_id，没有文件名，展示层回退为 [文件]
			msg.FileNames = append(msg.FileNames, "")
			msg.MessageParts = append(msg.MessageParts, MessagePart{Kind: "file", Index: len(msg.FileNames) - 1})

		case message.JSONData: // JSON 卡片消息
			if card := parseCardMessage(data.Data); card != nil {
				msg.Cards = append(msg.Cards, *card)
				msg.MessageParts = append(msg.MessageParts, MessagePart{Kind: "card", Index: len(msg.Cards) - 1})
			}

		case message.ForwardData: // 合并转发，内容由 SDK 管线按方言补拉
			if len(data.Content) > 0 {
				start := len(msg.ForwardContent)
				msg.ForwardContent = append(msg.ForwardContent, data.Content...)
				msg.MessageParts = append(msg.MessageParts, MessagePart{Kind: "forward", Index: start, Count: len(data.Content)})
			} else {
				// 补拉失败或缺少内容时保留占位，不伪造转发内容
				msg.MessageParts = append(msg.MessageParts, MessagePart{Kind: "text", Text: "[合并转发消息]"})
			}
		}
	}

	// 合并文本内容
	msg.Content = strings.Join(textParts, " ")
}

// parseCardMessage 解析JSON卡片消息
func parseCardMessage(jsonStr string) *CardMessage {
	var data map[string]interface{}
	if err := sonic.UnmarshalString(jsonStr, &data); err != nil {
		return nil
	}

	card := &CardMessage{}

	// 尝试从 meta 中提取信息（常见结构）
	if meta, ok := data["meta"].(map[string]interface{}); ok {
		// 按键名排序后取第一个子对象，避免 map 遍历顺序不稳定导致同一条卡片解析结果不同
		keys := make([]string, 0, len(meta))
		for k := range meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if detail, ok := meta[k].(map[string]interface{}); ok {
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

// extractTextFromSegments 从原始消息段数组中提取文本内容
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
		case "record":
			parts = append(parts, "[语音]")
		case "video":
			parts = append(parts, "[视频]")
		case "file":
			parts = append(parts, "[文件]")
		case "at":
			qq, _ := data["qq"].(string)
			if qq == "all" {
				parts = append(parts, "@全体成员")
			} else if qq != "" {
				parts = append(parts, "@"+qq)
			}
		case "json":
			parts = append(parts, "[卡片消息]")
		case "forward":
			parts = append(parts, "[合并转发]")
		}
	}
	return strings.Join(parts, "")
}

// parseAtTarget 解析 @ 段的 QQ 号，"all" 表示全体成员
func parseAtTarget(qq message.StrNum) (int64, bool) {
	if qq == "all" {
		return AtAllUserID, true
	}
	return parseStrNum(qq)
}

// parseStrNum 解析 SDK 的字符串数字形态
func parseStrNum(v message.StrNum) (int64, bool) {
	id, err := strconv.ParseInt(string(v), 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func newGroupMemberInfoCache() *ttlcache.Cache[string, *GroupMemberInfo] {
	return ttlcache.New(
		ttlcache.WithTTL[string, *GroupMemberInfo](10*time.Minute),
		ttlcache.WithCapacity[string, *GroupMemberInfo](2048),
		ttlcache.WithDisableTouchOnHit[string, *GroupMemberInfo](),
	)
}

func groupMemberCacheKey(groupID, userID int64) string {
	return strconv.FormatInt(groupID, 10) + ":" + strconv.FormatInt(userID, 10)
}

// ParseIDFromRaw 从生成代码保留的原始联合字段中解析整数 ID
func ParseIDFromRaw(raw []byte) int64 {
	var id int64
	if err := sonic.Unmarshal(raw, &id); err != nil {
		return 0
	}
	return id
}
