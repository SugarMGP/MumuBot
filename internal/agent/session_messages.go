package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	"gorm.io/gorm"
)

// allowedSessionMessage 是固定的角色投信图：聊天只把需求交给 life，
// worker 只能回报 life，life 可以投给任意会话
func allowedSessionMessage(fromKind, toKind string) bool {
	switch fromKind {
	case memory.ConversationKindLife:
		return true
	case memory.ConversationKindGroup, memory.ConversationKindPrivate:
		return toKind != memory.ConversationKindWorker
	case memory.ConversationKindWorker:
		return toKind == memory.ConversationKindLife
	}
	return false
}

// deliverSessionMessage 是工具发起的内部投信入口：普通消息不允许在目标睡眠时启动新回合
func (a *Agent) deliverSessionMessage(ctx context.Context, fromKind string, fromTargetID int64, toKind string, toTargetID int64, text string, wake bool) (*memory.SessionMessage, error) {
	return a.enqueueSessionMessage(ctx, fromKind, fromTargetID, toKind, toTargetID, text, wake, false)
}

// deliverSessionMessageWithMode 允许 life 派 worker 时显式开始新上下文
func (a *Agent) deliverSessionMessageWithMode(ctx context.Context, fromKind string, fromTargetID int64, toKind string, toTargetID int64, text string, wake bool, contextMode string) (*memory.SessionMessage, error) {
	return a.enqueueSessionMessageMode(ctx, fromKind, fromTargetID, toKind, toTargetID, text, wake, false, contextMode)
}

// enqueueSessionMessage 是所有内部投信的唯一入口：先校验角色路由，再在同一事务内
// 创建目标 transcript 行并取得内部编号，正文和 payload 只保存这一份
func (a *Agent) enqueueSessionMessage(ctx context.Context, fromKind string, fromTargetID int64, toKind string, toTargetID int64, text string, wake, allowSleep bool) (*memory.SessionMessage, error) {
	return a.enqueueSessionMessageMode(ctx, fromKind, fromTargetID, toKind, toTargetID, text, wake, allowSleep, "")
}

func (a *Agent) enqueueSessionMessageMode(ctx context.Context, fromKind string, fromTargetID int64, toKind string, toTargetID int64, text string, wake, allowSleep bool, contextMode string) (*memory.SessionMessage, error) {
	text = strings.TrimSpace(text)
	if contextMode != "" && contextMode != "continue" && contextMode != "new" {
		return nil, fmt.Errorf("上下文模式无效")
	}
	if !allowedSessionMessage(fromKind, toKind) {
		return nil, fmt.Errorf("当前会话不能向 %s 投信", toKind)
	}
	now := time.Now()
	var message *schema.AgenticMessage
	build := func(row *memory.SessionMessage) (json.RawMessage, error) {
		header := "【来自另一个会话｜不是当前用户发言】"
		source := fmt.Sprintf("%s:%d", fromKind, fromTargetID)
		if fromKind == toKind && fromTargetID == toTargetID {
			header = "【生活空间的内部记录｜不是当前用户发言】"
			source = "生活空间"
		}
		silent := ""
		if !wake {
			silent = "\n（这条不请求立即处理，等你下次正常运行时再看）"
		}
		body := fmt.Sprintf("%s\n内部消息编号：%d\n来源：%s\n投递时间：%s%s\n%s",
			header, row.ID, source, now.Format("2006-01-02 15:04:05"), silent, text)
		message = schema.UserAgenticMessage(body)
		message.Extra = map[string]any{
			"source": "agent_message", "kind": "agent_message", "internal_id": row.ID,
			"from_kind": fromKind, "from_target_id": fromTargetID, "created_at": now,
			"wake": wake, "allow_asleep": allowSleep,
		}
		return sonic.Marshal(message)
	}
	var prepare func(*gorm.DB, *memory.SessionMessage) error
	if toKind == memory.ConversationKindLife && wake {
		prepare = func(tx *gorm.DB, _ *memory.SessionMessage) error {
			return memory.ResetLifeRetryTx(tx)
		}
	}
	if contextMode == "new" {
		if fromKind != memory.ConversationKindLife || toKind != memory.ConversationKindWorker {
			return nil, fmt.Errorf("只有 life 派 worker 时可以选择新上下文")
		}
		worker, getErr := a.memory.GetWorkerSessionByID(ctx, uint(toTargetID))
		if getErr != nil || worker.Status != memory.WorkerStatusIdle {
			return nil, fmt.Errorf("worker 当前不在空闲状态，不能开始新上下文")
		}
		if len(a.sessions.get(toKind, toTargetID).PendingMessages()) > 0 {
			return nil, fmt.Errorf("worker 还有未处理任务，不能开始新上下文")
		}
		prepare = func(tx *gorm.DB, row *memory.SessionMessage) error {
			return a.memory.StartWorkerContextTx(tx, uint(toTargetID), row.ID)
		}
	}
	row, entry, err := a.memory.EnqueueSessionAgentMessageWithPrepare(ctx, fromKind, fromTargetID, toKind, toTargetID, text, build, prepare)
	if err != nil {
		return nil, err
	}
	if message == nil || entry == nil {
		return row, nil
	}
	session := a.sessions.get(toKind, toTargetID)
	if contextMode == "new" {
		session.StartNewContext(row.ID)
	}
	session.AppendAgentMessage(message, entry.ID, row.CreatedAt, wake, allowSleep)
	// 普通内部信不能打断睡眠；时间事件和明确恢复请求带 allow_sleep 才允许
	if wake && !(toKind == memory.ConversationKindLife && a.qqSleeping() && !allowSleep) {
		a.scheduleSessionThink(toKind, toTargetID, row.CreatedAt)
	}
	return row, nil
}

// scheduleSessionThink 按目标类型请求一次正常调度；目标正在运行时由模型边界注入
func (a *Agent) scheduleSessionThink(kind string, targetID int64, at time.Time) {
	switch kind {
	case memory.ConversationKindGroup:
		a.scheduleGroupThink(targetID, at)
	case memory.ConversationKindPrivate:
		a.schedulePrivateThink(targetID, at)
	case memory.ConversationKindWorker:
		a.scheduleWorkerThink(targetID)
	case memory.ConversationKindLife:
		a.scheduleLifeThink()
	}
}
