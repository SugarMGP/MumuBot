package topic

import (
	"context"
	"errors"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/llm"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/utils"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"go.uber.org/zap"
)

const (
	topicAssignQueueSize  = 1024
	topicAssignTimeout    = 90 * time.Second
	maxTopicAssignWorkers = 2
)

type Store interface {
	ListActiveTopicThreads(ctx context.Context, groupID int64) ([]memory.TopicThread, error)
	ListArchivedTopicThreadsNeedingSummary(ctx context.Context, groupID int64) ([]memory.TopicThread, error)
	ListRecentTopicMessages(ctx context.Context, topicID uint, limit int) ([]memory.MessageLog, error)
	ListRecentTopicMessagesByTopicIDs(ctx context.Context, topicIDs []uint, limit int) (map[uint][]memory.MessageLog, error)
	ListRecentTopicParticipants(ctx context.Context, topicID uint, limit int) ([]memory.TopicParticipantRef, error)
	CountTopicMessagesAfterSummary(ctx context.Context, topicID uint) (int, error)
	CountTopicMessagesAfterSummaryByTopicIDs(ctx context.Context, topics []memory.TopicThread) (map[uint]int, error)
	GetTopicMessagesAfterSummary(ctx context.Context, topicID uint, limit int) ([]memory.MessageLog, error)
	UpdateTopicSummary(ctx context.Context, topicID uint, summary memory.TopicSummary, summaryUntil uint, capturedAt time.Time) error
	GetTopicThread(ctx context.Context, topicID uint) (*memory.TopicThread, error)
	SearchArchivedTopicThreadHits(ctx context.Context, query string, groupID int64, topK int, threshold float64) ([]ThreadSearchHit, error)
	ArchiveTopicThreadForRepair(ctx context.Context, groupID int64, topicID uint) error
	SyncTopicThreadVector(ctx context.Context, topicID uint) error
	GetMessageLogByID(messageID string) (*memory.MessageLog, error)
	CreateMessageLog(ctx context.Context, msg memory.MessageLog) (*memory.MessageLog, error)
	ListPendingTopicAssignmentMessages(ctx context.Context, groupID int64, limit int) ([]memory.MessageLog, error)
	ApplyTopicAssignmentBatch(ctx context.Context, input AssignmentBatchInput) (AssignmentBatchResult, error)
}

type (
	topicSummaryFunc func(ctx context.Context, oldSummary memory.TopicSummary, newMessages []memory.MessageLog) (memory.TopicSummary, error)
	topicAssignFunc  func(ctx context.Context, groupID int64, messages []topicAssignJob, candidates []topicAssignmentCandidate) ([]topicAssignmentDecision, error)
)

type topicRuntimeState struct {
	thread       memory.TopicThread
	tail         []*onebot.GroupMessage
	participants []memory.TopicParticipantRef
	dirty        bool
	pendingCount int
	queued       bool
}

type topicGroupState struct {
	topics        map[uint]*topicRuntimeState
	pendingAssign map[uint]struct{}
}

type topicSummaryTask struct {
	groupID int64
	topicID uint
}

type topicSummaryCall struct {
	done  chan struct{}
	topic *memory.TopicThread
	err   error
}

type topicAssignJob struct {
	groupID      int64
	messageLogID uint
	message      *onebot.GroupMessage
}

type PromptContext struct {
	Prompt         string
	RetrievalQuery string
	TopicIDs       []uint
	MainTopicID    uint
}

type PersistMessageInput struct {
	Message      *onebot.GroupMessage
	IsMentioned  bool
	ForwardsJSON string
}

type Manager struct {
	ctx          context.Context
	store        Store
	assignModel  model.BaseChatModel
	summaryModel model.BaseChatModel
	summaryFn    topicSummaryFunc
	assignFn     topicAssignFunc
	groupStates  map[int64]*topicGroupState
	groupLocks   map[int64]*sync.Mutex

	statesMu sync.RWMutex
	locksMu  sync.Mutex

	summaryQueue chan topicSummaryTask
	assignQueue  chan topicAssignJob
	stopCh       chan struct{}
	closeOnce    sync.Once
	wg           sync.WaitGroup

	summaryMu       sync.Mutex
	summaryInFlight map[uint]*topicSummaryCall

	assignMu       sync.Mutex
	assignBuffers  map[int64][]topicAssignJob
	assignInFlight map[int64]struct{}
	assignSem      chan struct{}
	assignDone     chan struct{}
	closed         bool
	batchSize      int
}

func NewManager(ctx context.Context, store Store, chatModel model.ToolCallingChatModel, bufferCapacity ...int) *Manager {
	if ctx == nil {
		ctx = context.Background()
	}
	capacity := actualMessageBufferCapacity()
	if len(bufferCapacity) > 0 {
		capacity = bufferCapacity[0]
	}
	tm := &Manager{
		ctx:             ctx,
		store:           store,
		groupStates:     make(map[int64]*topicGroupState),
		groupLocks:      make(map[int64]*sync.Mutex),
		summaryQueue:    make(chan topicSummaryTask, 128),
		assignQueue:     make(chan topicAssignJob, topicAssignQueueSize),
		stopCh:          make(chan struct{}),
		summaryInFlight: make(map[uint]*topicSummaryCall),
		assignBuffers:   make(map[int64][]topicAssignJob),
		assignInFlight:  make(map[int64]struct{}),
		assignSem:       make(chan struct{}, maxTopicAssignWorkers),
		assignDone:      make(chan struct{}),
		batchSize:       topicAssignmentBatchSize(capacity),
	}
	tm.summaryModel = chatModel
	tm.assignModel = chatModel
	tm.summaryFn = tm.generateSummary
	tm.assignFn = tm.generateTopicAssignments
	tm.wg.Add(2)
	go tm.summaryWorker()
	go tm.assignmentScheduler()
	return tm
}

func (tm *Manager) Close() {
	tm.closeOnce.Do(func() {
		tm.assignMu.Lock()
		tm.closed = true
		tm.assignMu.Unlock()

		close(tm.stopCh)
		<-tm.assignDone
		tm.drainAssignmentBuffers()
		tm.wg.Wait()
	})
}

func (tm *Manager) withGroupLock(groupID int64, fn func() error) error {
	lock := tm.getGroupLock(groupID)
	lock.Lock()
	defer lock.Unlock()
	return fn()
}

