package topic

import (
	"context"
	"strings"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
)

type Manager struct{ store *DBStore }

func NewManager(store *DBStore) *Manager { return &Manager{store: store} }

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

	seen := make(map[uint]struct{})
	topics := make([]memory.TopicThread, 0, maxPromptTopics)
	addTopic := func(topic memory.TopicThread) {
		if topic.ID == 0 || len(topics) >= maxPromptTopics {
			return
		}
		if _, ok := seen[topic.ID]; ok {
			return
		}
		seen[topic.ID] = struct{}{}
		topics = append(topics, topic)
	}
	for _, messageID := range replyMessageIDs {
		topicID, _, err := m.store.TopicRefForOneBotMessage(ctx, groupID, messageID)
		if err != nil {
			return "", err
		}
		if topicID == 0 {
			continue
		}
		addTopic(memory.TopicThread{ID: topicID, GroupID: groupID})
		if len(topics) >= maxPromptTopics {
			break
		}
	}
	if len(topics) < maxPromptTopics {
		recent, err := m.store.ListRecentTopicThreads(ctx, groupID, throughMessageLogID, 4)
		if err != nil {
			return "", err
		}
		for _, topic := range recent {
			addTopic(topic)
			if len(topics) >= maxPromptTopics {
				break
			}
		}
	}
	if len(topics) < maxPromptTopics && !query.Empty() {
		hits, err := m.store.SearchTopicHits(ctx, query, groupID, throughMessageLogID, 6)
		if err != nil {
			return "", err
		}
		for _, hit := range hits {
			addTopic(hit)
			if len(topics) >= maxPromptTopics {
				break
			}
		}
	}
	var prompt strings.Builder
	for _, topic := range topics {
		record, err := m.store.LatestTopicSummary(ctx, topic.ID, throughMessageLogID)
		if err != nil {
			return "", err
		}
		summary := EmptySummary()
		if record != nil {
			summary = ParseSummary(record.SummaryJSON)
		}
		tail, err := m.store.ListRecentTopicMessages(ctx, topic.ID, throughMessageLogID, 4)
		if err != nil {
			return "", err
		}
		section := strings.TrimSpace(renderTopicPromptSection(topic, summary, tail))
		if section == "" {
			continue
		}
		if prompt.Len() > 0 {
			prompt.WriteString("\n\n")
		}
		prompt.WriteString(section)
	}
	return prompt.String(), nil
}
