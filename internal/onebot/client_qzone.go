package onebot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/zjutjh/onebot-sdk/api"
)

const (
	ActionGetQzoneFeeds  = "get_qzone_feeds"
	ActionGetQzoneList   = "get_qzone_msg_list"
	ActionGetQzoneDetail = "get_qzone_msg_detail"
	ActionLikeQzone      = "like_qzone"
	ActionCommentQzone   = "comment_qzone"
)

// QzoneString 兼容后端把帖子或评论编号发成字符串或数字
type QzoneString string

func (s *QzoneString) UnmarshalJSON(data []byte) error {
	var text string
	if err := sonic.Unmarshal(data, &text); err == nil {
		*s = QzoneString(text)
		return nil
	}
	var number json.Number
	if err := sonic.Unmarshal(data, &number); err != nil {
		*s = ""
		return nil
	}
	*s = QzoneString(number.String())
	return nil
}

type QzoneFeedItem struct {
	Uin             int64       `json:"uin"`
	Nickname        string      `json:"nickname"`
	Time            int64       `json:"time"`
	AppID           int64       `json:"appid"`
	Key             QzoneString `json:"key"`
	Tid             QzoneString `json:"tid"`
	Content         string      `json:"content"`
	ContentComplete *bool       `json:"content_complete"`
	Images          []string    `json:"images"`
	LikeCount       *int64      `json:"like_count"`
	CommentCount    *int64      `json:"comment_count"`
}

type QzonePost struct {
	Tid             QzoneString `json:"tid"`
	Content         string      `json:"content"`
	Time            int64       `json:"time"`
	CommentNum      *int64      `json:"comment_num"`
	IsPrivate       *bool       `json:"is_private"`
	Images          []string    `json:"images"`
	ContentComplete *bool       `json:"content_complete,omitempty"`
}

type QzoneFeedPage struct {
	Feeds      []QzoneFeedItem `json:"feeds"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

type QzoneUserPage struct {
	Total   *int64      `json:"total"`
	MsgList []QzonePost `json:"msglist"`
}

type QzoneReply struct {
	ReplyUin  int64       `json:"reply_uin"`
	ReplyName string      `json:"reply_name"`
	Content   string      `json:"content"`
	Time      int64       `json:"time"`
	CommentID QzoneString `json:"comment_id"`
}

type QzoneComment struct {
	CommentID  QzoneString  `json:"comment_id"`
	CommentUin int64        `json:"comment_uin"`
	Content    string       `json:"content"`
	Time       int64        `json:"time"`
	Replies    []QzoneReply `json:"replies"`
}

type QzoneDetail struct {
	Tid        QzoneString    `json:"tid"`
	Content    string         `json:"content"`
	CommentNum *int64         `json:"comment_num"`
	Comments   []QzoneComment `json:"comments"`
}

type qzoneCursor struct {
	Raw json.RawMessage
}

func (c *qzoneCursor) UnmarshalJSON(data []byte) error {
	c.Raw = append(c.Raw[:0], data...)
	return nil
}

func qzoneCursorText(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var text string
	if sonic.UnmarshalString(trimmed, &text) == nil {
		return text
	}
	return trimmed
}

func (c *Client) ListQzoneFeeds(ctx context.Context, cursor string, count int) (*QzoneFeedPage, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	req := map[string]any{"count": count}
	if strings.TrimSpace(cursor) != "" {
		req["cursor"] = cursor
	}
	var raw struct {
		Feeds      []QzoneFeedItem `json:"feeds"`
		HasMore    bool            `json:"has_more"`
		NextCursor qzoneCursor     `json:"next_cursor"`
	}
	if err := client.Call(ctx, ActionGetQzoneFeeds, req, &raw); err != nil {
		return nil, err
	}
	return &QzoneFeedPage{Feeds: raw.Feeds, HasMore: raw.HasMore, NextCursor: qzoneCursorText(raw.NextCursor.Raw)}, nil
}

func (c *Client) ListQzonePosts(ctx context.Context, userID int64, offset, limit int) (*QzoneUserPage, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	req := map[string]any{"pos": offset, "num": limit}
	if userID > 0 {
		req["target_uin"] = userID
	}
	var raw QzoneUserPage
	if err := client.Call(ctx, ActionGetQzoneList, req, &raw); err != nil {
		return nil, err
	}
	return &raw, nil
}

func (c *Client) ReadQzonePost(ctx context.Context, authorQQ int64, tid string, offset, limit int) (*QzoneDetail, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	req := map[string]any{"tid": tid, "pos": offset, "num": limit}
	if authorQQ > 0 {
		req["target_uin"] = authorQQ
	}
	var detail QzoneDetail
	if err := client.Call(ctx, ActionGetQzoneDetail, req, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

func (c *Client) PublishQzone(ctx context.Context, content string, images []string) (string, error) {
	client, err := c.apiClient()
	if err != nil {
		return "", err
	}
	right := rawUnionPtr[api.SendQzoneMsgRequestUgcRightUnion](1)
	resp, err := client.SendQzoneMsg(ctx, api.SendQzoneMsgRequest{
		Content: content, Images: images, UgcRight: right,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || strings.TrimSpace(resp.Tid) == "" {
		return "", fmt.Errorf("发表回执没有 tid")
	}
	return resp.Tid, nil
}

func (c *Client) LikeQzone(ctx context.Context, authorQQ int64, tid string, abstime int64) error {
	client, err := c.apiClient()
	if err != nil {
		return err
	}
	req := map[string]any{"tid": tid, "abstime": abstime}
	if authorQQ > 0 {
		req["target_uin"] = authorQQ
	}
	return client.Call(ctx, ActionLikeQzone, req, &map[string]any{})
}

type QzoneCommentInput struct {
	AuthorQQ   int64
	Tid        string
	Content    string
	CommentID  string
	CommentUin int64
	ReplyUin   int64
	ReplyName  string
}

func (c *Client) CommentQzone(ctx context.Context, input QzoneCommentInput) (string, error) {
	client, err := c.apiClient()
	if err != nil {
		return "", err
	}
	req := map[string]any{"tid": input.Tid, "content": input.Content}
	if input.AuthorQQ > 0 {
		req["target_uin"] = input.AuthorQQ
	}
	if strings.TrimSpace(input.CommentID) != "" {
		req["comment_id"] = input.CommentID
	}
	if input.CommentUin > 0 {
		req["comment_uin"] = input.CommentUin
	}
	if input.ReplyUin > 0 {
		req["reply_uin"] = input.ReplyUin
	}
	if strings.TrimSpace(input.ReplyName) != "" {
		req["reply_name"] = input.ReplyName
	}
	var raw struct {
		CommentID QzoneString `json:"comment_id"`
	}
	if err := client.Call(ctx, ActionCommentQzone, req, &raw); err != nil {
		return "", err
	}
	return string(raw.CommentID), nil
}
