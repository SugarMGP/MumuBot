package onebot

import (
	"context"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/zjutjh/onebot-sdk/api"
	"github.com/zjutjh/onebot-sdk/message"
)

// QzoneObject 保留 SDK 返回的完整动态、评论及不透明定位字段
type QzoneObject = api.QzoneObject

// QzoneFeedItem 是好友聚合流的线索，完整说说通过新详情接口读取
type QzoneFeedItem struct {
	Uin             int64           `json:"uin"`
	Nickname        string          `json:"nickname"`
	Time            int64           `json:"time"`
	AppID           int64           `json:"appid"`
	Key             message.StrNum  `json:"key"`
	Tid             *message.StrNum `json:"tid"`
	Content         *string         `json:"content"`
	ContentComplete *bool           `json:"content_complete"`
	Images          []string        `json:"images"`
	LikeCount       *int64          `json:"like_count"`
	CommentCount    *int64          `json:"comment_count"`
}

type QzoneFeedPage struct {
	Feeds      []QzoneFeedItem `json:"feeds"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor,omitempty"`
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
	response, err := client.GetQzoneFeeds(ctx, req)
	if err != nil {
		return nil, err
	}
	var page QzoneFeedPage
	if err := sonic.Unmarshal(response.Raw, &page); err != nil {
		return nil, fmt.Errorf("解析 QQ 空间动态索引失败: %w", err)
	}
	return &page, nil
}

func (c *Client) ListQzoneHomeFeeds(ctx context.Context, userID int64, cursor string) (QzoneObject, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	return client.GetQzoneHomeFeeds(ctx, api.GetQzoneHomeFeedsRequest{TargetUin: message.ID(userID), Cursor: cursor})
}

func (c *Client) ReadQzonePost(ctx context.Context, tid string) (QzoneObject, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	return client.GetQzoneFeedDetail(ctx, api.GetQzoneFeedDetailRequest{Tid: tid})
}

func (c *Client) ReadQzoneComments(ctx context.Context, feed QzoneObject, cursor string) (QzoneObject, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	return client.GetQzoneFeedComments(ctx, api.GetQzonePublicCommentsRequest{Feed: feed, Cursor: cursor})
}

func (c *Client) ReadQzoneReplies(ctx context.Context, feed, comment QzoneObject, cursor string, limit int) (QzoneObject, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	return client.GetQzoneFeedReplies(ctx, api.GetQzonePublicRepliesRequest{
		Feed: feed, Comment: comment, Cursor: &cursor,
		PageSize: rawUnionPtr[api.GetQzonePublicRepliesRequestPageSizeUnion](limit),
	})
}

func (c *Client) PublishQzone(ctx context.Context, content string, images []string) (*api.SendQzoneMsgResponse, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.SendQzoneMsg(ctx, api.SendQzoneMsgRequest{
		Content: rawUnion[api.SendQzoneMsgRequestContentUnion](content), Images: images,
		UgcRight: rawUnionPtr[api.SendQzoneMsgRequestUgcRightUnion](1),
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || strings.TrimSpace(resp.Tid) == "" {
		return nil, fmt.Errorf("发表回执没有 tid")
	}
	return resp, nil
}

func (c *Client) LikeQzone(ctx context.Context, feed QzoneObject) (QzoneObject, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	return client.SetQzoneFeedLike(ctx, api.SetQzonePublicLikeRequest{
		Feed: feed, Enable: rawUnionPtr[api.SetQzonePublicLikeRequestEnableUnion](true),
	})
}

func (c *Client) CommentQzone(ctx context.Context, feed QzoneObject, content string) (QzoneObject, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	return client.CommentQzoneFeed(ctx, api.CommentQzonePublicFeedRequest{
		Feed: feed, Content: rawUnion[api.CommentQzonePublicFeedRequestContentUnion](content),
	})
}

func (c *Client) ReplyQzone(ctx context.Context, feed, comment, reply QzoneObject, content string) (QzoneObject, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	return client.ReplyQzoneFeed(ctx, api.ReplyQzonePublicFeedRequest{
		Feed: feed, Comment: comment, Reply: reply,
		Content: rawUnion[api.ReplyQzonePublicFeedRequestContentUnion](content),
	})
}
