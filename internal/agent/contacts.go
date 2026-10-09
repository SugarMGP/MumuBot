package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"mumu-bot/internal/memory"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// 好友申请处理的可识别结果，供后台翻译成可读提示
var (
	ErrFriendRequestMissing = errors.New("好友申请记录不存在")
	ErrFriendRequestHandled = errors.New("好友申请已经处理过")
	ErrFriendRequestGone    = errors.New("好友申请记录已删除")
)

// friendRequestGoneError 保留 API 原始错误文本，只为页面刷新提供分类
type friendRequestGoneError struct{ cause error }

func (e *friendRequestGoneError) Error() string        { return e.cause.Error() }
func (e *friendRequestGoneError) Unwrap() error        { return e.cause }
func (e *friendRequestGoneError) Is(target error) bool { return target == ErrFriendRequestGone }

// HandleFriendRequest 串行处理 pending 到远端操作及本地写回，只按实际 API 失败清理
func (a *Agent) HandleFriendRequest(ctx context.Context, id uint, approve bool) error {
	if a.bot == nil || id == 0 {
		return fmt.Errorf("好友申请参数无效")
	}
	ctx, cancel := context.WithTimeout(ctx, persistenceTimeout)
	defer cancel()
	// ponytail: 单进程全局串行，申请处理量显著增加时再改为按申请加锁
	a.friendRequestsMu.Lock()
	defer a.friendRequestsMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	var request memory.FriendRequest
	if err := a.memory.GetDB().WithContext(ctx).Where("id=?", id).First(&request).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrFriendRequestMissing
		}
		return err
	}
	if request.Status != "pending" {
		return ErrFriendRequestHandled
	}
	if strings.TrimSpace(request.Flag) == "" {
		return fmt.Errorf("好友申请缺少处理凭据")
	}
	apiErr := a.bot.SetFriendRequest(ctx, request.Flag, approve, "")
	if apiErr != nil && !strings.Contains(apiErr.Error(), "No such request") {
		return apiErr
	}
	storeCtx, storeCancel := context.WithTimeout(context.WithoutCancel(ctx), persistenceTimeout)
	defer storeCancel()
	db := a.memory.GetDB().WithContext(storeCtx).Where("id=? AND status=?", id, "pending")
	if apiErr != nil {
		result := db.Delete(&memory.FriendRequest{})
		if result.Error != nil {
			zap.L().Error("清理好友申请记录失败", zap.Error(apiErr), zap.NamedError("database_error", result.Error))
			return errors.Join(apiErr, fmt.Errorf("清理好友申请记录失败: %w", result.Error))
		}
		if result.RowsAffected == 0 {
			return apiErr
		}
		return &friendRequestGoneError{cause: apiErr}
	}
	status := "rejected"
	if approve {
		status = "approved"
	}
	result := db.Model(&memory.FriendRequest{}).Update("status", status)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrFriendRequestHandled
	}
	return nil
}

func (a *Agent) SkipConversationEvent(kind string, targetID int64, arrivalSeq uint64) {
	item := commitItem{skip: true}
	if kind == memory.ConversationKindPrivate {
		a.privateCommits.enqueue(targetID, arrivalSeq, item)
	} else {
		a.groupCommits.enqueue(targetID, arrivalSeq, item)
	}
}
