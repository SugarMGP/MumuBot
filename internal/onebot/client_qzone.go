package onebot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/zjutjh/onebot-sdk/api"
	"github.com/zjutjh/onebot-sdk/message"
)

type QzoneFeedItem struct {
	Uin             int64          `json:"uin"`
	Nickname        string         `json:"nickname"`
	Time            int64          `json:"time"`
	AppID           int64          `json:"appid"`
	Key             message.StrNum `json:"key"`
	Tid             message.StrNum `json:"tid"`
	Content         string         `json:"content"`
	ContentComplete *bool          `json:"content_complete"`
	Images          []string       `json:"images"`
	LikeCount       *int64         `json:"like_count"`
	CommentCount    *int64         `json:"comment_count"`
}

type QzonePost struct {
	Tid             message.StrNum `json:"tid"`
	Content         string         `json:"content"`
	Time            int64          `json:"time"`
	CommentNum      *int64         `json:"comment_num"`
	IsPrivate       *bool          `json:"is_private"`
	Images          []string       `json:"images"`
	ContentComplete *bool          `json:"content_complete,omitempty"`
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
	ReplyUin  int64          `json:"reply_uin"`
	ReplyName string         `json:"reply_name"`
	Content   string         `json:"content"`
	Time      int64          `json:"time"`
	CommentID message.StrNum `json:"comment_id"`
}

type QzoneComment struct {
	CommentID  message.StrNum `json:"comment_id"`
	CommentUin int64          `json:"comment_uin"`
	Content    string         `json:"content"`
	Time       int64          `json:"time"`
	Replies    []QzoneReply   `json:"replies"`
}

type QzoneDetail struct {
	Tid        message.StrNum `json:"tid"`
	Content    string         `json:"content"`
	CommentNum *int64         `json:"comment_num"`
	Comments   []QzoneComment `json:"comments"`
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
	req := api.GetQzoneFeedsRequest{Count: rawUnionPtr[api.GetQzoneFeedsRequestCountUnion](count)}
	if cursor != "" {
		req.Cursor = &cursor
	}
	var raw struct {
		Feeds      []QzoneFeedItem `json:"feeds"`
		HasMore    bool            `json:"has_more"`
		NextCursor json.RawMessage `json:"next_cursor"`
	}
	response, err := client.GetQzoneFeeds(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := sonic.Unmarshal(response.Raw, &raw); err != nil {
		return nil, fmt.Errorf("解析 QQ 空间动态失败: %w", err)
	}
	return &QzoneFeedPage{Feeds: raw.Feeds, HasMore: raw.HasMore, NextCursor: qzoneCursorText(raw.NextCursor)}, nil
}

func (c *Client) ListQzonePosts(ctx context.Context, userID int64, offset, limit int) (*QzoneUserPage, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	req := api.GetQzoneMsgListRequest{
		Pos: rawUnionPtr[api.GetQzoneMsgListRequestPosUnion](offset),
		Num: rawUnionPtr[api.GetQzoneMsgListRequestNumUnion](limit),
	}
	if userID > 0 {
		req.TargetUin = rawUnionPtr[api.GetQzoneMsgListRequestTargetUINUnion](userID)
	}
	var raw QzoneUserPage
	response, err := client.GetQzoneMsgList(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := sonic.Unmarshal(response.Raw, &raw); err != nil {
		return nil, fmt.Errorf("解析 QQ 空间说说列表失败: %w", err)
	}
	return &raw, nil
}

func (c *Client) ReadQzonePost(ctx context.Context, authorQQ int64, tid string, offset, limit int) (*QzoneDetail, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	req := api.GetQzoneMsgDetailRequest{
		Tid: rawUnionPtr[api.GetQzoneMsgDetailRequestTidUnion](tid),
		Pos: rawUnionPtr[api.GetQzoneMsgDetailRequestPosUnion](offset),
		Num: rawUnionPtr[api.GetQzoneMsgDetailRequestNumUnion](limit),
	}
	if authorQQ > 0 {
		req.TargetUin = rawUnionPtr[api.GetQzoneMsgDetailRequestTargetUINUnion](authorQQ)
	}
	var detail QzoneDetail
	response, err := client.GetQzoneMsgDetail(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := sonic.Unmarshal(response.Raw, &detail); err != nil {
		return nil, fmt.Errorf("解析 QQ 空间说说详情失败: %w", err)
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
		Content: rawUnion[api.SendQzoneMsgRequestContentUnion](content), Images: images, UgcRight: right,
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
	req := api.LikeQzoneRequest{
		Tid:     rawUnionPtr[api.LikeQzoneRequestTidUnion](tid),
		Abstime: rawUnionPtr[api.LikeQzoneRequestAbstimeUnion](abstime),
	}
	if authorQQ > 0 {
		req.TargetUin = rawUnionPtr[api.LikeQzoneRequestTargetUINUnion](authorQQ)
	}
	_, err = client.LikeQzone(ctx, req)
	return err
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
	req := api.CommentQzoneRequest{
		Tid:     rawUnionPtr[api.CommentQzoneRequestTidUnion](input.Tid),
		Content: rawUnion[api.CommentQzoneRequestContentUnion](input.Content),
	}
	if input.AuthorQQ > 0 {
		req.TargetUin = rawUnionPtr[api.CommentQzoneRequestTargetUINUnion](input.AuthorQQ)
	}
	if strings.TrimSpace(input.CommentID) != "" {
		req.CommentID = rawUnionPtr[api.CommentQzoneRequestCommentIDUnion](input.CommentID)
	}
	if input.CommentUin > 0 {
		req.CommentUin = rawUnionPtr[api.CommentQzoneRequestCommentUINUnion](input.CommentUin)
	}
	if input.ReplyUin > 0 {
		req.ReplyUin = rawUnionPtr[api.CommentQzoneRequestReplyUINUnion](input.ReplyUin)
	}
	if strings.TrimSpace(input.ReplyName) != "" {
		req.ReplyName = rawUnionPtr[api.CommentQzoneRequestReplyNameUnion](input.ReplyName)
	}
	var raw struct {
		CommentID message.StrNum `json:"comment_id"`
	}
	response, err := client.CommentQzone(ctx, req)
	if err != nil {
		return "", err
	}
	if err := sonic.Unmarshal(response.Raw, &raw); err != nil {
		return "", fmt.Errorf("解析 QQ 空间评论回执失败: %w", err)
	}
	return string(raw.CommentID), nil
}
