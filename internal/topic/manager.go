package topic

import (
	"context"
	"strings"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"

	"gorm.io/gorm"
)

type Manager struct{ store *DBStore }

func NewManager(db *gorm.DB) *Manager { return &Manager{store: &DBStore{db: db}} }

func (m *Manager) PersistMessage(ctx context.Context, msg *onebot.GroupMessage, isMentioned bool) (*memory.MessageLog, bool, error) {
	if msg == nil || msg.MessageID == 0 || msg.GroupID == 0 {
		return nil, false, nil
	}
	var replyTo *int64
	if msg.Reply != nil && msg.Reply.MessageID != 0 {
		id := msg.Reply.MessageID
		replyTo = &id
	}
	item, created, err := m.store.PersistMessageLog(ctx, memory.MessageLog{
		OneBotMessageID: msg.MessageID, GroupID: msg.GroupID, UserID: msg.UserID, Nickname: msg.Nickname,
		TextContent: strings.TrimSpace(msg.Content), DisplayContent: msg.FinalContent,
		ReplyToMessageID: replyTo, IsMentioned: isMentioned, MessageTime: msg.Time,
	})
	if err != nil {
		return nil, false, err
	}

	return item, created, nil
}

func (m *Manager) BuildPromptContext(ctx context.Context, groupID int64, query memory.HybridQuery, throughMessageLogID uint, replyMessageIDs []int64) (string, error) {
	const maxPromptTopics = 3
	seen := map[uint]bool{}
	sections := []string{}
	add := func(id uint) error {
		if id == 0 || seen[id] || len(sections) >= maxPromptTopics {
			return nil
		}
		seen[id] = true
		record, err := m.store.LatestTopicSummary(ctx, id, throughMessageLogID)
		if err != nil {
			return err
		}
		summary := EmptySummary()
		if record != nil && record.SourcesValid {
			summary = ParseSummary(record.SummaryJSON)
		}
		tail, err := m.store.ListRecentTopicMessages(ctx, id, throughMessageLogID, 4)
		if err != nil {
			return err
		}
		if strings.TrimSpace(summary.Gist) == "" && renderMessageTail(tail, 4) == "" {
			return nil
		}
		sections = append(sections, renderTopicPromptSection(memory.TopicThread{ID: id, GroupID: groupID}, summary, tail))
		return nil
	}
	for _, messageID := range replyMessageIDs {
		id, _, err := m.store.TopicRefForOneBotMessage(ctx, groupID, messageID)
		if err != nil {
			return "", err
		}
		if err := add(id); err != nil {
			return "", err
		}
		if len(sections) >= maxPromptTopics {
			break
		}
	}
	if len(sections) < maxPromptTopics && !query.Empty() {
		hits, err := m.store.SearchTopicHits(ctx, query, groupID, throughMessageLogID, 6)
		if err != nil {
			return "", err
		}
		for _, hit := range hits {
			if err := add(hit.ID); err != nil {
				return "", err
			}
			if len(sections) >= maxPromptTopics {
				break
			}
		}
	}
	if len(sections) < maxPromptTopics {
		recent, err := m.store.ListRecentTopicThreads(ctx, groupID, throughMessageLogID, 4)
		if err != nil {
			return "", err
		}
		for _, topic := range recent {
			if err := add(topic.ID); err != nil {
				return "", err
			}
			if len(sections) >= maxPromptTopics {
				break
			}
		}
	}
	return strings.Join(sections, "\n\n"), nil
}
