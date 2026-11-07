package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

type UserProfileInput struct {
	UserID string `json:"user_id" jsonschema:"description=QQ 号或 self"`
}

type UserPinnedMemory struct {
	ID        uint   `json:"id"`
	Label     string `json:"label"`
	Content   string `json:"content"`
	Version   uint64 `json:"version"`
	UpdatedAt string `json:"updated_at"`
}

type UserProfileOutput struct {
	Success        bool               `json:"success"`
	UserID         int64              `json:"user_id"`
	IsSelf         bool               `json:"is_self"`
	Nickname       string             `json:"nickname,omitempty"`
	Remark         string             `json:"remark,omitempty"`
	GroupCard      string             `json:"group_card,omitempty"`
	Signature      string             `json:"signature,omitempty"`
	AvatarRef      string             `json:"avatar_ref,omitempty"`
	AvatarState    string             `json:"avatar_state,omitempty"`
	AvatarError    string             `json:"avatar_error,omitempty"`
	Warning        string             `json:"warning,omitempty"`
	PinnedMemories []UserPinnedMemory `json:"pinned_memories"`
}

func getUserProfileFunc(ctx context.Context, input *UserProfileInput) (*schema.ToolResult, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.Bot == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("用户详情能力未初始化"))
	}
	if input == nil || strings.TrimSpace(input.UserID) == "" {
		return nil, fmt.Errorf("user_id 不能为空")
	}
	raw := strings.TrimSpace(input.UserID)
	userID := int64(0)
	isSelf := raw == "self"
	if isSelf {
		userID = tc.Bot.GetSelfID()
	} else {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("user_id 必须是 QQ 号或 self")
		}
		userID = parsed
	}
	if userID <= 0 {
		return nil, fmt.Errorf("无法确定用户 QQ")
	}
	output := UserProfileOutput{Success: true, UserID: userID, IsSelf: isSelf, PinnedMemories: []UserPinnedMemory{}}
	if !isSelf && tc.ConversationKind == memory.ConversationKindGroup {
		if info, err := tc.Bot.GetGroupMemberInfo(ctx, tc.TargetID, userID, false); err == nil && info != nil {
			output.Nickname = info.Nickname
			output.GroupCard = info.Card
		}
	}
	if isSelf {
		if profile, err := tc.Bot.GetSelfProfile(ctx); err == nil && profile != nil {
			if strings.TrimSpace(profile.Nickname) != "" {
				output.Nickname = profile.Nickname
			}
			output.Signature = strings.TrimSpace(profile.Signature)
			output.Warning = profile.Warning
		}
	} else if info, err := tc.Bot.GetStrangerInfo(ctx, userID, true); err == nil && info != nil {
		if strings.TrimSpace(info.Nickname) != "" {
			output.Nickname = info.Nickname
		}
		output.Remark = strings.TrimSpace(info.Remark)
		output.Signature = strings.TrimSpace(info.LongNick)
	}
	var pinned []memory.KnowledgeRecord
	var err error
	if isSelf {
		pinned, err = tc.MemoryMgr.PinnedMemoriesForSelf(ctx)
	} else {
		pinned, err = tc.MemoryMgr.PinnedMemoriesForUser(ctx, userID)
	}
	if err != nil {
		return nil, err
	}
	for _, item := range pinned {
		output.PinnedMemories = append(output.PinnedMemories, UserPinnedMemory{
			ID: item.ID, Label: item.Label, Content: item.Content, Version: item.Version,
			UpdatedAt: item.UpdatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	avatarURL := fmt.Sprintf("https://q1.qlogo.cn/g?b=qq&nk=%d&s=640", userID)
	output.AvatarState = "unavailable"
	if image, _ := downloadAvatar(ctx, avatarURL); len(image) > 0 {
		if tc.MemoryMgr.RefStore() != nil {
			ref, refErr := tc.MemoryMgr.RefStore().CreateReady(ctx, "avatar.jpg", bytes.NewReader(image), 10*1024*1024, nil)
			if refErr != nil {
				output.AvatarError = refErr.Error()
			} else {
				output.AvatarRef = ref.ImageRef()
				output.AvatarState = "ready"
			}
		}
	} else {
		output.AvatarError = "头像下载失败"
	}
	text, err := sonic.MarshalString(output)
	if err != nil {
		return nil, err
	}
	return &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: text}}}, nil
}

func downloadAvatar(ctx context.Context, url string) ([]byte, string) {
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ""
	}
	mime := strings.TrimSpace(resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024+1))
	if err != nil || len(body) == 0 || len(body) > 512*1024 || media.ImageMIME(body) == "" {
		return nil, ""
	}
	return body, mime
}

func NewGetUserProfileTool() (tool.EnhancedInvokableTool, error) {
	return utils.InferEnhancedTool("getUserProfile", "查看用户资料、全部置顶记忆和头像引用；头像不直接进入上下文，需要时用 avatar_ref 调 viewImage。user_id 支持 QQ 号或 self。", getUserProfileFunc)
}
