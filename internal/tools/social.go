package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"mumu-bot/internal/media"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/workerproto"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	ob "github.com/zjutjh/onebot-sdk"
)

type SetSelfProfileInput struct {
	Field    string `json:"field" jsonschema:"description=avatar、nickname 或 signature，一次只改一个"`
	ImageRef string `json:"image_ref,omitempty" jsonschema:"description=field=avatar 时必填的图片引用"`
	Value    string `json:"value,omitempty" jsonschema:"description=field=nickname 或 signature 时的新内容"`
}

type SetSelfProfileOutput struct {
	Success bool           `json:"success"`
	Field   string         `json:"field"`
	Value   string         `json:"value,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
	Note    string         `json:"note,omitempty"`
}

func setSelfProfile(ctx context.Context, input *SetSelfProfileInput) (*SetSelfProfileOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("QQ 资料能力未初始化"))
	}
	if input == nil {
		return nil, fmt.Errorf("请提供要修改的字段")
	}
	switch strings.ToLower(strings.TrimSpace(input.Field)) {
	case "avatar":
		refValue := strings.TrimSpace(input.ImageRef)
		if refValue == "" {
			return nil, fmt.Errorf("修改头像必须提供 image_ref")
		}
		data, _, _, err := refImageBytes(ctx, tc, refValue)
		if err != nil {
			return nil, err
		}
		if len(data) > workerproto.MaxImageBytes {
			return nil, fmt.Errorf("头像超过 10 MiB")
		}
		if media.ImageMIME(data) == "" {
			return nil, fmt.Errorf("这个文件不是可识别的图片")
		}
		if err := tc.Bot.SetAvatarBytes(ctx, data); err != nil {
			return nil, socialError(err)
		}
		return &SetSelfProfileOutput{Success: true, Field: "avatar", Note: "平台已接受设置；头像显示刷新可能受 CDN 缓存影响"}, nil
	case "nickname":
		value := strings.TrimSpace(input.Value)
		if value == "" {
			return nil, fmt.Errorf("昵称不能为空")
		}
		result, err := tc.Bot.SetNickname(ctx, value)
		if err != nil {
			return nil, socialError(err)
		}
		return &SetSelfProfileOutput{Success: true, Field: "nickname", Value: value, Result: result, Note: "已请求修改；需要展示当前值时重新查询"}, nil
	case "signature":
		value := strings.TrimSpace(input.Value)
		result, err := tc.Bot.SetSignature(ctx, value)
		if err != nil {
			return nil, socialError(err)
		}
		return &SetSelfProfileOutput{Success: true, Field: "signature", Value: value, Result: result, Note: "已请求修改；需要展示当前值时重新查询"}, nil
	default:
		return nil, fmt.Errorf("field 只能是 avatar、nickname 或 signature")
	}
}

type ListQzonePostsInput struct {
	Scope  string `json:"scope" jsonschema:"description=feed 好友动态，或 user 指定账号说说"`
	UserID int64  `json:"user_id,omitempty" jsonschema:"description=scope=user 时的 QQ 号，省略表示自己"`
	Cursor string `json:"cursor,omitempty" jsonschema:"description=feed 续页游标，原样回传，未知时省略"`
	Offset int    `json:"offset,omitempty" jsonschema:"description=scope=user 的偏移，默认 0"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=本页数量，feed 最大 50，user 最大 100"`
}

type ListQzonePostsOutput struct {
	Success    bool                   `json:"success"`
	Scope      string                 `json:"scope"`
	Feeds      []onebot.QzoneFeedItem `json:"feeds,omitempty"`
	Posts      []onebot.QzonePost     `json:"posts,omitempty"`
	Total      *int64                 `json:"total,omitempty"`
	HasMore    bool                   `json:"has_more"`
	NextCursor string                 `json:"next_cursor,omitempty"`
	NextOffset int                    `json:"next_offset,omitempty"`
}

func listQzonePosts(ctx context.Context, input *ListQzonePostsInput) (*ListQzonePostsOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("QQ 空间能力未初始化"))
	}
	if input == nil {
		return nil, fmt.Errorf("请提供 scope")
	}
	switch strings.ToLower(strings.TrimSpace(input.Scope)) {
	case "feed":
		limit := input.Limit
		if limit <= 0 {
			limit = 10
		}
		if limit > 50 {
			limit = 50
		}
		page, err := tc.Bot.ListQzoneFeeds(ctx, input.Cursor, limit)
		if err != nil {
			return nil, socialError(err)
		}
		return &ListQzonePostsOutput{Success: true, Scope: "feed", Feeds: page.Feeds, HasMore: page.HasMore, NextCursor: page.NextCursor}, nil
	case "user":
		limit := input.Limit
		if limit <= 0 {
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}
		offset := max(input.Offset, 0)
		page, err := tc.Bot.ListQzonePosts(ctx, input.UserID, offset, limit)
		if err != nil {
			return nil, socialError(err)
		}
		next := 0
		if len(page.MsgList) == limit {
			next = offset + len(page.MsgList)
		}
		return &ListQzonePostsOutput{Success: true, Scope: "user", Posts: page.MsgList, Total: page.Total, HasMore: next > 0, NextOffset: next}, nil
	default:
		return nil, fmt.Errorf("scope 只能是 feed 或 user")
	}
}

