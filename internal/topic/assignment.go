package topic

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/utils"

	"go.uber.org/zap"
)

const (
	assignmentContextSize    = 10
	assignmentCandidateLimit = assignmentBatchSize + assignmentContextSize
)

type topicAssignJob struct {
	messageLogID     uint
	nickname         string
	text             string
	messageTime      time.Time
	replyToMessageID int64
	replyTopicID     uint
}

type topicAssignmentContextMessage struct {
	MessageLogID     uint
	OneBotMessageID  int64
	Nickname         string
	Text             string
	MessageTime      time.Time
	ReplyToMessageID *int64
	TopicID          *uint
}

type topicAssignmentSubmission struct {
	Assignments []topicAssignmentDecision `json:"assignments" jsonschema:"description=逐条消息的话题分配结果"`
}

type topicAssignmentDecision struct {
	MessageKey  string `json:"message_key" jsonschema:"description=输入消息的编号，例如 m123"`
	Action      string `json:"action" jsonschema:"enum=no_topic,enum=new,enum=reuse,description=分配动作"`
	TopicID     uint   `json:"topic_id,omitempty" jsonschema:"description=reuse 时填写已有话题 ID"`
	NewTopicKey string `json:"new_topic_key,omitempty" jsonschema:"description=创建新话题时填写批内新话题临时编号"`
}

type topicAssignmentCandidate struct {
	ID               uint
	Source           string
	Summary          string
	Tail             string
	LastMessageTime  time.Time
	SourceMessageIDs []uint
}

func (m *Manager) maybeScheduleAssignment(groupID int64, forceDrain bool) {
	minimum := assignmentBatchSize
	if forceDrain {
		minimum = 1
	}
	ready, err := m.store.HasPendingTopicAssignmentMessages(m.ctx, groupID, minimum)
	if err != nil {
		zap.L().Warn("读取话题归属水位失败", zap.Int64("group_id", groupID), zap.Error(err))
		return
	}
	if !ready {
		return
	}
	m.enqueueAssignment(groupID, forceDrain)
}

func (m *Manager) enqueueAssignment(groupID int64, forceDrain bool) {
	if groupID == 0 {
		return
	}
	m.mu.Lock()
	if current, ok := m.assigning[groupID]; ok {
		m.assigning[groupID] = current || forceDrain
		m.mu.Unlock()
		return
	}
	m.assigning[groupID] = forceDrain
	m.mu.Unlock()
	select {
	case m.assignQueue <- groupID:
	case <-m.ctx.Done():
		m.finishAssignment(groupID)
	default:
		m.finishAssignment(groupID)
		zap.L().Warn("话题归属队列已满，消息保持待处理", zap.Int64("group_id", groupID))
	}
}

func (m *Manager) finishAssignment(groupID int64) bool {
	m.mu.Lock()
	forceDrain := m.assigning[groupID]
	delete(m.assigning, groupID)
	m.mu.Unlock()
	return forceDrain
}

func (m *Manager) assignmentWorker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case groupID := <-m.assignQueue:
			succeeded := m.assignGroup(groupID)
			forceDrain := m.finishAssignment(groupID)
			if succeeded {
				m.maybeScheduleAssignment(groupID, forceDrain)
			}
		}
	}
}

