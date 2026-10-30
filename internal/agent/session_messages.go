package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
)

func (a *Agent) deliverSessionMessage(ctx context.Context, fromKind string, fromTargetID int64, toKind string, toTargetID int64, text string) (*memory.SessionMessage, error) {
	text = strings.TrimSpace(text)
	now := time.Now()
	body := fmt.Sprintf("【来自另一个会话｜不是当前用户发言】\n来源：%s:%d\n投递时间：%s\n%s", fromKind, fromTargetID, now.Format("2006-01-02 15:04:05"), text)
	message := schema.UserAgenticMessage(body)
	message.Extra = map[string]any{"source": "agent_message", "from_kind": fromKind, "from_target_id": fromTargetID, "created_at": now}
	payload, err := sonic.Marshal(message)
	if err != nil {
		return nil, err
	}
	row, entry, err := a.memory.EnqueueSessionAgentMessage(ctx, fromKind, fromTargetID, toKind, toTargetID, text, payload)
	if err != nil {
		return nil, err
	}
	target := a.sessions.get(toKind, toTargetID)
	target.AppendAgentMessage(message, entry.ID, row.CreatedAt)
	if toKind == memory.ConversationKindPrivate {
		a.schedulePrivateThink(toTargetID, row.CreatedAt)
	} else {
		a.scheduleGroupThink(toTargetID, row.CreatedAt)
	}
	return row, nil
}
