package tools

import (
	"context"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"go.uber.org/zap"
)

// ==================== 更新成员画像工具 ====================

// mergeAndDeduplicateStrings 合并两个字符串切片并去重
func mergeAndDeduplicateStrings(existing []string, newItems []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)

	// 先添加已有的
	for _, item := range existing {
		if item != "" && !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}

	// 再添加新的
	for _, item := range newItems {
		if item != "" && !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}

	return result
}

// UpdateMemberProfileInput 更新成员画像的输入参数
type UpdateMemberProfileInput struct {
	// UserID 群友的QQ号
	UserID int64 `json:"user_id" jsonschema:"description=群友的QQ号"`
	// SpeakStyle 说话风格描述
	SpeakStyle string `json:"speak_style,omitempty" jsonschema:"description=说话风格描述"`
	// Interests 兴趣爱好列表
	Interests []string `json:"interests,omitempty" jsonschema:"description=兴趣爱好列表（只传入新增的项）"`
	// CommonWords 常用词汇或口头禅
	CommonWords []string `json:"common_words,omitempty" jsonschema:"description=常用词汇或口头禅（只传入新增的项）"`
	// Intimacy 亲密度 0-1，根据互动情况调整
	Intimacy *float64 `json:"intimacy,omitempty" jsonschema:"description=亲密度0-1，根据与对方的互动频率、聊天深度、情感连接来评估。"`
}

// UpdateMemberProfileOutput 更新成员画像的输出
type UpdateMemberProfileOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// updateMemberProfileFunc 更新成员画像的实际实现
func updateMemberProfileFunc(ctx context.Context, input *UpdateMemberProfileInput) (*UpdateMemberProfileOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &UpdateMemberProfileOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.UserID == 0 {
		return &UpdateMemberProfileOutput{Success: false, Message: "用户 ID 不能为空"}, nil
	}

	profile, err := tc.MemoryMgr.GetMemberProfile(input.UserID)
	if err != nil {
		return &UpdateMemberProfileOutput{Success: false, Message: err.Error()}, nil
	}

	if input.SpeakStyle != "" {
		profile.SpeakStyle = input.SpeakStyle
	}
	if len(input.Interests) > 0 {
		// 解析已有的兴趣爱好
		var existingInterests []string
		if profile.Interests != "" {
			if err := sonic.UnmarshalString(profile.Interests, &existingInterests); err != nil {
				existingInterests = []string{}
			}
		}
		// 合并并去重
		mergedInterests := mergeAndDeduplicateStrings(existingInterests, input.Interests)
		b, _ := sonic.MarshalString(mergedInterests)
		profile.Interests = b
	}
	if len(input.CommonWords) > 0 {
		// 解析已有的常用词汇
		var existingCommonWords []string
		if profile.CommonWords != "" {
			if err := sonic.UnmarshalString(profile.CommonWords, &existingCommonWords); err != nil {
				existingCommonWords = []string{}
			}
		}
		// 合并并去重
		mergedCommonWords := mergeAndDeduplicateStrings(existingCommonWords, input.CommonWords)
		b, _ := sonic.MarshalString(mergedCommonWords)
		profile.CommonWords = b
	}
	if input.Intimacy != nil {
		// 限制亲密度在 0-1 范围内
		intimacy := *input.Intimacy
		if intimacy < 0 {
			intimacy = 0
		} else if intimacy > 1 {
			intimacy = 1
		}
		profile.Intimacy = intimacy
	}

	if err := tc.MemoryMgr.UpdateMemberProfile(profile); err != nil {
		output := &UpdateMemberProfileOutput{Success: false, Message: err.Error()}
		LogToolCall("updateMemberProfile", input, output, err)
		return output, nil
	}

	output := &UpdateMemberProfileOutput{Success: true, Message: "已更新对该群友的了解"}
	LogToolCall("updateMemberProfile", input, output, nil)
	return output, nil
}

// NewUpdateMemberProfileTool 创建更新成员画像工具
func NewUpdateMemberProfileTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"updateMemberProfile",
		"更新你对某个群友的了解。当你发现群友的新特点、说话风格、兴趣爱好时使用。也可以根据互动情况调整亲密度（intimacy）。",
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
		output := &GetMemberInfoOutput{
			Success: false,
			Message: "不太了解这个人",
		}
		LogToolCall("getMemberInfo", input, output, err)
		return output, nil
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

	output := &GetMemberInfoOutput{
		Success:     true,
		Nickname:    profile.Nickname,
		SpeakStyle:  profile.SpeakStyle,
		Interests:   interests,
		CommonWords: commonWords,
		Activity:    profile.Activity,
		Intimacy:    profile.Intimacy,
		MsgCount:    profile.MsgCount,
	}
	LogToolCall("getMemberInfo", input, output, nil)
	return output, nil
}

// NewGetMemberInfoTool 创建获取成员信息工具
func NewGetMemberInfoTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"getMemberInfo",
		"查看你对某个群友的了解。",
		getMemberInfoFunc,
	)
}
