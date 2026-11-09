package agent

import (
	"context"
	"fmt"
	"time"

	"mumu-bot/internal/modelstats"
	"mumu-bot/internal/tools"

	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// injectLifeUsageHint 同一次 think 只追加一次软提醒，不创建新的唤醒输入
func injectLifeUsageHint(ctx context.Context, session *Session) (*schema.AgenticMessage, error) {
	state := runStateFrom(ctx)
	if state == nil || state.lifeUsageChecked {
		return nil, nil
	}
	state.lifeUsageChecked = true
	limit := state.config.Agent.LifeDailyRequestLimit
	if limit == 0 {
		return nil, nil
	}
	tc := tools.GetToolContext(ctx)
	usage, err := modelstats.QueryDailyUsage(ctx, tc.MemoryMgr.GetDB(), time.Now(), limit)
	if err != nil {
		zap.L().Warn("读取每日生活用量失败，本轮不发送额度提醒", zap.Error(err))
		return nil, nil
	}
	if usage.Status != "near" && usage.Status != "over" {
		return nil, nil
	}
	level := "接近"
	if usage.Status == "over" {
		level = "达到或超过"
	}
	text := fmt.Sprintf("%s（上海自然日）的生活与工作模型请求已%s参考额度：共 %d 次，生活 %d 次、工作 %d 次，参考额度 %d 次。统计通常约有十秒延迟，包含失败和摘要调用。这只是用量提醒，可以合并不急的事项、减少无意义自检，由你自主决定安排，不要求停机、睡眠或向主人发消息。", usage.Date, level, usage.Total, usage.Life, usage.Worker, limit)
	message := schema.UserAgenticMessage(systemNote("每日用量", text))
	message.Extra = map[string]any{"kind": "daily_usage_hint"}
	if err := session.appendHeadEntry(ctx, tc.MemoryMgr, kindSystemNote, message); err != nil {
		return nil, err
	}
	return message, nil
}
