package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"mumu-bot/internal/media"
	"mumu-bot/internal/onebot"
	workerproto "mumu-bot/worker/proto"

	"github.com/bytedance/sonic"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type ListQzonePostsInput struct {
	Scope  string `json:"scope" description:"feed 读取好友动态线索索引；user 读取自己或指定账号的完整主页动态"`
	UserID int64  `json:"user_id,omitempty" description:"scope=user 时的 QQ 号，省略表示自己"`
	Cursor string `json:"cursor,omitempty" description:"同一 scope 和同一账号上次返回的 next_cursor，原样传回"`
	Limit  int    `json:"limit,omitempty" description:"仅 scope=feed 使用，默认10，最大50；主页每页数量由平台决定"`
}

func listQzonePosts(ctx context.Context, input *ListQzonePostsInput) (any, error) {
	tc, err := requireQzoneContext(ctx)
	if err != nil {
		return nil, err
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
		limit = min(limit, 50)
		page, err := tc.Bot.ListQzoneFeeds(ctx, input.Cursor, limit)
		if err != nil {
			return nil, socialError(err)
		}
		return map[string]any{
			"success": true,
			"feeds":   page.Feeds, "has_more": page.HasMore, "next_cursor": page.NextCursor,
		}, nil
	case "user":
		if input.UserID < 0 {
			return nil, fmt.Errorf("user_id 不能为负数")
		}
		page, err := tc.Bot.ListQzoneHomeFeeds(ctx, input.UserID, input.Cursor)
		if err != nil {
			return nil, socialError(err)
		}
		if err := referenceQzoneArray(ctx, tc, page["feeds"], "feed_ref"); err != nil {
			return nil, err
		}
		return map[string]any{"success": true, "page": page}, nil
	default:
		return nil, fmt.Errorf("scope 只能是 feed 或 user")
	}
}

type ReadQzonePostInput struct {
	PostID string `json:"post_id" description:"真实的24位十六进制说说编号，来自好友动态的 tid、主页的 post_id 或发表回执"`
}

func readQzonePost(ctx context.Context, input *ReadQzonePostInput) (any, error) {
	tc, err := requireQzoneContext(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil || input.PostID == "" {
		return nil, fmt.Errorf("请提供 post_id")
	}
	post, err := tc.Bot.ReadQzonePost(ctx, input.PostID)
	if err != nil {
		return nil, socialError(err)
	}
	if err := referenceQzoneObject(ctx, tc, post["feed"], "feed_ref"); err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "post": post}, nil
}

type ReadQzoneCommentsInput struct {
	FeedRef    string `json:"feed_ref" description:"完整动态返回的 feed_ref"`
	CommentRef string `json:"comment_ref,omitempty" description:"读取楼中楼时填写父评论的 comment_ref；省略则读取一级评论续页"`
	Cursor     string `json:"cursor,omitempty" description:"一级评论用详情或上一页的 attach_info；楼中楼用 reply_attach_info，首次可省略；原样传回"`
	Limit      int    `json:"limit,omitempty" description:"仅楼中楼使用，默认20，最大100"`
}