type ReadQzonePostInput struct {
	AuthorQQ int64  `json:"author_qq" jsonschema:"description=说说作者 QQ"`
	PostID   string `json:"post_id" jsonschema:"description=字符串说说 tid"`
	Offset   int    `json:"offset,omitempty" jsonschema:"description=一级评论偏移，默认 0"`
	Limit    int    `json:"limit,omitempty" jsonschema:"description=一级评论本页数量，默认 20，最大 100"`
}

type ReadQzonePostOutput struct {
	Success    bool               `json:"success"`
	Post       onebot.QzoneDetail `json:"post"`
	Offset     int                `json:"offset"`
	HasMore    bool               `json:"has_more"`
	NextOffset int                `json:"next_offset,omitempty"`
}

func readQzonePost(ctx context.Context, input *ReadQzonePostInput) (*ReadQzonePostOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("QQ 空间能力未初始化"))
	}
	if input == nil || input.AuthorQQ <= 0 || strings.TrimSpace(input.PostID) == "" {
		return nil, fmt.Errorf("请提供 author_qq 和 post_id")
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := max(input.Offset, 0)
	post, err := tc.Bot.ReadQzonePost(ctx, input.AuthorQQ, input.PostID, offset, limit)
	if err != nil {
		return nil, socialError(err)
	}
	hasMore := post.CommentNum != nil && int64(offset+len(post.Comments)) < *post.CommentNum
	next := 0
	if hasMore {
		next = offset + len(post.Comments)
	}
	return &ReadQzonePostOutput{Success: true, Post: *post, Offset: offset, HasMore: hasMore, NextOffset: next}, nil
}

type PublishQzoneInput struct {
	Content   string   `json:"content" jsonschema:"description=说说正文，不能为空"`
	ImageRefs []string `json:"image_refs,omitempty" jsonschema:"description=图片引用列表；配图只能使用 image_refs"`
}

type PublishQzoneOutput struct {
	Success bool   `json:"success"`
	PostID  string `json:"post_id"`
	Note    string `json:"note,omitempty"`
}

func publishQzone(ctx context.Context, input *PublishQzoneInput) (*PublishQzoneOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("QQ 空间能力未初始化"))
	}
	if input == nil || strings.TrimSpace(input.Content) == "" {
		return nil, fmt.Errorf("说说正文不能为空")
	}
	images := make([]string, 0, len(input.ImageRefs))
	total := 0
	appendImage := func(data []byte, name string) error {
		if len(data) == 0 || media.ImageMIME(data) == "" {
			return fmt.Errorf("%s 不是可识别的图片", name)
		}
		if len(data) > workerproto.MaxImageBytes {
			return fmt.Errorf("图片超过 10 MiB")
		}
		total += len(data)
		if total > workerproto.MaxFileBytes {
			return fmt.Errorf("图片总大小超过 32 MiB")
		}
		images = append(images, "base64://"+base64.StdEncoding.EncodeToString(data))
		return nil
	}
	for _, imageRef := range input.ImageRefs {
		data, _, name, err := refImageBytes(ctx, tc, imageRef)
		if err != nil {
			return nil, err
		}
		if err := appendImage(data, name); err != nil {
			return nil, err
		}
	}
	tid, err := tc.Bot.PublishQzone(ctx, input.Content, images)
	if err != nil {
		return nil, socialError(err)
	}
	return &PublishQzoneOutput{Success: true, PostID: tid, Note: "平台已接受发表；图片和可见性以 QQ 实际显示为准"}, nil
}

type LikeQzoneInput struct {
	AuthorQQ int64  `json:"author_qq" jsonschema:"description=说说作者 QQ"`
	PostID   string `json:"post_id" jsonschema:"description=字符串说说 tid"`
	Abstime  int64  `json:"abstime" jsonschema:"description=列表或详情里的真实发表时间"`
}

type LikeQzoneOutput struct {
	Success bool   `json:"success"`
	PostID  string `json:"post_id"`
}

