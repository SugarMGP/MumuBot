package onebot

import (
	"fmt"
	"time"
)

const AtAllUserID int64 = -1

// GroupMessage 群消息
type GroupMessage struct {
	MessageID    int64            `json:"message_id"`
	GroupID      int64            `json:"group_id"`
	UserID       int64            `json:"user_id"`
	Nickname     string           `json:"nickname"`                // QQ 原始昵称
	GroupCard    string           `json:"group_card,omitempty"`    // 当前群名片
	DisplayName  string           `json:"display_name,omitempty"`  // 当前渲染显示名
	Content      string           `json:"content"`                 // 纯文本内容
	IsMentioned  bool             `json:"is_mentioned"`            // 是否@机器人
	Time         time.Time        `json:"time"`                    // 消息时间
	ReceivedAt   time.Time        `json:"-"`                       // 当前进程从 OneBot 收到事件的时间
	Images       []ImageInfo      `json:"images,omitempty"`        // 图片列表
	Videos       []VideoInfo      `json:"videos,omitempty"`        // 视频列表
	Faces        []FaceInfo       `json:"faces,omitempty"`         // 表情列表
	AtList       []int64          `json:"at_list,omitempty"`       // 消息指向的用户；普通消息为 @ 目标，互动消息为互动目标
	Reply        *ReplyInfo       `json:"reply,omitempty"`         // 回复信息
	ForwardIDs   []string         `json:"-"`                       // 待延迟展开的合并转发 ID
	Forwards     []ForwardMessage `json:"forwards,omitempty"`      // 合并转发内容
	FileNames    []string         `json:"file_names,omitempty"`    // 文件消息名称
	Cards        []CardMessage    `json:"cards,omitempty"`         // 卡片消息
	HasRecord    bool             `json:"has_record,omitempty"`    // 是否包含语音消息
	FinalContent string           `json:"final_content,omitempty"` // 处理后的最终内容
}

// ImageInfo 图片信息
type ImageInfo struct {
	URL     string `json:"url"`
	File    string `json:"file"`
	SubType int    `json:"sub_type"` // 0普通图片 1表情包
	Desc    string `json:"desc,omitempty"`
}

// VideoInfo 视频信息
type VideoInfo struct {
	URL  string `json:"url"`
	File string `json:"file"`
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
	SenderID  int64  `json:"sender_id,omitempty"` // 被回复消息发送者 ID
	Nickname  string `json:"nickname,omitempty"`  // 被回复消息发送者原始昵称
	GroupCard string `json:"group_card,omitempty"`
	Display   string `json:"display,omitempty"`
}

// ForwardMessage 合并转发中的单条消息
type ForwardMessage struct {
	UserID   int64     `json:"user_id"`
	Nickname string    `json:"nickname"`
	Time     time.Time `json:"time"`
	Content  string    `json:"content"`
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
	SenderNick   string `json:"sender_nick"`
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
	GroupID      int64  `json:"group_id"`
	UserID       int64  `json:"user_id"`
	Nickname     string `json:"nickname"`
	Card         string `json:"card"`
	Role         string `json:"role"` // owner/admin/member
	JoinTime     int64  `json:"join_time"`
	LastSentTime int64  `json:"last_sent_time"`
	Level        string `json:"level"`
	Title        string `json:"title"` // 专属头衔
}
