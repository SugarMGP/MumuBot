package onebot

import (
	"fmt"
	"mumu-bot/internal/utils"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/jellydator/ttlcache/v3"
)

// parseGroupMessage 解析群消息
func (c *Client) parseGroupMessage(event map[string]interface{}) *GroupMessage {
	msg := &GroupMessage{}

	// 消息时间
	if t, ok := utils.ParseInt64Value(event["time"]); ok {
		msg.Time = time.Unix(t, 0)
	} else {
		msg.Time = time.Now()
	}

	// 消息 ID
	if msgID, ok := utils.ParseInt64Value(event["message_id"]); ok {
		msg.MessageID = msgID
	}

	// 群ID
	if groupID, ok := utils.ParseInt64Value(event["group_id"]); ok {
		msg.GroupID = groupID
	}

	// 发送者信息
	if sender, ok := event["sender"].(map[string]interface{}); ok {
		if userID, ok := utils.ParseInt64Value(sender["user_id"]); ok {
			msg.UserID = userID
		}
		if nickname, ok := sender["nickname"].(string); ok {
			msg.Nickname = nickname
		}
		if card, ok := sender["card"].(string); ok {
			msg.GroupCard = card
		}
	}
	if msg.UserID <= 0 {
		return nil
	}

	// 解析消息段，提取各类信息
	if !c.parseMessageSegments(event, msg) {
		return nil
	}

	// 检查是否@机器人
	selfID := c.GetSelfID()
	for _, atID := range msg.AtList {
		if atID == selfID {
			msg.IsMentioned = true
			break
		}
	}

	return msg
}

// parseMessageSegments 解析消息段，填充消息各字段
func (c *Client) parseMessageSegments(event map[string]interface{}, msg *GroupMessage) bool {
	message, ok := event["message"].([]interface{})
	if !ok {
		if raw, ok := event["raw_message"].(string); ok {
			msg.Content = raw
		}
		return true
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
			if subType, ok := parseInt(data["sub_type"]); ok {
				img.SubType = subType
			}
			if img.URL != "" || img.File != "" {
				msg.Images = append(msg.Images, img)
			}

		case "face":
			face := FaceInfo{}
			// ID
			if id, ok := parseInt(data["id"]); ok {
				face.ID = id
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
			qqID, ok := parseAtSegmentForGroup(data)
			if ok {
				msg.AtList = append(msg.AtList, qqID)
				if qqID > 0 {
					if displayName := atDisplayName(data); displayName != "" {
						if msg.AtNames == nil {
							msg.AtNames = make(map[int64]string)
						}
						msg.AtNames[qqID] = displayName
					}
				}
			}

		case "reply":
			if replyMsgID, ok := utils.ParseInt64Value(data["id"]); ok {
				msg.Reply = &ReplyInfo{MessageID: replyMsgID}
			}

		case "mface": // 商城表情/魔法表情
			img := ImageInfo{}
			if url, ok := data["url"].(string); ok {
				img.URL = url
			}
			img.SubType = 1 // 标记为表情包类型
			if img.URL != "" {
				msg.Images = append(msg.Images, img)
			}

		case "record": // 语音消息
			msg.HasRecord = true

		case "video": // 视频消息
			vid := VideoInfo{}
			if url, ok := data["url"].(string); ok {
				vid.URL = url
			}
			if file, ok := data["file"].(string); ok {
				vid.File = file
			}
			if vid.URL != "" || vid.File != "" {
				msg.Videos = append(msg.Videos, vid)
			}

		case "file": // 文件
			if name, ok := data["name"].(string); ok {
				msg.FileNames = append(msg.FileNames, name)
			} else {
				msg.FileNames = append(msg.FileNames, "")
			}

		case "json": // JSON 卡片消息
			if jsonStr, ok := data["data"].(string); ok {
				card := parseCardMessage(jsonStr)
				if card != nil {
					msg.Cards = append(msg.Cards, *card)
				}
			}

		case "forward": // 合并转发
			content, ok := data["content"].([]interface{})
			if !ok {
				return false
			}
			msg.ForwardContent = append(msg.ForwardContent, content...)
		}
	}

	// 合并文本内容
	for i, part := range textParts {
		if i > 0 {
			msg.Content += " "
		}
		msg.Content += part
	}
	return true
}

// parseCardMessage 解析JSON卡片消息
func parseCardMessage(jsonStr string) *CardMessage {
	var data map[string]interface{}
	if err := sonic.UnmarshalString(jsonStr, &data); err != nil {
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
		case "record":
			parts = append(parts, "[语音]")
		case "video":
			parts = append(parts, "[视频]")
		case "file":
			parts = append(parts, "[文件]")
		case "at":
			if text, _, _ := parseAtSegment(data); text != "" {
				parts = append(parts, text)
			}
		case "json":
			parts = append(parts, "[卡片消息]")
		case "forward":
			parts = append(parts, "[合并转发]")
		}
	}
	return strings.Join(parts, "")
}

func parseAtSegmentForGroup(data map[string]interface{}) (int64, bool) {
	if qq, ok := data["qq"].(string); ok {
		if qq == "all" {
			return AtAllUserID, true
		}
		qqID, err := strconv.ParseInt(qq, 10, 64)
		if err != nil {
			return 0, false
		}
		return qqID, true
	}

	qqID, ok := utils.ParseInt64Value(data["qq"])
	if !ok {
		return 0, false
	}
	return qqID, true
}

func atDisplayName(data map[string]interface{}) string {
	for _, key := range []string{"name", "nickname", "card"} {
		if value, ok := data[key].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func parseAtSegment(data map[string]interface{}) (string, int64, bool) {
	if qq, ok := data["qq"].(string); ok {
		if qq == "all" {
			return "@全体成员", 0, false
		}
		qqID, err := strconv.ParseInt(qq, 10, 64)
		if err != nil {
			return "", 0, false
		}
		if displayName := atDisplayName(data); displayName != "" {
			return "@" + displayName, qqID, true
		}
		return "@" + qq, qqID, true
	}
	qqID, ok := utils.ParseInt64Value(data["qq"])
	if !ok {
		return "", 0, false
	}
	if displayName := atDisplayName(data); displayName != "" {
		return "@" + displayName, qqID, true
	}
	return fmt.Sprintf("@%d", qqID), qqID, true
}

func newGroupMemberInfoCache() *ttlcache.Cache[string, *GroupMemberInfo] {
	return ttlcache.New(
		ttlcache.WithTTL[string, *GroupMemberInfo](10*time.Minute),
		ttlcache.WithCapacity[string, *GroupMemberInfo](2048),
		ttlcache.WithDisableTouchOnHit[string, *GroupMemberInfo](),
	)
}

func groupMemberCacheKey(groupID, userID int64) string {
	return fmt.Sprintf("%d:%d", groupID, userID)
}

func parseInt(v interface{}) (int, bool) {
	i64, ok := utils.ParseInt64Value(v)
	return int(i64), ok
}