func (m *Manager) assignGroup(groupID int64) bool {
	ctx, cancel := context.WithTimeout(m.ctx, assignmentTimeout)
	defer cancel()
	rows, err := m.store.ListPendingTopicAssignmentMessages(ctx, groupID, assignmentBatchSize)
	if err != nil {
		zap.L().Warn("读取待归属消息失败", zap.Int64("group_id", groupID), zap.Error(err))
		return false
	}
	if len(rows) == 0 {
		return true
	}
	history, err := m.store.ListTopicAssignmentContext(ctx, groupID, rows[0].ID, assignmentContextSize)
	if err != nil {
		zap.L().Warn("读取话题归属上文失败", zap.Int64("group_id", groupID), zap.Error(err))
		return false
	}
	jobs := make([]topicAssignJob, 0, len(rows))
	for _, row := range rows {
		replyTo := int64(0)
		if row.ReplyToMessageID != nil {
			replyTo = *row.ReplyToMessageID
		}
		jobs = append(jobs, topicAssignJob{
			messageLogID: row.ID, nickname: strings.TrimSpace(row.Nickname), text: strings.TrimSpace(row.TextContent),
			messageTime: row.MessageTime, replyToMessageID: replyTo,
		})
	}
	candidates, replyTopics, err := m.assignmentCandidates(ctx, groupID, rows, history)
	if err != nil {
		zap.L().Warn("构建话题候选失败", zap.Int64("group_id", groupID), zap.Error(err))
		return false
	}
	for i := range jobs {
		jobs[i].replyTopicID = replyTopics[jobs[i].messageLogID]
	}
	raw, err := llm.GenerateStructuredJSONObject[topicAssignmentSubmission](llm.WithTask(ctx, "topic_assignment", config.Get().ModelTiers.Low.Model), m.model, buildTopicAssignmentPrompt(groupID, history, jobs, candidates))
	if err != nil {
		zap.L().Warn("话题归属失败，保留待处理", zap.Int64("group_id", groupID), zap.Error(err))
		return false
	}
	items := assignmentItems(jobs, normalizeTopicAssignmentSubmission(&raw), candidates)
	if len(items) == 0 {
		return false
	}
	sourceMessageIDs := make([]uint, 0, len(rows)+len(history)+len(candidates)*4)
	for _, row := range rows {
		sourceMessageIDs = append(sourceMessageIDs, row.ID)
	}
	for _, item := range history {
		sourceMessageIDs = append(sourceMessageIDs, item.MessageLogID)
	}
	for _, candidate := range candidates {
		sourceMessageIDs = append(sourceMessageIDs, candidate.SourceMessageIDs...)
	}
	updatedTopicIDs, err := m.store.ApplyTopicAssignmentBatch(ctx, groupID, utils.UniqueIDs(sourceMessageIDs), items)
	if err != nil {
		zap.L().Warn("提交话题归属失败，保留待处理", zap.Int64("group_id", groupID), zap.Error(err))
		return false
	}
	for _, topicID := range updatedTopicIDs {
		m.scheduleSummary(topicID)
	}
	return true
}

func assignmentItems(jobs []topicAssignJob, decisions []topicAssignmentDecision, candidates []topicAssignmentCandidate) []AssignmentBatchItem {
	jobByKey := make(map[string]topicAssignJob, len(jobs))
	for _, job := range jobs {
		jobByKey[assignmentMessageKey(job)] = job
	}
	candidateIDs := make(map[uint]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidateIDs[candidate.ID] = struct{}{}
	}
	seen := make(map[uint]struct{}, len(decisions))
	items := make([]AssignmentBatchItem, 0, len(decisions))
	for _, decision := range decisions {
		job, ok := jobByKey[decision.MessageKey]
		if !ok {
			zap.L().Warn("话题模型返回未知消息编号", zap.String("message_key", decision.MessageKey))
			continue
		}
		if _, duplicate := seen[job.messageLogID]; duplicate {
			zap.L().Warn("话题模型重复返回消息", zap.Uint("message_log_id", job.messageLogID))
			continue
		}
		seen[job.messageLogID] = struct{}{}
		item := AssignmentBatchItem{MessageLogID: job.messageLogID, Action: AssignmentAction(decision.Action), TopicID: decision.TopicID, NewTopicKey: decision.NewTopicKey}
		switch item.Action {
		case AssignmentActionNoTopic:
		case AssignmentActionReuse:
			if _, ok := candidateIDs[item.TopicID]; !ok {
				zap.L().Warn("话题模型引用了非候选话题", zap.Uint("message_log_id", job.messageLogID), zap.Uint("topic_id", item.TopicID))
				continue
			}
		case AssignmentActionNew:
			if item.NewTopicKey == "" {
				zap.L().Warn("话题模型创建话题时缺少临时编号", zap.Uint("message_log_id", job.messageLogID))
				continue
			}
		default:
			zap.L().Warn("话题模型返回非法归属动作", zap.Uint("message_log_id", job.messageLogID), zap.String("action", decision.Action))
			continue
		}
		items = append(items, item)
	}
	if len(items) != len(jobs) {
		zap.L().Warn("话题模型未完整返回有效归属", zap.Int("expected", len(jobs)), zap.Int("accepted", len(items)))
		return nil
	}
	return items
}

