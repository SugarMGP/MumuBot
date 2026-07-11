package tools

import (
	"context"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"go.uber.org/zap"
)

// ==================== 更新成员画像工具 ====================

// UpdateMemberProfileInput 更新成员画像的输入参数
type UpdateMemberProfileInput struct {
	// UserID 群友的QQ号
	UserID int64 `json:"user_id" jsonschema:"description=群友的QQ号"`
	// SpeakStyle 说话风格描述
	SpeakStyle string `json:"speak_style,omitempty" jsonschema:"description=说话风格描述（覆盖之前的描述）"`
	// Interests 兴趣爱好列表
	Interests []string `json:"interests,omitempty" jsonschema:"description=兴趣爱好列表（传入后会覆盖旧列表）"`
	// CommonWords 常用词汇或口头禅
	CommonWords []string `json:"common_words,omitempty" jsonschema:"description=常用词汇或口头禅（传入后会覆盖旧列表）"`
	// Aliases 学到的稳定别称
	Aliases []string `json:"aliases,omitempty" jsonschema:"description=基于明确证据学到的稳定别称（只会追加为学习别称，不会覆盖群名片或原昵称）"`
	// IntimacyDelta 亲密度变化值 -0.3 到 0.3
	IntimacyDelta float64 `json:"intimacy_delta,omitempty" jsonschema:"minimum=-0.3,maximum=0.3,description=亲密度变化值(-0.3到0.3)，正数表示增加亲密度，负数表示降低亲密度"`
}

// UpdateMemberProfileOutput 更新成员画像的输出
type UpdateMemberProfileOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// updateMemberProfileFunc 更新成员画像的实际实现
func updateMemberProfileFunc(ctx context.Context, input *UpdateMemberProfileInput) (*UpdateMemberProfileOutput, error) {
	lc := GetLearningContext(ctx)
	if lc == nil || lc.MemMgr == nil {
		return &UpdateMemberProfileOutput{Success: false, Message: "当前阶段不能直接改写成员画像"}, nil
	}

	if input.UserID == 0 {
		return &UpdateMemberProfileOutput{Success: false, Message: "用户 ID 不能为空"}, nil
	}

	profile, err := lc.MemMgr.GetMemberProfile(input.UserID)
	if err != nil {
		profile, err = lc.MemMgr.GetOrCreateMemberProfile(input.UserID, "")
		if err != nil {
			return &UpdateMemberProfileOutput{Success: false, Message: err.Error()}, nil
		}
	}

	if text := strings.TrimSpace(input.SpeakStyle); text != "" {
		profile.SpeakStyle = text
	}
	if input.Interests != nil {
		b, _ := sonic.MarshalString(input.Interests)
		profile.Interests = b
	}
	if input.CommonWords != nil {
		b, _ := sonic.MarshalString(input.CommonWords)
		profile.CommonWords = b
	}
	if input.Aliases != nil {
		profile.UpsertLearnedAliases(input.Aliases, time.Now())
	}

	delta := input.IntimacyDelta
	profile.Intimacy = min(max(profile.Intimacy+delta, 0), 1)

	if err := lc.MemMgr.UpdateMemberProfileLearned(profile); err != nil {
		return &UpdateMemberProfileOutput{Success: false, Message: err.Error()}, nil
	}

	return &UpdateMemberProfileOutput{Success: true, Message: "已更新对该群友的了解"}, nil
}

// NewUpdateMemberProfileTool 创建更新成员画像工具
func NewUpdateMemberProfileTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"updateMemberProfile",
		"更新你对某个群友的了解。当你发现群友的新特点、说话风格、兴趣爱好时使用。兴趣和常用词列表会按本次结果整体覆盖。也可以根据互动情况调整亲密度。",
		updateMemberProfileFunc,
	)
}

// ==================== 获取成员信息工具 ====================

// GetMemberInfoInput 获取成员信息的输入参数
type GetMemberInfoInput struct {
	// UserID 群友的QQ号
	UserID int64 `json:"user_id" jsonschema:"description=群友的QQ号"`
}

// GetMemberInfoOutput 获取成员信息的输出
type GetMemberInfoOutput struct {
	Success     bool     `json:"success"`
	Message     string   `json:"message,omitempty"`
	Nickname    string   `json:"nickname,omitempty"`
	SpeakStyle  string   `json:"speak_style,omitempty"`
	Interests   []string `json:"interests,omitempty"`
	CommonWords []string `json:"common_words,omitempty"`
	Activity    float64  `json:"activity,omitempty"` // 活跃度 0-1
	Intimacy    float64  `json:"intimacy,omitempty"` // 亲密度 0-1
	MsgCount    int      `json:"msg_count,omitempty"`
}

// getMemberInfoFunc 获取成员信息的实际实现
func getMemberInfoFunc(ctx context.Context, input *GetMemberInfoInput) (*GetMemberInfoOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &GetMemberInfoOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.UserID == 0 {
		return &GetMemberInfoOutput{Success: false, Message: "用户 ID 不能为空"}, nil
	}

	profile, err := tc.MemoryMgr.GetMemberProfile(input.UserID)
	if err != nil {
		return &GetMemberInfoOutput{
			Success: false,
			Message: "不太了解这个人",
		}, nil
	}

	if tc.Bot != nil && tc.GroupID > 0 && tc.MemoryMgr != nil {
		info, infoErr := tc.Bot.GetGroupMemberInfo(ctx, tc.GroupID, input.UserID, false)
		if infoErr != nil {
			zap.L().Debug("获取成员昵称用于回写失败", zap.Int64("group_id", tc.GroupID), zap.Int64("user_id", input.UserID), zap.Error(infoErr))
		} else if latestNickname := strings.TrimSpace(info.Nickname); latestNickname != "" && latestNickname != strings.TrimSpace(profile.Nickname) {
			profile.Nickname = latestNickname
			if err := tc.MemoryMgr.UpdateMemberProfile(profile); err != nil {
				zap.L().Warn("成员昵称回写失败", zap.Int64("group_id", tc.GroupID), zap.Int64("user_id", input.UserID), zap.Error(err))
			}
		}
	}

	var interests, commonWords []string
	if profile.Interests != "" {
		if err := sonic.UnmarshalString(profile.Interests, &interests); err != nil {
			zap.L().Warn("反序列化 interests 失败", zap.Error(err))
		}
	}
	if profile.CommonWords != "" {
		if err := sonic.UnmarshalString(profile.CommonWords, &commonWords); err != nil {
			zap.L().Warn("反序列化 commonWords 失败", zap.Error(err))
		}
	}

	return &GetMemberInfoOutput{
		Success:     true,
		Nickname:    profile.Nickname,
		SpeakStyle:  profile.SpeakStyle,
		Interests:   interests,
		CommonWords: commonWords,
		Activity:    profile.Activity,
		Intimacy:    profile.Intimacy,
		MsgCount:    profile.MsgCount,
	}, nil
}

// NewGetMemberInfoTool 创建获取成员信息工具
func NewGetMemberInfoTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getMemberInfo",
		"查看你对某个群友的了解。",
		getMemberInfoFunc,
	)
}
