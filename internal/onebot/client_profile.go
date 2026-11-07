package onebot

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/zjutjh/onebot-sdk/api"
	"github.com/zjutjh/onebot-sdk/message"
)

// SelfProfile 是当前 QQ 账号可回读的资料
type SelfProfile struct {
	UserID         int64  `json:"user_id"`
	Nickname       string `json:"nickname"`
	Signature      string `json:"signature,omitempty"`
	SignatureKnown bool   `json:"signature_known"`
	Warning        string `json:"warning,omitempty"`
}

// GetSelfProfile 组合登录信息和 self 的陌生人资料；缺字段保持未知
func (c *Client) GetSelfProfile(ctx context.Context) (*SelfProfile, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	login, err := client.GetLoginInfo(ctx, api.GetLoginInfoRequest{})
	if err != nil {
		return nil, err
	}
	if login == nil || login.UserID <= 0 {
		return nil, fmt.Errorf("登录信息没有当前账号")
	}
	profile := &SelfProfile{UserID: int64(login.UserID), Nickname: login.Nickname}
	info, infoErr := client.GetStrangerInfo(ctx, api.GetStrangerInfoRequest{
		UserID:  message.ID(profile.UserID),
		NoCache: api.GetStrangerInfoRequestNoCacheUnion{Raw: []byte("true")},
	})
	if infoErr != nil {
		profile.Warning = infoErr.Error()
		return profile, nil
	}
	if info != nil {
		if strings.TrimSpace(info.Nickname) != "" {
			profile.Nickname = info.Nickname
		}
		profile.Signature = info.LongNick
		profile.SignatureKnown = info.LongNick != ""
	}
	return profile, nil
}

// SetNickname 修改当前账号昵称并刷新自身昵称缓存
func (c *Client) SetNickname(ctx context.Context, nickname string) (map[string]any, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	result, err := client.SetQQProfile(ctx, api.SetQQProfileRequest{Nickname: nickname})
	if err != nil {
		return nil, err
	}
	raw := map[string]any{}
	if result != nil {
		raw = map[string]any(*result)
	}
	if err := nativeProfileError(raw); err != nil {
		return nil, err
	}
	c.selfName.Store(strings.TrimSpace(nickname))
	return raw, nil
}

// SetSignature 只修改签名，不提交昵称覆盖旧值
func (c *Client) SetSignature(ctx context.Context, signature string) (map[string]any, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	result, err := client.SetSelfLongnick(ctx, api.SetSelfLongnickRequest{LongNick: signature})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return map[string]any{}, nil
	}
	raw := map[string]any(*result)
	if err := nativeProfileError(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// SetAvatarBytes 把真实图片字节编码成 base64 后交给头像接口
func (c *Client) SetAvatarBytes(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("头像文件为空")
	}
	client, err := c.apiClient()
	if err != nil {
		return err
	}
	_, err = client.SetQQAvatar(ctx, api.SetQQAvatarRequest{
		File: "base64://" + base64.StdEncoding.EncodeToString(data),
	})
	return err
}

// nativeProfileError 保留 NapCat 放在 data 内的明确失败，不把顶层 ok 当成成功
func nativeProfileError(raw map[string]any) error {
	value, ok := raw["result"]
	if !ok {
		value, ok = raw["retcode"]
	}
	if !ok {
		return nil
	}
	var code int64
	switch typed := value.(type) {
	case float64:
		code = int64(typed)
	case int64:
		code = typed
	case int:
		code = int64(typed)
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return nil
		}
		code = parsed
	default:
		return nil
	}
	if code != 0 {
		return fmt.Errorf("后端资料操作返回非零结果 %d", code)
	}
	return nil
}