func (m *Manager) assignmentCandidates(ctx context.Context, groupID int64, rows []memory.MessageLog, history []topicAssignmentContextMessage) ([]topicAssignmentCandidate, map[uint]uint, error) {
	ids := make([]uint, 0, assignmentCandidateLimit)
	sources := make(map[uint]string, assignmentCandidateLimit)
	addCandidate := func(id uint, source string) {
		if id == 0 || len(ids) >= assignmentCandidateLimit {
			return
		}
		if _, exists := sources[id]; exists {
			return
		}
		ids = append(ids, id)
		sources[id] = source
	}
	replyTopics := make(map[uint]uint)
	replySourceIDs := make(map[uint][]uint)
	for _, row := range rows {
		if row.ReplyToMessageID == nil {
			continue
		}
		id, sourceMessageID, err := m.store.TopicRefForOneBotMessage(ctx, groupID, *row.ReplyToMessageID)
		if err != nil {
			return nil, nil, err
		}
		if id != 0 {
			replyTopics[row.ID] = id
			if sourceMessageID != 0 && !slices.Contains(replySourceIDs[id], sourceMessageID) {
				replySourceIDs[id] = append(replySourceIDs[id], sourceMessageID)
			}
			addCandidate(id, "reply")
		}
	}
	for _, item := range history {
		if item.TopicID != nil {
			addCandidate(*item.TopicID, "context")
		}
	}
	throughMessageLogID := rows[0].ID - 1
	recent, err := m.store.ListRecentTopicThreads(ctx, groupID, throughMessageLogID, 6)
	if err != nil {
		return nil, nil, err
	}
	for _, topic := range recent {
		addCandidate(topic.ID, "recent")
	}
	if len(ids) < assignmentCandidateLimit {
		queryParts := make([]string, 0, len(rows))
		for _, row := range rows {
			if row.TextContent != "" {
				queryParts = append(queryParts, row.TextContent)
			}
		}
		query, err := m.store.memory.PrepareHybridQuery(ctx, queryParts)
		if err != nil {
			return nil, nil, err
		}
		hits, err := m.store.SearchTopicHits(ctx, query, groupID, throughMessageLogID, 6)
		if err != nil {
			return nil, nil, err
		}
		for _, hit := range hits {
			addCandidate(hit.ID, "semantic")
		}
	}
	result := make([]topicAssignmentCandidate, 0, len(ids))
	for _, id := range ids {
		summaryRecord, err := m.store.LatestTopicSummary(ctx, id, throughMessageLogID)
		if err != nil {
			return nil, nil, err
		}
		summary := EmptySummary()
		if summaryRecord != nil {
			summary = ParseSummary(summaryRecord.SummaryJSON)
		}
		tail, err := m.store.ListRecentTopicMessages(ctx, id, throughMessageLogID, 4)
		if err != nil {
			return nil, nil, err
		}
		sourceMessageIDs := make([]uint, len(tail))
		for i, message := range tail {
			sourceMessageIDs[i] = message.ID
		}
		for _, sourceMessageID := range replySourceIDs[id] {
			if !slices.Contains(sourceMessageIDs, sourceMessageID) {
				sourceMessageIDs = append(sourceMessageIDs, sourceMessageID)
			}
		}
		candidate := topicAssignmentCandidate{
			ID: id, Source: sources[id], Summary: renderTopicSummaryForAssignment(summary),
			Tail: renderAssignmentTail(tail), SourceMessageIDs: sourceMessageIDs,
		}
		if len(tail) > 0 {
			candidate.LastMessageTime = tail[len(tail)-1].MessageTime
		}
		result = append(result, candidate)
	}
	return result, replyTopics, nil
}

func normalizeTopicAssignmentSubmission(raw *topicAssignmentSubmission) []topicAssignmentDecision {
	if raw == nil || len(raw.Assignments) == 0 {
		return nil
	}
	result := make([]topicAssignmentDecision, 0, len(raw.Assignments))
	for _, item := range raw.Assignments {
		item.MessageKey = strings.TrimSpace(item.MessageKey)
		item.Action = strings.ToLower(strings.TrimSpace(item.Action))
		item.NewTopicKey = strings.TrimSpace(item.NewTopicKey)
		result = append(result, item)
	}
	return result
}

