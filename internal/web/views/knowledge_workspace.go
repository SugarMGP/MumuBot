package views

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/web/services"
)

type KnowledgeWorkspaceData struct {
	Filter        services.KnowledgeFilter
	Conversations []services.KnowledgeConversation
	Note          *memory.ConversationAgentState
	CurrentURL    string
}

// conversationLabel 渲染知识筛选里的会话选项
func conversationLabel(item services.KnowledgeConversation) string {
	prefix := "群"
	if item.Kind == memory.ConversationKindPrivate {
		prefix = "好友"
	}
	if name := strings.TrimSpace(item.Name); name != "" {
		return fmt.Sprintf("%s %d（%s）", prefix, item.TargetID, name)
	}
	return fmt.Sprintf("%s %d", prefix, item.TargetID)
}

// conversationText 渲染知识条目所属会话
func conversationText(kind string, targetID int64) string {
	if kind == memory.ConversationKindPrivate {
		return fmt.Sprintf("好友 %d", targetID)
	}
	return fmt.Sprintf("群 %d", targetID)
}

// WithQuery 在已有页面地址上更新查询参数，空值移除参数
func WithQuery(raw string, pairs ...string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			q.Del(pairs[i])
		} else {
			q.Set(pairs[i], pairs[i+1])
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func knowledgeDetailURL(id uint, returnTo string) string {
	return WithQuery(knowledgeHref(id), "return_to", returnTo)
}

func knowledgeSubject(item memory.KnowledgeItem, selfID int64, meta services.KnowledgeMetadata) string {
	if item.SubjectUserID == 0 || item.SubjectUserID == selfID {
		return memorySubjectText(item.SubjectUserID, selfID)
	}
	if meta.Name != "" {
		return meta.Name
	}
	return "QQ " + strconv.FormatInt(item.SubjectUserID, 10)
}

func evidenceSummary(meta services.KnowledgeMetadata) string {
	switch valid := meta.ValidMessages(); {
	case meta.Messages == 0:
		return "暂无原文"
	case valid <= 0:
		return "原文依据已失效"
	default:
		return fmt.Sprintf("原文 %d 条", valid)
	}
}

func firstEvidenceOpen(sets []memory.KnowledgeEvidence, index int) bool {
	for i, set := range sets {
		if set.Valid {
			return index == i
		}
	}
	return index == 0
}