func readQzoneComments(ctx context.Context, input *ReadQzoneCommentsInput) (any, error) {
	tc, err := requireQzoneContext(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil || input.FeedRef == "" {
		return nil, fmt.Errorf("请提供 feed_ref")
	}
	feed, err := readQzoneRef(ctx, tc, input.FeedRef)
	if err != nil {
		return nil, err
	}
	var page onebot.QzoneObject
	if input.CommentRef == "" {
		if input.Cursor == "" {
			return nil, fmt.Errorf("一级评论续页需要详情或上一页返回的 attach_info")
		}
		page, err = tc.Bot.ReadQzoneComments(ctx, feed, input.Cursor)
	} else {
		comment, readErr := readQzoneRef(ctx, tc, input.CommentRef)
		if readErr != nil {
			return nil, readErr
		}
		limit := input.Limit
		if limit <= 0 {
			limit = 20
		}
		page, err = tc.Bot.ReadQzoneReplies(ctx, feed, comment, input.Cursor, min(limit, 100))
	}
	if err != nil {
		return nil, socialError(err)
	}
	if input.CommentRef == "" {
		if err := referenceQzoneCellComments(ctx, tc, page); err != nil {
			return nil, err
		}
	} else if replies := page["replys"]; replies != nil {
		if err := referenceQzoneArray(ctx, tc, replies, "reply_ref"); err != nil {
			return nil, err
		}
	}
	return map[string]any{"success": true, "page": page}, nil
}

type PublishQzoneInput struct {
	Content   string   `json:"content" description:"说说正文"`
	ImageRefs []string `json:"image_refs,omitempty" description:"配图的 image_ref 列表；不能传路径或普通文件"`
}

type PublishQzoneOutput struct {
	Success bool   `json:"success"`
	PostID  string `json:"post_id"`
	Time    int64  `json:"time"`
}

func publishQzone(ctx context.Context, input *PublishQzoneInput) (*PublishQzoneOutput, error) {
	tc, err := requireQzoneContext(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil || strings.TrimSpace(input.Content) == "" {
		return nil, fmt.Errorf("说说正文不能为空")
	}
	images := make([]string, 0, len(input.ImageRefs))
	total := 0
	for _, imageRef := range input.ImageRefs {
		data, _, name, err := refImageBytes(ctx, tc, imageRef)
		if err != nil {
			return nil, err
		}
		if len(data) == 0 || media.ImageMIME(data) == "" {
			return nil, fmt.Errorf("%s 不是可识别的图片", name)
		}
		if len(data) > workerproto.MaxImageBytes {
			return nil, fmt.Errorf("图片超过 10 MiB")
		}
		total += len(data)
		if total > workerproto.MaxFileBytes {
			return nil, fmt.Errorf("图片总大小超过 32 MiB")
		}
		images = append(images, "base64://"+base64.StdEncoding.EncodeToString(data))
	}
	result, err := tc.Bot.PublishQzone(ctx, input.Content, images)
	if err != nil {
		return nil, socialError(err)
	}
	return &PublishQzoneOutput{
		Success: true, PostID: result.Tid, Time: int64(result.Time),
	}, nil
}

type LikeQzoneInput struct {
	FeedRef string `json:"feed_ref" description:"完整主页或说说详情返回的 feed_ref"`
}

func likeQzone(ctx context.Context, input *LikeQzoneInput) (any, error) {
	tc, err := requireQzoneContext(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil || input.FeedRef == "" {
		return nil, fmt.Errorf("请提供 feed_ref")
	}
	feed, err := readQzoneRef(ctx, tc, input.FeedRef)
	if err != nil {
		return nil, err
	}
	result, err := tc.Bot.LikeQzone(ctx, feed)
	if err != nil {
		return nil, socialError(err)
	}
	return map[string]any{"success": true, "feed_ref": input.FeedRef, "result": result}, nil
}

type CommentQzoneInput struct {
	FeedRef    string `json:"feed_ref" description:"完整主页或说说详情返回的 feed_ref"`
	Content    string `json:"content" description:"评论或回复正文"`
	CommentRef string `json:"comment_ref,omitempty" description:"回复评论或楼中楼时填写父评论的 comment_ref；省略则发布一级评论"`
	ReplyRef   string `json:"reply_ref,omitempty" description:"回复楼中楼某人时填写其 reply_ref，同时须提供父 comment_ref"`
}

func commentQzone(ctx context.Context, input *CommentQzoneInput) (any, error) {
	tc, err := requireQzoneContext(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil || input.FeedRef == "" || strings.TrimSpace(input.Content) == "" {
		return nil, fmt.Errorf("请提供 feed_ref 和评论正文")
	}
	if input.ReplyRef != "" && input.CommentRef == "" {
		return nil, fmt.Errorf("回复楼中楼需要同时提供父 comment_ref")
	}
	feed, err := readQzoneRef(ctx, tc, input.FeedRef)
	if err != nil {
		return nil, err
	}
	var result onebot.QzoneObject
	key, refKey := "comment", "comment_ref"
	if input.CommentRef == "" {
		result, err = tc.Bot.CommentQzone(ctx, feed, input.Content)
	} else {
		comment, readErr := readQzoneRef(ctx, tc, input.CommentRef)
		if readErr != nil {
			return nil, readErr
		}
		var reply onebot.QzoneObject
		if input.ReplyRef != "" {
			reply, readErr = readQzoneRef(ctx, tc, input.ReplyRef)
			if readErr != nil {
				return nil, readErr
			}
		}
		result, err = tc.Bot.ReplyQzone(ctx, feed, comment, reply, input.Content)
		key, refKey = "reply", "reply_ref"
	}
	if err != nil {
		return nil, socialError(err)
	}
	output := map[string]any{"success": true, "feed_ref": input.FeedRef, "result": result}
	if refErr := referenceQzoneObject(ctx, tc, result[key], refKey); refErr != nil {
		output["reference_error"] = refErr.Error()
		output["note"] = "平台已接受评论或回复，但回执引用未保存；先读取详情核实，不要因为缺少引用而重发"
	}
	return output, nil
}

func requireQzoneContext(ctx context.Context) (*ToolContext, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.Bot == nil || tc.MemoryMgr == nil || tc.MemoryMgr.RefStore() == nil {
		return nil, NewTerminalToolError(fmt.Errorf("QQ 空间能力未初始化"))
	}
	return tc, nil
}

// readQzoneRef 取回服务端原始对象，避免模型重新拼装不透明定位字段
func readQzoneRef(ctx context.Context, tc *ToolContext, token string) (onebot.QzoneObject, error) {
	data, _, _, err := refBytes(ctx, tc, token)
	if err != nil {
		return nil, err
	}
	var object onebot.QzoneObject
	if err := sonic.Unmarshal(data, &object); err != nil {
		return nil, fmt.Errorf("空间引用不包含有效对象")
	}
	return object, nil
}

// referenceQzoneObject 先固定原始对象，再给展示结果添加引用，回传给 SDK 的仍是原始数据
func referenceQzoneObject(ctx context.Context, tc *ToolContext, value any, refKey string) error {
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("空间返回的 %s 对象无效", refKey)
	}
	raw, err := sonic.Marshal(object)
	if err != nil {
		return err
	}
	ref, err := tc.MemoryMgr.RefStore().CreateReady(ctx, "qzone-"+refKey+".json", bytes.NewReader(raw), workerproto.MaxFileBytes, nil)
	if err != nil {
		return err
	}
	object[refKey] = ref.FileRef()
	switch refKey {
	case "feed_ref":
		common, _ := object["cell_common"].(map[string]any)
		union, _ := common["union_id"].(map[string]any)
		if id, ok := union["feed_id"].(string); ok && len(id) == 24 {
			if _, err := hex.DecodeString(id); err == nil {
				object["post_id"] = id
			}
		}
		// 定位字段只留在原始快照，展示仍保留当前内容、状态与媒体
		delete(common, "union_id")
		delete(common, "cur_like_key")
		if len(common) == 0 {
			delete(object, "cell_common")
		}
		delete(object, "cell_feed_transfer")
		return referenceQzoneCellComments(ctx, tc, object)
	case "comment_ref":
		if replies := object["replys"]; replies != nil {
			return referenceQzoneArray(ctx, tc, replies, "reply_ref")
		}
	}
	return nil
}

func referenceQzoneArray(ctx context.Context, tc *ToolContext, value any, refKey string) error {
	rows, ok := value.([]any)
	if !ok {
		return fmt.Errorf("空间返回的 %s 列表无效", refKey)
	}
	for _, row := range rows {
		if err := referenceQzoneObject(ctx, tc, row, refKey); err != nil {
			return err
		}
	}
	return nil
}

func referenceQzoneCellComments(ctx context.Context, tc *ToolContext, object map[string]any) error {
	cell, _ := object["cell_comment"].(map[string]any)
	if comments := cell["comments"]; comments != nil {
		return referenceQzoneArray(ctx, tc, comments, "comment_ref")
	}
	return nil
}

func NewListQzonePostsTool() (tool.Tool, error) {
	return newFunctionTool("listQzonePosts", "scope=user 查看自己或指定账号的主页内容，返回 feed_ref 和可刷新的说说 post_id；完整原始对象可用 readFile(file_ref=feed_ref) 按需读。scope=feed 只提供好友动态线索，完整说说用 readQzonePost(post_id=tid) 读取。两种列表都用各自的 next_cursor 续页，不能混用游标。", listQzonePosts)
}

func NewReadQzonePostTool() (tool.Tool, error) {
	return newFunctionTool("readQzonePost", "按 post_id 读取或刷新说说内容、媒体和内联评论，返回 feed_ref/comment_ref；已有 feed_ref 可直接点赞或评论。完整原始对象保存在引用快照，可用 readFile(file_ref=feed_ref) 按需读。一级评论用 attach_info 继续 readQzoneComments；内联评论不保证完整，媒体地址不代表已经看过媒体。", readQzonePost)
}

func NewReadQzoneCommentsTool() (tool.Tool, error) {
	return newFunctionTool("readQzoneComments", "读取完整动态的评论或楼中楼续页。只传 feed_ref 与 attach_info 读取一级评论，另传 comment_ref 读取该评论的楼中楼；返回 comment_ref/reply_ref，保留平台全部字段和分页状态。按 has_more 与对应游标判断是否继续，不用条数或自己推算 offset。", readQzoneComments)
}

func NewPublishQzoneTool() (tool.Tool, error) {
	return newFunctionTool("publishQzone", "发表一条图文说说，配图只能使用 image_refs；返回 post_id 和平台提供的发表时间，time=0 表示未知。固定使用 QQ 默认公开参数；后续互动先用 readQzonePost(post_id) 取得完整动态的 feed_ref。", publishQzone)
}

func NewLikeQzoneTool() (tool.Tool, error) {
	return newFunctionTool("likeQzone", "通过完整动态的 feed_ref 点赞，保留平台要求的原始定位信息；无需自行填写作者、时间或拼造 like key，以真实回执为准。", likeQzone)
}

func NewCommentQzoneTool() (tool.Tool, error) {
	return newFunctionTool("commentQzone", "通过 feed_ref 发表评论；回复评论时提供父 comment_ref，回复楼中楼某人时再提供 reply_ref。引用来自完整详情或 readQzoneComments，不拼造评论编号和用户身份。成功回执保留全部字段并附新 comment_ref/reply_ref；引用保存失败也不能当成评论未发出而重发。", commentQzone)
}