func (tm *Manager) LoadFromDB(groupIDs []int64) error {
	for _, groupID := range groupIDs {
		if groupID == 0 {
			continue
		}

		for {
			var victimTopicID uint
			if err := tm.withGroupLock(groupID, func() error {
				activeTopics, err := tm.store.ListActiveTopicThreads(tm.ctx, groupID)
				if err != nil {
					return err
				}
				if len(activeTopics) <= MaxActiveThreadsPerGroup {
					victimTopicID = 0
					return nil
				}
				victimTopicID = OldestActiveTopicID(activeTopics)
				return nil
			}); err != nil {
				return err
			}
			if victimTopicID == 0 {
				break
			}

			if _, err := tm.ensureTopicSummaryFresh(tm.ctx, victimTopicID); err != nil {
				return err
			}

			archived := false
			err := tm.withGroupLock(groupID, func() error {
				activeTopics, err := tm.store.ListActiveTopicThreads(tm.ctx, groupID)
				if err != nil {
					return err
				}
				if len(activeTopics) <= MaxActiveThreadsPerGroup {
					return nil
				}
				if OldestActiveTopicID(activeTopics) != victimTopicID {
					return ErrStateChanged
				}
				if err := tm.store.ArchiveTopicThreadForRepair(tm.ctx, groupID, victimTopicID); err != nil {
					return err
				}
				archived = true
				return nil
			})
			if errors.Is(err, ErrStateChanged) {
				continue
			}
			if err != nil {
				return err
			}
			if !archived {
				break
			}
			if err := tm.syncTopicVectors(tm.ctx, victimTopicID); err != nil {
				zap.L().Warn("启动修复后的话题向量同步失败", zap.Int64("group_id", groupID), zap.Uint("topic_id", victimTopicID), zap.Error(err))
			}
		}

		var dirtyArchivedIDs []uint
		if err := tm.withGroupLock(groupID, func() error {
			activeTopics, err := tm.store.ListActiveTopicThreads(tm.ctx, groupID)
			if err != nil {
				return err
			}

			groupState := tm.ensureGroupState(groupID)
			groupState.topics = make(map[uint]*topicRuntimeState, len(activeTopics))
			topicIDs := make([]uint, 0, len(activeTopics))
			dirtyTopics := make([]memory.TopicThread, 0, len(activeTopics))
			for _, topic := range activeTopics {
				topicIDs = append(topicIDs, topic.ID)
				if topic.SummaryUntilMessageLogID < topic.LastMessageLogID {
					dirtyTopics = append(dirtyTopics, topic)
				}
			}
			tailLogsByTopic, err := tm.store.ListRecentTopicMessagesByTopicIDs(tm.ctx, topicIDs, TailKeepMessages)
			if err != nil {
				return err
			}
			pendingCounts := make(map[uint]int, len(dirtyTopics))
			if len(dirtyTopics) > 0 {
				pendingCounts, err = tm.store.CountTopicMessagesAfterSummaryByTopicIDs(tm.ctx, dirtyTopics)
				if err != nil {
					return err
				}
			}
			for _, topic := range activeTopics {
				state := &topicRuntimeState{
					thread:       topic,
					tail:         groupMessagesFromLogs(tailLogsByTopic[topic.ID]),
					participants: tm.loadTopicParticipants(tm.ctx, topic.ID),
					dirty:        topic.SummaryUntilMessageLogID < topic.LastMessageLogID,
					pendingCount: 0,
				}
				if state.dirty {
					state.pendingCount = pendingCounts[topic.ID]
				}
				groupState.topics[topic.ID] = state
			}

			dirtyArchived, err := tm.store.ListArchivedTopicThreadsNeedingSummary(tm.ctx, groupID)
			if err != nil {
				return err
			}
			dirtyArchivedIDs = make([]uint, 0, len(dirtyArchived))
			for _, topic := range dirtyArchived {
				dirtyArchivedIDs = append(dirtyArchivedIDs, topic.ID)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, topicID := range utils.UniqueIDs(dirtyArchivedIDs) {
			tm.enqueueSummaryTask(topicSummaryTask{groupID: groupID, topicID: topicID})
		}
	}
	return nil
}

func (tm *Manager) BuildPromptContext(ctx context.Context, groupID int64, buffer []*onebot.GroupMessage, topicQuery string) (PromptContext, error) {
	if len(buffer) == 0 {
		return PromptContext{}, nil
	}

	topicQuery = strings.TrimSpace(topicQuery)
	archivedHits := tm.searchArchivedTopicHitsByText(ctx, groupID, topicQuery)
	var selectedIDs []uint
	if err := tm.withGroupLock(groupID, func() error {
		var err error
		selectedIDs, err = tm.selectPromptTopicIDsLocked(ctx, groupID, buffer[len(buffer)-1], archivedHits, topicQuery)
		return err
	}); err != nil {
		return PromptContext{}, err
	}

	if len(selectedIDs) == 0 {
		return PromptContext{
			RetrievalQuery: topicQuery,
		}, nil
	}
	var promptCtx PromptContext
	err := tm.withGroupLock(groupID, func() error {
		var err error
		promptCtx, err = tm.renderPromptContextLocked(ctx, groupID, buffer, selectedIDs, topicQuery)
		for _, topicID := range selectedIDs {
			if state := tm.lookupTopicStateLocked(groupID, topicID); state != nil && state.dirty {
				tm.enqueueSummaryLocked(groupID, topicID, state)
			}
		}
		return err
	})
	return promptCtx, err
}

func (tm *Manager) PersistMessage(ctx context.Context, input PersistMessageInput) error {
	if input.Message == nil {
		return nil
	}

	msg := input.Message
	baseMessage := memory.MessageLog{
		MessageID:       fmt.Sprintf("%d", msg.MessageID),
		GroupID:         msg.GroupID,
		UserID:          msg.UserID,
		Nickname:        msg.Nickname,
		Content:         msg.FinalContent,
		OriginalContent: msg.Content,
		MsgType:         msg.MessageType,
		IsMentioned:     input.IsMentioned,
		CreatedAt:       msg.Time,
		Forwards:        input.ForwardsJSON,
	}
	saved, err := tm.store.CreateMessageLog(ctx, baseMessage)
	if err != nil {
		return err
	}
	if strings.TrimSpace(msg.Content) == "" {
		_, err := tm.store.ApplyTopicAssignmentBatch(ctx, AssignmentBatchInput{
			GroupID: msg.GroupID,
			Items: []AssignmentBatchItem{
				{
					MessageLogID: saved.ID,
					Action:       AssignmentActionNoTopic,
					MatchReason:  string(AssignmentActionNoTopic),
					MatchScore:   0,
				},
			},
		})
		return err
	}
	clonedValue := *msg
	if msg.Reply != nil {
		replyCopy := *msg.Reply
		clonedValue.Reply = &replyCopy
	}
	if len(msg.Images) > 0 {
		clonedValue.Images = append([]onebot.ImageInfo(nil), msg.Images...)
	}
	if len(msg.Videos) > 0 {
		clonedValue.Videos = append([]onebot.VideoInfo(nil), msg.Videos...)
	}
	if len(msg.Faces) > 0 {
		clonedValue.Faces = append([]onebot.FaceInfo(nil), msg.Faces...)
	}
	if len(msg.AtList) > 0 {
		clonedValue.AtList = append([]int64(nil), msg.AtList...)
	}
	if len(msg.Forwards) > 0 {
		clonedValue.Forwards = append([]onebot.ForwardMessage(nil), msg.Forwards...)
	}
	tm.enqueueAssignment(topicAssignJob{
		groupID:      msg.GroupID,
		messageLogID: saved.ID,
		message:      &clonedValue,
	})
	return nil
}

func (tm *Manager) syncTopicVectors(ctx context.Context, topicIDs ...uint) error {
	var syncErr error
	for _, topicID := range utils.UniqueIDs(topicIDs) {
		if topicID == 0 {
			continue
		}
		if err := tm.store.SyncTopicThreadVector(ctx, topicID); err != nil {
			if syncErr == nil {
				syncErr = err
			}
		}
	}
	return syncErr
}

func (tm *Manager) enqueueAssignment(job topicAssignJob) {
	if job.groupID == 0 || job.messageLogID == 0 {
		return
	}
	tm.markAssignmentPending(job.groupID, job.messageLogID)
	tm.assignMu.Lock()
	if tm.closed {
		tm.assignMu.Unlock()
		tm.clearAssignmentPending(job.groupID, []uint{job.messageLogID})
		return
	}
	queued := false
	select {
	case tm.assignQueue <- job:
		queued = true
	default:
	}
	tm.assignMu.Unlock()
	if !queued {
		if err := tm.markAssignmentBatchNoTopic(tm.ctx, job.groupID, []topicAssignJob{job}); err != nil {
			zap.L().Warn("话题分配队列已满，标记无话题失败", zap.Int64("group_id", job.groupID), zap.Uint("message_log_id", job.messageLogID), zap.Error(err))
			return
		}
		tm.clearAssignmentPending(job.groupID, []uint{job.messageLogID})
		zap.L().Warn("话题分配队列已满，消息已标记为无话题", zap.Int64("group_id", job.groupID), zap.Uint("message_log_id", job.messageLogID))
	}
}

func (tm *Manager) assignmentScheduler() {
	defer tm.wg.Done()
	defer close(tm.assignDone)
	for {
		select {
		case <-tm.stopCh:
			tm.drainAssignmentQueueIntoBuffers()
			return
		case job := <-tm.assignQueue:
			tm.assignMu.Lock()
			tm.assignBuffers[job.groupID] = append(tm.assignBuffers[job.groupID], job)
			ready := len(tm.assignBuffers[job.groupID]) >= tm.batchSize
			tm.assignMu.Unlock()
			if ready {
				tm.scheduleAssignmentFlush(job.groupID)
			}
		}
	}
}

func (tm *Manager) buildAssignmentCandidates(ctx context.Context, groupID int64, batch []topicAssignJob) []topicAssignmentCandidate {
	var queryParts []string
	for _, job := range batch {
		if text := messageTopicText(job.message); text != "" {
			queryParts = append(queryParts, text)
		}
	}
	archivedHits := tm.searchArchivedTopicHitsByText(ctx, groupID, strings.Join(queryParts, "\n"))
	candidates := make([]topicAssignmentCandidate, 0)
	seen := make(map[uint]struct{})

	_ = tm.withGroupLock(groupID, func() error {
		for _, topic := range tm.activeTopicsLocked(groupID) {
			if _, ok := seen[topic.ID]; ok {
				continue
			}
			candidates = append(candidates, tm.assignmentCandidateFromTopicLocked(topic, memory.TopicThreadStatusActive))
			seen[topic.ID] = struct{}{}
		}
		return nil
	})
	for _, hit := range archivedHits {
		if _, ok := seen[hit.Topic.ID]; ok {
			continue
		}
		candidate := tm.assignmentCandidateFromTopic(ctx, hit.Topic, memory.TopicThreadStatusArchived)
		candidate.Score = hit.Score
		candidates = append(candidates, candidate)
		seen[hit.Topic.ID] = struct{}{}
	}
	return candidates
}

func (tm *Manager) assignmentCandidateFromTopicLocked(topic memory.TopicThread, status memory.TopicThreadStatus) topicAssignmentCandidate {
	state := tm.lookupTopicStateLocked(topic.GroupID, topic.ID)
	candidate := topicAssignmentCandidate{
		ID:            topic.ID,
		Status:        status,
		Summary:       renderTopicSummaryForAssignment(topic),
		LastMessageID: topic.LastMessageLogID,
	}
	if state != nil {
		candidate.Tail = renderTopicTailLines(state.tail, TailKeepMessages)
		candidate.Participants = participantNames(state.participants)
	}
	return candidate
}

func (tm *Manager) assignmentCandidateFromTopic(ctx context.Context, topic memory.TopicThread, status memory.TopicThreadStatus) topicAssignmentCandidate {
	candidate := topicAssignmentCandidate{
		ID:            topic.ID,
		Status:        status,
		Summary:       renderTopicSummaryForAssignment(topic),
		LastMessageID: topic.LastMessageLogID,
	}
	candidate.Tail = renderTopicTailLines(tm.loadTopicTailMessages(ctx, topic.ID), TailKeepMessages)
	candidate.Participants = participantNames(tm.loadTopicParticipants(ctx, topic.ID))
	return candidate
}

func (tm *Manager) assignmentItemsFromDecisions(groupID int64, batch []topicAssignJob, decisions []topicAssignmentDecision, candidates []topicAssignmentCandidate) []AssignmentBatchItem {
	decisionByKey := make(map[string]topicAssignmentDecision, len(decisions))
	for _, decision := range decisions {
		decision.MessageKey = strings.TrimSpace(decision.MessageKey)
		if decision.MessageKey == "" {
			continue
		}
		decisionByKey[decision.MessageKey] = decision
	}
	candidateIDs := make(map[uint]memory.TopicThreadStatus, len(candidates))
	for _, candidate := range candidates {
		candidateIDs[candidate.ID] = candidate.Status
	}
	items := make([]AssignmentBatchItem, 0, len(batch))
	for _, job := range batch {
		messageKey := assignmentMessageKey(job)
		decision, ok := decisionByKey[messageKey]
		if !ok {
			zap.L().Warn("话题分配结果缺少消息项，改为无话题",
				zap.Int64("group_id", groupID),
				zap.Uint("message_log_id", job.messageLogID),
				zap.String("message_key", messageKey))
			items = append(items, noTopicAssignmentItem(job.messageLogID))
			continue
		}
		rawAction := strings.ToLower(strings.TrimSpace(decision.Action))
		if rawAction != string(AssignmentActionNoTopic) && rawAction != string(AssignmentActionReuse) && rawAction != string(AssignmentActionNew) {
			zap.L().Warn("话题分配结果动作无效，改为无话题",
				zap.Int64("group_id", groupID),
				zap.Uint("message_log_id", job.messageLogID),
				zap.String("message_key", messageKey),
				zap.String("action", decision.Action))
			items = append(items, noTopicAssignmentItem(job.messageLogID))
			continue
		}
		item := AssignmentBatchItem{
			MessageLogID: job.messageLogID,
			Action:       AssignmentAction(rawAction),
			TopicID:      decision.TopicID,
			NewTopicKey:  strings.TrimSpace(decision.NewTopicKey),
			MatchReason:  assignmentMatchReason(AssignmentAction(rawAction), decision.Reason),
			MatchScore:   clamp01(decision.Confidence),
		}
		switch item.Action {
		case AssignmentActionReuse:
			if status := candidateIDs[item.TopicID]; status != memory.TopicThreadStatusActive && status != memory.TopicThreadStatusArchived {
				zap.L().Warn("话题分配结果引用了无效候选话题，改为无话题",
					zap.Int64("group_id", groupID),
					zap.Uint("message_log_id", job.messageLogID),
					zap.String("message_key", messageKey),
					zap.Uint("topic_id", item.TopicID))
				items = append(items, noTopicAssignmentItem(job.messageLogID))
				continue
			}
		case AssignmentActionNew:
			if item.NewTopicKey == "" {
				zap.L().Warn("话题分配结果缺少新话题编号，改为无话题",
					zap.Int64("group_id", groupID),
					zap.Uint("message_log_id", job.messageLogID),
					zap.String("message_key", messageKey))
				items = append(items, noTopicAssignmentItem(job.messageLogID))
				continue
			}
		case AssignmentActionNoTopic:
			if strings.TrimSpace(item.MatchReason) == "" {
				item.MatchReason = string(AssignmentActionNoTopic)
			}
		}
		items = append(items, item)
	}
	return items
}

func noTopicAssignmentItem(messageLogID uint) AssignmentBatchItem {
	return AssignmentBatchItem{
		MessageLogID: messageLogID,
		Action:       AssignmentActionNoTopic,
		MatchReason:  string(AssignmentActionNoTopic),
		MatchScore:   0,
	}
}

func (tm *Manager) scheduleAssignmentFlush(groupID int64) {
	tm.assignMu.Lock()
	if tm.closed {
		tm.assignMu.Unlock()
		return
	}
	if _, running := tm.assignInFlight[groupID]; running {
		tm.assignMu.Unlock()
		return
	}
	if len(tm.assignBuffers[groupID]) < tm.batchSize {
		tm.assignMu.Unlock()
		return
	}
	batch := append([]topicAssignJob(nil), tm.assignBuffers[groupID][:tm.batchSize]...)
	tm.assignBuffers[groupID] = tm.assignBuffers[groupID][tm.batchSize:]
	tm.assignInFlight[groupID] = struct{}{}
	tm.assignMu.Unlock()

	tm.wg.Add(1)
	go tm.runAssignmentFlush(groupID, batch)
}

func (tm *Manager) runAssignmentFlush(groupID int64, batch []topicAssignJob) {
	defer tm.wg.Done()
	select {
	case tm.assignSem <- struct{}{}:
		defer func() { <-tm.assignSem }()
	case <-tm.stopCh:
		tm.clearAssignmentPending(groupID, assignmentJobMessageIDs(batch))
		tm.finishAssignmentFlush(groupID, nil)
		return
	}

	ctx, cancel := tm.assignmentFlushContext(topicAssignTimeout)
	defer cancel()
	processedMessageIDs, err := tm.flushAssignmentBatch(ctx, groupID, batch)
	if err != nil {
		zap.L().Warn("批量话题分配写入失败，保留待处理状态", zap.Int64("group_id", groupID), zap.Int("count", len(batch)), zap.Error(err))
		tm.finishAssignmentFlush(groupID, batch)
		return
	}
	tm.clearAssignmentPending(groupID, processedMessageIDs)
	tm.finishAssignmentFlush(groupID, unprocessedAssignmentJobs(batch, processedMessageIDs))
}

func (tm *Manager) assignmentFlushContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	base := tm.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, timeout)
	go func() {
		select {
		case <-tm.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (tm *Manager) finishAssignmentFlush(groupID int64, batch []topicAssignJob) {
	tm.assignMu.Lock()
	if len(batch) > 0 && !tm.closed {
		tm.assignBuffers[groupID] = append(batch, tm.assignBuffers[groupID]...)
	}
	delete(tm.assignInFlight, groupID)
	ready := !tm.closed && len(batch) == 0 && len(tm.assignBuffers[groupID]) >= tm.batchSize
	tm.assignMu.Unlock()
	if ready {
		tm.scheduleAssignmentFlush(groupID)
	}
}

func (tm *Manager) drainAssignmentQueueIntoBuffers() {
	for {
		select {
		case job := <-tm.assignQueue:
			tm.assignMu.Lock()
			tm.assignBuffers[job.groupID] = append(tm.assignBuffers[job.groupID], job)
			tm.assignMu.Unlock()
		default:
			return
		}
	}
}

func (tm *Manager) drainAssignmentBuffers() {
	tm.drainAssignmentQueueIntoBuffers()
	tm.clearAssignmentBuffers()
}

func (tm *Manager) clearAssignmentBuffers() {
	pendingByGroup := make(map[int64][]uint)
	tm.assignMu.Lock()
	for groupID, batch := range tm.assignBuffers {
		for _, job := range batch {
			pendingByGroup[groupID] = append(pendingByGroup[groupID], job.messageLogID)
		}
		tm.assignBuffers[groupID] = nil
	}
	tm.assignMu.Unlock()

	for groupID, ids := range pendingByGroup {
		tm.clearAssignmentPending(groupID, ids)
	}
}

func (tm *Manager) markAssignmentBatchNoTopic(ctx context.Context, groupID int64, batch []topicAssignJob) error {
	items := make([]AssignmentBatchItem, 0, len(batch))
	for _, job := range batch {
		if job.messageLogID == 0 {
			continue
		}
		items = append(items, noTopicAssignmentItem(job.messageLogID))
	}
	if len(items) == 0 {
		return nil
	}
	_, err := tm.store.ApplyTopicAssignmentBatch(ctx, AssignmentBatchInput{
		GroupID: groupID,
		Items:   items,
	})
	return err
}

func (tm *Manager) flushAssignmentBatch(ctx context.Context, groupID int64, batch []topicAssignJob) ([]uint, error) {
	if len(batch) == 0 {
		return nil, nil
	}
	sort.SliceStable(batch, func(i, j int) bool {
		return batch[i].messageLogID < batch[j].messageLogID
	})
	candidates := tm.buildAssignmentCandidates(ctx, groupID, batch)
	decisions, err := tm.assignFn(ctx, groupID, batch, candidates)
	if err != nil {
		if markErr := tm.markAssignmentBatchNoTopic(ctx, groupID, batch); markErr != nil {
			return nil, markErr
		}
		return assignmentJobMessageIDs(batch), nil
	}
	items := tm.assignmentItemsFromDecisions(groupID, batch, decisions, candidates)
	result, err := tm.store.ApplyTopicAssignmentBatch(ctx, AssignmentBatchInput{
		GroupID: groupID,
		Items:   items,
	})
	if err != nil {
		return nil, err
	}
	for _, topicID := range result.ArchivedTopicIDs {
		if err := tm.syncTopicVectors(ctx, topicID); err != nil {
			zap.L().Warn("归档话题向量同步失败", zap.Int64("group_id", groupID), zap.Uint("topic_id", topicID), zap.Error(err))
		}
		tm.enqueueSummaryTask(topicSummaryTask{groupID: groupID, topicID: topicID})
	}
	for _, topicID := range result.UpdatedTopicIDs {
		if err := tm.refreshTopicState(ctx, topicID); err != nil {
			zap.L().Warn("批量分配后同步话题运行态失败", zap.Int64("group_id", groupID), zap.Uint("topic_id", topicID), zap.Error(err))
			continue
		}
		if err := tm.syncTopicVectors(ctx, topicID); err != nil {
			zap.L().Warn("话题向量同步失败", zap.Int64("group_id", groupID), zap.Uint("topic_id", topicID), zap.Error(err))
		}
	}
	processedMessageIDs := append([]uint(nil), result.MessageLogIDs...)
	processedMessageIDs = append(processedMessageIDs, result.NoTopicMessageIDs...)
	return processedMessageIDs, nil
}

func assignmentJobMessageIDs(batch []topicAssignJob) []uint {
	messageIDs := make([]uint, 0, len(batch))
	for _, job := range batch {
		messageIDs = append(messageIDs, job.messageLogID)
	}
	return messageIDs
}

func unprocessedAssignmentJobs(batch []topicAssignJob, processedMessageIDs []uint) []topicAssignJob {
	if len(batch) == 0 || len(processedMessageIDs) == len(batch) {
		return nil
	}
	processed := make(map[uint]struct{}, len(processedMessageIDs))
	for _, id := range processedMessageIDs {
		processed[id] = struct{}{}
	}
	unprocessed := make([]topicAssignJob, 0, len(batch)-len(processedMessageIDs))
	for _, job := range batch {
		if _, ok := processed[job.messageLogID]; ok {
			continue
		}
		unprocessed = append(unprocessed, job)
	}
	return unprocessed
}

func (tm *Manager) RecoverPendingAssignments(groupIDs []int64) error {
	for _, groupID := range groupIDs {
		if groupID == 0 {
			continue
		}
		pending, err := tm.store.ListPendingTopicAssignmentMessages(tm.ctx, groupID, 0)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			continue
		}

		batch := make([]topicAssignJob, 0, len(pending))
		noTopicItems := make([]AssignmentBatchItem, 0)
		for _, log := range pending {
			if log.ID == 0 {
				continue
			}
			if strings.TrimSpace(log.OriginalContent) == "" {
				noTopicItems = append(noTopicItems, noTopicAssignmentItem(log.ID))
				continue
			}
			batch = append(batch, topicAssignJob{
				groupID:      groupID,
				messageLogID: log.ID,
				message:      messageLogToGroupMessage(log),
			})
		}
		if len(noTopicItems) > 0 {
			result, err := tm.store.ApplyTopicAssignmentBatch(tm.ctx, AssignmentBatchInput{
				GroupID: groupID,
				Items:   noTopicItems,
			})
			if err != nil {
				return err
			}
			tm.clearAssignmentPending(groupID, result.NoTopicMessageIDs)
		}
		if len(batch) > 0 {
			if processedMessageIDs, err := tm.flushAssignmentBatch(tm.ctx, groupID, batch); err != nil {
				return err
			} else {
				tm.clearAssignmentPending(groupID, processedMessageIDs)
			}
		}
	}
	return nil
}

func (tm *Manager) selectPromptTopicIDsLocked(ctx context.Context, groupID int64, latest *onebot.GroupMessage, archivedHits []ThreadSearchHit, topicQuery string) ([]uint, error) {
	candidates, err := tm.collectCandidatesLocked(ctx, groupID, latest, archivedHits, topicQuery)
	if err != nil {
		return nil, err
	}
	sorted := sortTopicCandidates(candidates)

	selectedIDs := make([]uint, 0, len(sorted))
	for _, candidate := range sorted {
		score := scoreTopicCandidate(candidate)
		if !candidate.ReplyMatched && score < 0.58 {
			continue
		}
		selectedIDs = append(selectedIDs, candidate.TopicID)
	}
	return utils.UniqueIDs(selectedIDs), nil
}

func (tm *Manager) renderPromptContextLocked(ctx context.Context, groupID int64, buffer []*onebot.GroupMessage, selectedIDs []uint, retrievalQuery string) (PromptContext, error) {
	if len(buffer) == 0 {
		return PromptContext{}, nil
	}

	var builder strings.Builder
	var injected []uint
	for _, topicID := range utils.UniqueIDs(selectedIDs) {
		state := tm.lookupTopicStateLocked(groupID, topicID)
		var topic *memory.TopicThread
		if state != nil {
			topicCopy := state.thread
			topic = &topicCopy
		} else {
			var err error
			topic, err = tm.store.GetTopicThread(ctx, topicID)
			if err != nil {
				return PromptContext{}, err
			}
		}
		section := renderTopicPromptSection(topic, state)
		if section == "" {
			continue
		}
		if builder.Len() > 0 && len([]rune(builder.String()))+len([]rune(section)) > 2200 {
			break
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(section)
		injected = append(injected, topicID)
	}

	mainTopicID := uint(0)
	if len(injected) > 0 {
		mainTopicID = injected[0]
	}

	return PromptContext{
		Prompt:         builder.String(),
		RetrievalQuery: retrievalQuery,
		TopicIDs:       injected,
		MainTopicID:    mainTopicID,
	}, nil
}

func (tm *Manager) ensureTopicSummaryFresh(ctx context.Context, topicID uint) (*memory.TopicThread, error) {
	call, leader := tm.beginTopicSummaryCall(topicID)
	if !leader {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-call.done:
			return copyTopicThread(call.topic), call.err
		}
	}

	topic, err := tm.ensureTopicSummaryFreshOnce(ctx, topicID)
	tm.finishTopicSummaryCall(topicID, call, topic, err)
	return copyTopicThread(topic), err
}

func (tm *Manager) ensureTopicSummaryFreshOnce(ctx context.Context, topicID uint) (*memory.TopicThread, error) {
	topic, err := tm.store.GetTopicThread(ctx, topicID)
	if err != nil {
		return nil, err
	}
	if topic.SummaryUntilMessageLogID >= topic.LastMessageLogID {
		return copyTopicThread(topic), nil
	}

	current := *topic
	for current.SummaryUntilMessageLogID < current.LastMessageLogID {
		pendingLogs, err := tm.store.GetTopicMessagesAfterSummary(ctx, topicID, 100)
		if err != nil {
			return nil, err
		}
		if len(pendingLogs) == 0 {
			break
		}

		oldSummary := ParseSummary(current.SummaryJSON)
		summaryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		summary, err := tm.summaryFn(summaryCtx, oldSummary, pendingLogs)
		cancel()
		if err != nil {
			return nil, err
		}
		summary.ItemMeta = MergeSummaryItemMeta(oldSummary, summary)
		summaryUntil := pendingLogs[len(pendingLogs)-1].ID
		if err := tm.store.UpdateTopicSummary(ctx, topicID, summary, summaryUntil, time.Now()); err != nil {
			return nil, err
		}

		refreshed, err := tm.store.GetTopicThread(ctx, topicID)
		if err != nil {
			return nil, err
		}
		current = *refreshed
	}
	if current.SummaryUntilMessageLogID < current.LastMessageLogID {
		return nil, fmt.Errorf("topic %d summary still stale after refresh: %d < %d", topicID, current.SummaryUntilMessageLogID, current.LastMessageLogID)
	}

	return copyTopicThread(&current), nil
}

func (tm *Manager) beginTopicSummaryCall(topicID uint) (*topicSummaryCall, bool) {
	tm.summaryMu.Lock()
	defer tm.summaryMu.Unlock()

	if call, ok := tm.summaryInFlight[topicID]; ok {
		return call, false
	}

	call := &topicSummaryCall{done: make(chan struct{})}
	tm.summaryInFlight[topicID] = call
	return call, true
}

func (tm *Manager) finishTopicSummaryCall(topicID uint, call *topicSummaryCall, topic *memory.TopicThread, err error) {
	tm.summaryMu.Lock()
	defer tm.summaryMu.Unlock()

	call.topic = copyTopicThread(topic)
	call.err = err
	close(call.done)
	delete(tm.summaryInFlight, topicID)
}

func (tm *Manager) summaryWorker() {
	defer tm.wg.Done()
	for {
		select {
		case <-tm.stopCh:
			return
		case task := <-tm.summaryQueue:
			topic, err := tm.store.GetTopicThread(tm.ctx, task.topicID)
			if err == nil && topic != nil && topic.LastMessageLogID > topic.SummaryUntilMessageLogID {
				if tm.withGroupLock(task.groupID, func() error {
					if tm.hasPendingAssignmentInRangeLocked(task.groupID, topic.SummaryUntilMessageLogID, topic.LastMessageLogID) {
						return ErrStateChanged
					}
					return nil
				}) == ErrStateChanged {
					timer := time.NewTimer(500 * time.Millisecond)
					select {
					case <-tm.stopCh:
						if !timer.Stop() {
							<-timer.C
						}
						return
					case <-timer.C:
						tm.enqueueSummaryTask(task)
					}
					continue
				}
			}
			topic, err = tm.ensureTopicSummaryFresh(tm.ctx, task.topicID)
			if err != nil {
				zap.L().Warn("刷新话题摘要失败", zap.Int64("group_id", task.groupID), zap.Uint("topic_id", task.topicID), zap.Error(err))
				_ = tm.withGroupLock(task.groupID, func() error {
					if state := tm.lookupTopicStateLocked(task.groupID, task.topicID); state != nil {
						state.queued = false
					}
					return nil
				})
				continue
			}
			if topic != nil {
				if err := tm.refreshTopicState(tm.ctx, topic.ID); err != nil {
					zap.L().Warn("同步话题运行态失败", zap.Int64("group_id", task.groupID), zap.Uint("topic_id", task.topicID), zap.Error(err))
				}
			}
		}
	}
}

func (tm *Manager) syncTopicStateLocked(ctx context.Context, groupID int64, topic memory.TopicThread) {
	state := tm.ensureTopicStateLocked(groupID, topic.ID)
	state.thread = topic
	state.dirty = topic.SummaryUntilMessageLogID < topic.LastMessageLogID
	state.queued = false
	if state.dirty {
		if remainingCount, err := tm.store.CountTopicMessagesAfterSummary(ctx, topic.ID); err == nil {
			state.pendingCount = remainingCount
		}
	} else {
		state.pendingCount = 0
	}
	if topic.Status == memory.TopicThreadStatusActive {
		state.tail = tm.loadTopicTailMessages(ctx, topic.ID)
		state.participants = tm.loadTopicParticipants(ctx, topic.ID)
		return
	}
	groupState := tm.ensureGroupState(groupID)
	delete(groupState.topics, topic.ID)
}

func (tm *Manager) markAssignmentPending(groupID int64, messageLogID uint) {
	if messageLogID == 0 {
		return
	}
	tm.statesMu.Lock()
	defer tm.statesMu.Unlock()
	groupState := tm.groupStates[groupID]
	if groupState == nil {
		groupState = newTopicGroupState()
		tm.groupStates[groupID] = groupState
	}
	groupState.pendingAssign[messageLogID] = struct{}{}
}

func (tm *Manager) clearAssignmentPending(groupID int64, messageLogIDs []uint) {
	if len(messageLogIDs) == 0 {
		return
	}
	tm.statesMu.Lock()
	defer tm.statesMu.Unlock()
	groupState := tm.groupStates[groupID]
	if groupState == nil {
		return
	}
	for _, id := range messageLogIDs {
		delete(groupState.pendingAssign, id)
	}
}

func (tm *Manager) hasPendingAssignmentInRangeLocked(groupID int64, afterID uint, untilID uint) bool {
	if untilID <= afterID {
		return false
	}
	tm.statesMu.RLock()
	groupState := tm.groupStates[groupID]
	if groupState == nil {
		tm.statesMu.RUnlock()
		return false
	}
	defer tm.statesMu.RUnlock()
	for id := range groupState.pendingAssign {
		if id > afterID && id <= untilID {
			return true
		}
	}
	return false
}

func (tm *Manager) collectCandidatesLocked(ctx context.Context, groupID int64, msg *onebot.GroupMessage, archivedHits []ThreadSearchHit, topicQuery string) ([]topicCandidate, error) {
	query := messageTopicText(msg)
	archiveQuery := strings.TrimSpace(topicQuery)
	replyTopicID := tm.resolveReplyTopicID(msg)
	candidates := make([]topicCandidate, 0)
	seen := make(map[uint]struct{})

	for _, topic := range tm.activeTopicsLocked(groupID) {
		state := tm.lookupTopicStateLocked(groupID, topic.ID)
		candidate := topicCandidate{
			TopicID:            topic.ID,
			Status:             topic.Status,
			ReplyMatched:       replyTopicID != 0 && replyTopicID == topic.ID,
			SemanticScore:      max(topicSemanticSimilarity(query, topic, state), 0.15),
			ParticipantOverlap: topicParticipantOverlap(msg, state),
			KeywordContinuity:  topicKeywordContinuity(query, topic),
			LastMessageLogID:   topic.LastMessageLogID,
		}
		candidates = append(candidates, candidate)
		seen[topic.ID] = struct{}{}
	}

	if archiveQuery != "" {
		for _, hit := range archivedHits {
			if _, ok := seen[hit.Topic.ID]; ok {
				continue
			}
			archivedState := &topicRuntimeState{
				thread:       hit.Topic,
				participants: tm.loadTopicParticipants(ctx, hit.Topic.ID),
			}
			candidates = append(candidates, topicCandidate{
				TopicID:            hit.Topic.ID,
				Status:             hit.Topic.Status,
				ReplyMatched:       replyTopicID != 0 && replyTopicID == hit.Topic.ID,
				SemanticScore:      max(hit.Score, topicKeywordContinuity(archiveQuery, hit.Topic)),
				ParticipantOverlap: topicParticipantOverlap(msg, archivedState),
				KeywordContinuity:  topicKeywordContinuity(archiveQuery, hit.Topic),
				LastMessageLogID:   hit.Topic.LastMessageLogID,
			})
			seen[hit.Topic.ID] = struct{}{}
		}
	}

	if replyTopicID != 0 {
		if _, ok := seen[replyTopicID]; !ok {
			topic, err := tm.store.GetTopicThread(ctx, replyTopicID)
			if err == nil && topic.GroupID == groupID {
				replyState := tm.lookupTopicStateLocked(groupID, topic.ID)
				if replyState == nil {
					replyState = &topicRuntimeState{
						thread:       *topic,
						participants: tm.loadTopicParticipants(ctx, topic.ID),
					}
				}
				candidates = append(candidates, topicCandidate{
					TopicID:            topic.ID,
					Status:             topic.Status,
					ReplyMatched:       true,
					SemanticScore:      1,
					ParticipantOverlap: topicParticipantOverlap(msg, replyState),
					KeywordContinuity:  topicKeywordContinuity(query, *topic),
					LastMessageLogID:   topic.LastMessageLogID,
				})
			}
		}
	}

	return candidates, nil
}

func (tm *Manager) searchArchivedTopicHitsByText(ctx context.Context, groupID int64, query string) []ThreadSearchHit {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	hits, err := tm.store.SearchArchivedTopicThreadHits(ctx, query, groupID, 6, 0.45)
	if err != nil {
		zap.L().Warn("检索归档话题失败", zap.Int64("group_id", groupID), zap.String("query", query), zap.Error(err))
		return nil
	}
	return hits
}

func (tm *Manager) resolveReplyTopicID(msg *onebot.GroupMessage) uint {
	if msg == nil || msg.Reply == nil || msg.Reply.MessageID == 0 {
		return 0
	}
	log, err := tm.store.GetMessageLogByID(strconv.FormatInt(msg.Reply.MessageID, 10))
	if err != nil {
		return 0
	}
	return log.TopicThreadID
}

func (tm *Manager) enqueueSummaryLocked(groupID int64, topicID uint, state *topicRuntimeState) {
	if state == nil || state.queued {
		return
	}
	state.queued = true
	tm.enqueueSummaryTask(topicSummaryTask{groupID: groupID, topicID: topicID})
}

func (tm *Manager) enqueueSummaryTask(task topicSummaryTask) {
	select {
	case tm.summaryQueue <- task:
		return
	default:
		zap.L().Warn("话题摘要队列已满，等待空位重试", zap.Int64("group_id", task.groupID), zap.Uint("topic_id", task.topicID))
		go func() {
			select {
			case <-tm.stopCh:
				return
			case tm.summaryQueue <- task:
			}
		}()
	}
}

func (tm *Manager) activeTopicsLocked(groupID int64) []memory.TopicThread {
	groupState := tm.ensureGroupState(groupID)
	topics := make([]memory.TopicThread, 0, len(groupState.topics))
	for _, state := range groupState.topics {
		if state.thread.Status == memory.TopicThreadStatusActive {
			topics = append(topics, state.thread)
		}
	}
	sort.Slice(topics, func(i, j int) bool {
		if topics[i].LastMessageLogID != topics[j].LastMessageLogID {
			return topics[i].LastMessageLogID > topics[j].LastMessageLogID
		}
		return topics[i].ID > topics[j].ID
	})
	return topics
}

func (tm *Manager) loadTopicTailMessages(ctx context.Context, topicID uint) []*onebot.GroupMessage {
	logs, err := tm.store.ListRecentTopicMessages(ctx, topicID, TailKeepMessages)
	if err != nil {
		return nil
	}
	return groupMessagesFromLogs(logs)
}

func (tm *Manager) loadTopicParticipants(ctx context.Context, topicID uint) []memory.TopicParticipantRef {
	participants, err := tm.store.ListRecentTopicParticipants(ctx, topicID, TailKeepMessages)
	if err != nil {
		return nil
	}
	return participants
}

func groupMessagesFromLogs(logs []memory.MessageLog) []*onebot.GroupMessage {
	msgs := make([]*onebot.GroupMessage, 0, len(logs))
	for _, log := range logs {
		msgs = append(msgs, messageLogToGroupMessage(log))
	}
	return msgs
}

func (tm *Manager) refreshTopicState(ctx context.Context, topicID uint) error {
	topic, err := tm.store.GetTopicThread(ctx, topicID)
	if err != nil {
		return err
	}
	return tm.withGroupLock(topic.GroupID, func() error {
		_, err := tm.reloadTopicStateLocked(ctx, topic.GroupID, topic)
		return err
	})
}

func (tm *Manager) reloadTopicStateLocked(ctx context.Context, groupID int64, topic *memory.TopicThread) (*memory.TopicThread, error) {
	if topic == nil {
		return nil, fmt.Errorf("topic is nil")
	}
	tm.syncTopicStateLocked(ctx, groupID, *topic)
	return copyTopicThread(topic), nil
}

func (tm *Manager) ensureGroupState(groupID int64) *topicGroupState {
	tm.statesMu.Lock()
	defer tm.statesMu.Unlock()
	groupState, ok := tm.groupStates[groupID]
	if !ok {
		groupState = newTopicGroupState()
		tm.groupStates[groupID] = groupState
	}
	return groupState
}

func newTopicGroupState() *topicGroupState {
	return &topicGroupState{
		topics:        make(map[uint]*topicRuntimeState),
		pendingAssign: make(map[uint]struct{}),
	}
}

func (tm *Manager) lookupTopicStateLocked(groupID int64, topicID uint) *topicRuntimeState {
	tm.statesMu.RLock()
	groupState := tm.groupStates[groupID]
	tm.statesMu.RUnlock()
	if groupState == nil {
		return nil
	}
	return groupState.topics[topicID]
}

func (tm *Manager) ensureTopicStateLocked(groupID int64, topicID uint) *topicRuntimeState {
	groupState := tm.ensureGroupState(groupID)
	if state, ok := groupState.topics[topicID]; ok {
		return state
	}

	state := &topicRuntimeState{
		thread: memory.TopicThread{
			ID:      topicID,
			GroupID: groupID,
			Status:  memory.TopicThreadStatusActive,
		},
	}
	groupState.topics[topicID] = state
	return state
}

func (tm *Manager) getGroupLock(groupID int64) *sync.Mutex {
	tm.locksMu.Lock()
	defer tm.locksMu.Unlock()
	lock, ok := tm.groupLocks[groupID]
	if !ok {
		lock = &sync.Mutex{}
		tm.groupLocks[groupID] = lock
	}
	return lock
}

func (tm *Manager) generateSummary(ctx context.Context, oldSummary memory.TopicSummary, newMessages []memory.MessageLog) (memory.TopicSummary, error) {
	if tm.summaryModel == nil {
		return memory.TopicSummary{}, fmt.Errorf("topic summary extractor not configured")
	}

	var msgLines []string
	for _, log := range newMessages {
		text := strings.TrimSpace(log.OriginalContent)
		if text == "" {
			continue
		}
		msgLines = append(msgLines, text)
	}

	oldSummaryJSON, err := MarshalSummary(oldSummary)
	if err != nil {
		return memory.TopicSummary{}, err
	}
	oldItemMetaJSON, err := MarshalSummaryItemMetaForPrompt(oldSummary)
	if err != nil {
		return memory.TopicSummary{}, err
	}
	summaryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	prompt := fmt.Sprintf("请根据旧摘要和新增消息输出最新的话题摘要。\n字段固定为 title,gist,facts,participants,open_loops,recent_turns,keywords。\nfacts 只写已经确认且对后续有用的稳定事实；open_loops 只写尚未解决、后续可能要接上的事项；recent_turns 保留近期推进，不复述全部聊天。\nparticipants 中每项包含 nickname 和 position。\n旧摘要里的 item_meta 只供你理解旧条目身份：如果新消息没有实质改变同一条事实或事项，尽量沿用旧 facts/open_loops 的原句，不要把同一事实改写成并列新条目；如果需要更新同一条事实，替换旧条目，不要同时保留新旧两条。\n旧摘要：%s\n旧条目元数据：%s\n新增消息：\n%s", oldSummaryJSON, oldItemMetaJSON, strings.Join(msgLines, "\n"))
	target, err := llm.GenerateStructuredJSONObject[topicSummarySubmission](summaryCtx, tm.summaryModel, prompt)
	if err != nil {
		return memory.TopicSummary{}, err
	}

	return normalizeTopicSummarySubmission(&target), nil
}

func messageLogToGroupMessage(log memory.MessageLog) *onebot.GroupMessage {
	msg := onebot.MessageLogToGroupMessage(log)
	msg.Content = strings.TrimSpace(log.OriginalContent)
	msg.FinalContent = strings.TrimSpace(log.Content)
	return msg
}

func (tm *Manager) generateTopicAssignments(ctx context.Context, groupID int64, messages []topicAssignJob, candidates []topicAssignmentCandidate) ([]topicAssignmentDecision, error) {
	if tm.assignModel == nil {
		return nil, fmt.Errorf("topic assignment extractor not configured")
	}
	assignCtx, cancel := context.WithTimeout(ctx, topicAssignTimeout)
	defer cancel()

	prompt := buildTopicAssignmentPrompt(groupID, messages, candidates)
	target, err := llm.GenerateStructuredJSONObject[topicAssignmentSubmission](assignCtx, tm.assignModel, prompt)
	if err != nil {
		return nil, err
	}
	if len(target.Assignments) == 0 {
		return nil, fmt.Errorf("topic assignment returned no assignments")
	}
	return normalizeTopicAssignmentSubmission(&target), nil
}

func assignmentMessageKey(job topicAssignJob) string {
	if job.messageLogID != 0 {
		return fmt.Sprintf("m%d", job.messageLogID)
	}
	if job.message != nil && job.message.MessageID != 0 {
		return fmt.Sprintf("qq%d", job.message.MessageID)
	}
	return ""
}

func assignmentMatchReason(action AssignmentAction, rawReason string) string {
	reason := strings.TrimSpace(rawReason)
	if reason == "" {
		if action == AssignmentActionNoTopic {
			return string(AssignmentActionNoTopic)
		}
		return "llm_batch_" + string(action)
	}
	return reason
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func messageTopicText(msg *onebot.GroupMessage) string {
	if msg == nil {
		return ""
	}
	return strings.TrimSpace(msg.Content)
}

func copyTopicThread(topic *memory.TopicThread) *memory.TopicThread {
	if topic == nil {
		return nil
	}
	copied := *topic
	return &copied
}

func actualMessageBufferCapacity() int {
	cfg := config.Get()
	if cfg == nil || cfg.Agent.MessageBufferSize <= 0 {
		return 15
	}
	return cfg.Agent.MessageBufferSize
}

func topicAssignmentBatchSize(bufferCapacity int) int {
	if bufferCapacity <= 0 {
		bufferCapacity = actualMessageBufferCapacity()
	}
	if bufferCapacity < 1 {
		return 1
	}
	return bufferCapacity
}