func likeQzone(ctx context.Context, input *LikeQzoneInput) (*LikeQzoneOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("QQ 空间能力未初始化"))
	}
	if input == nil || input.AuthorQQ <= 0 || strings.TrimSpace(input.PostID) == "" {
		return nil, fmt.Errorf("请提供 author_qq 和 post_id")
	}
	if err := tc.Bot.LikeQzone(ctx, input.AuthorQQ, input.PostID, input.Abstime); err != nil {
		return nil, socialError(err)
	}
	return &LikeQzoneOutput{Success: true, PostID: input.PostID}, nil
}

type QzoneReplyInput struct {
	CommentID  string `json:"comment_id" jsonschema:"description=一级评论 comment_id，字符串 0 也是有效编号"`
	CommentUin int64  `json:"comment_uin" jsonschema:"description=一级评论作者 QQ"`
	ReplyUin   int64  `json:"reply_uin,omitempty" jsonschema:"description=回复楼中楼某人时的实际 QQ"`
	ReplyName  string `json:"reply_name,omitempty" jsonschema:"description=回复楼中楼某人时的显示名"`
}

type CommentQzoneInput struct {
	AuthorQQ int64            `json:"author_qq" jsonschema:"description=说说作者 QQ"`
	PostID   string           `json:"post_id" jsonschema:"description=字符串说说 tid"`
	Content  string           `json:"content" jsonschema:"description=评论正文"`
	ReplyTo  *QzoneReplyInput `json:"reply_to,omitempty" jsonschema:"description=回复一级评论或楼中楼时提供"`
}

type CommentQzoneOutput struct {
	Success   bool   `json:"success"`
	CommentID string `json:"comment_id,omitempty"`
	Note      string `json:"note,omitempty"`
}

func commentQzone(ctx context.Context, input *CommentQzoneInput) (*CommentQzoneOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("QQ 空间能力未初始化"))
	}
	if input == nil || input.AuthorQQ <= 0 || strings.TrimSpace(input.PostID) == "" || strings.TrimSpace(input.Content) == "" {
		return nil, fmt.Errorf("请提供 author_qq、post_id 和评论正文")
	}
	comment := onebot.QzoneCommentInput{AuthorQQ: input.AuthorQQ, Tid: input.PostID, Content: input.Content}
	if input.ReplyTo != nil {
		comment.CommentID = strings.TrimSpace(input.ReplyTo.CommentID)
		comment.CommentUin = input.ReplyTo.CommentUin
		comment.ReplyUin = input.ReplyTo.ReplyUin
		comment.ReplyName = input.ReplyTo.ReplyName
	}
	commentID, err := tc.Bot.CommentQzone(ctx, comment)
	if err != nil {
		return nil, socialError(err)
	}
	note := ""
	if commentID == "" {
		note = "平台没有返回评论编号，不代表评论失败；请先核实再决定是否重发"
	}
	return &CommentQzoneOutput{Success: true, CommentID: commentID, Note: note}, nil
}

// socialError 保留平台真实失败，并对不支持与结果未知给出可核查提示
func socialError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *ob.APIError
	if errors.As(err, &apiErr) && apiErr.RetCode == 1404 {
		return fmt.Errorf("后端不支持这个 QQ 空间或资料动作（可能版本过旧）: %w", err)
	}
	if errors.Is(err, ob.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("请求结果未确认，可能已经送达；先核实目标再决定是否重做: %w", err)
	}
	return err
}

func NewSetSelfProfileTool() (tool.InvokableTool, error) {
	return utils.InferTool("setSelfProfile", "一次修改自己的一个资料字段：avatar 用 image_ref，nickname 或 signature 用 value；不提交其他字段覆盖旧值。", setSelfProfile)
}

func NewListQzonePostsTool() (tool.InvokableTool, error) {
	return utils.InferTool("listQzonePosts", "查看好友动态流或指定账号的说说；feed 用 cursor 续页，user 用 offset 续页，帖子 tid 始终是字符串。", listQzonePosts)
}

func NewReadQzonePostTool() (tool.InvokableTool, error) {
	return utils.InferTool("readQzonePost", "读取一条 QQ 空间说说的完整正文和一级评论分页；评论分页与帖子分页是两件事。", readQzonePost)
}

func NewPublishQzoneTool() (tool.InvokableTool, error) {
	return utils.InferTool("publishQzone", "给自己发表一条图文说说，正文非空；配图只能使用 image_refs，固定使用 QQ 默认公开参数，不提供可见范围选项。", publishQzone)
}

func NewLikeQzoneTool() (tool.InvokableTool, error) {
	return utils.InferTool("likeQzone", "给一条说说点赞；传列表或详情里的真实 abstime，不根据点赞数推断以前是否点过。", likeQzone)
}

func NewCommentQzoneTool() (tool.InvokableTool, error) {
	return utils.InferTool("commentQzone", "评论一条说说或回复一级评论/楼中楼；comment_id 是字符串，0 也是有效编号，回执为空不代表失败。", commentQzone)
}