func buildTopicAssignmentPrompt(groupID int64, history []topicAssignmentContextMessage, messages []topicAssignJob, candidates []topicAssignmentCandidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "群 %d 有一批新消息需要分配话题。先结合已处理上文和整批新消息划分连贯会话，再逐条返回归属。\n", groupID)
	b.WriteString("- 追问、澄清、复述、短附和及依赖前文的句子应跟随所属会话，不要因局部措辞变化创建新话题。\n")
	b.WriteString("- reuse: 归入已有候选话题，topic_id 必须来自候选；较早的 semantic 候选只有明确属于同一实体、事件或未完事项时才能复用。\n")
	b.WriteString("- new: 只有出现可独立描述的主体、事件或讨论目标变化时才创建；同一新话题的多条消息必须复用同一个 new_topic_key。\n")
	b.WriteString("- no_topic: 纯灌水、纯表情、单字附和或没有可持续语义的孤立消息。\n")
	b.WriteString("- 每个 message_key 只能返回一次，assignments 必须完整覆盖本批消息。\n")
	b.WriteString("\n已处理上文：\n")
	if len(history) == 0 {
		b.WriteString("无\n")
	}
	for _, item := range history {
		assigned := "no_topic"
		if item.TopicID != nil {
			assigned = strconv.FormatUint(uint64(*item.TopicID), 10)
		}
		replyTo := int64(0)
		if item.ReplyToMessageID != nil {
			replyTo = *item.ReplyToMessageID
		}
		fmt.Fprintf(&b, "message_log_id=%d message_id=%d assigned_topic_id=%s time=%s reply_to=%d nickname=%q text=%q\n",
			item.MessageLogID, item.OneBotMessageID, assigned, item.MessageTime.Format("2006-01-02 15:04:05"), replyTo, item.Nickname, item.Text)
	}
	b.WriteString("\n候选话题：\n")
	if len(candidates) == 0 {
		b.WriteString("无\n")
	}
	for _, candidate := range candidates {
		lastTime := "无"
		distance := "未知"
		if !candidate.LastMessageTime.IsZero() {
			lastTime = candidate.LastMessageTime.Format("2006-01-02 15:04:05")
			if len(messages) > 0 {
				age := messages[0].messageTime.Sub(candidate.LastMessageTime)
				if age < 0 {
					age = 0
				}
				distance = age.Round(time.Minute).String()
			}
		}
		fmt.Fprintf(&b, "topic_id=%d source=%s last_message_time=%s distance_to_batch=%s\n", candidate.ID, candidate.Source, lastTime, distance)
		if candidate.Summary != "" {
			b.WriteString(candidate.Summary + "\n")
		}
		if candidate.Tail != "" {
			b.WriteString("最近原文：\n" + candidate.Tail + "\n")
		}
	}
	b.WriteString("\n待分配消息：\n")
	for _, msg := range messages {
		fmt.Fprintf(&b, "%s time=%s reply_to=%d", assignmentMessageKey(msg), msg.messageTime.Format("2006-01-02 15:04:05"), msg.replyToMessageID)
		if msg.replyTopicID != 0 {
			fmt.Fprintf(&b, " reply_topic_id=%d", msg.replyTopicID)
		}
		fmt.Fprintf(&b, " nickname=%q text=%q\n", msg.nickname, msg.text)
	}
	return b.String()
}

func renderAssignmentTail(messages []memory.MessageLog) string {
	lines := make([]string, 0, len(messages))
	for _, item := range messages {
		text := strings.TrimSpace(item.TextContent)
		if text == "" {
			continue
		}
		replyTo := int64(0)
		if item.ReplyToMessageID != nil {
			replyTo = *item.ReplyToMessageID
		}
		lines = append(lines, fmt.Sprintf("time=%s reply_to=%d nickname=%q text=%q", item.MessageTime.Format("2006-01-02 15:04:05"), replyTo, item.Nickname, text))
	}
	return strings.Join(lines, "\n")
}

func assignmentMessageKey(job topicAssignJob) string {
	return "m" + strconv.FormatUint(uint64(job.messageLogID), 10)
}
