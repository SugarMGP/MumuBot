package agent

import (
	"context"
	"errors"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/cloudwego/eino/components/model"
	agentreact "github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

const (
	topicPromptTailLines    = 4
	topicAssignQueueSize    = 256
	topicAssignDrainTimeout = 3 * time.Second
	topicAssignTimeout      = 30 * time.Second
	maxTopicAssignWorkers   = 2
)

type topicMemoryStore interface {
	ListActiveTopicThreads(ctx context.Context, groupID int64) ([]memory.TopicThread, error)
	ListArchivedTopicThreadsNeedingSummary(ctx context.Context, groupID int64) ([]memory.TopicThread, error)
	ListRecentTopicMessages(ctx context.Context, topicID uint, limit int) ([]memory.MessageLog, error)
	ListRecentTopicParticipants(ctx context.Context, topicID uint, limit int) ([]memory.TopicParticipantRef, error)
	CountTopicMessagesAfterSummary(ctx context.Context, topicID uint) (int, error)
	GetTopicMessagesAfterSummary(ctx context.Context, topicID uint, limit int) ([]memory.MessageLog, error)
	UpdateTopicSummary(ctx context.Context, topicID uint, summary memory.TopicSummaryV1, summaryUntil uint, capturedAt time.Time) error
	GetTopicThread(ctx context.Context, topicID uint) (*memory.TopicThread, error)
	SearchArchivedTopicThreadHits(ctx context.Context, query string, groupID int64, topK int, threshold float64) ([]memory.TopicThreadSearchHit, error)
	ArchiveTopicThreadForRepair(ctx context.Context, groupID int64, topicID uint) error
	SyncTopicThreadVector(ctx context.Context, topicID uint) error
	GetMessageLogByID(messageID string) (*memory.MessageLog, error)
	CreateMessageLog(ctx context.Context, msg memory.MessageLog) (*memory.MessageLog, error)
	ListPendingTopicAssignmentMessages(ctx context.Context, groupID int64, limit int) ([]memory.MessageLog, error)
	ApplyTopicAssignmentBatch(ctx context.Context, input memory.TopicAssignmentBatchInput) (memory.TopicAssignmentBatchResult, error)
}

type (
	topicSummaryFunc func(ctx context.Context, oldSummary memory.TopicSummaryV1, newMessages []memory.MessageLog) (memory.TopicSummaryV1, error)
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

type TopicPromptContext struct {
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

type TopicManager struct {
	ctx              context.Context
	store            topicMemoryStore
	assignExtractor  *agentreact.Agent
	summaryExtractor *agentreact.Agent
	summaryFn        topicSummaryFunc
	assignFn         topicAssignFunc
	groupStates      map[int64]*topicGroupState
	groupLocks       map[int64]*sync.Mutex

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

func NewTopicManager(ctx context.Context, store topicMemoryStore, chatModel model.ToolCallingChatModel, bufferCapacity ...int) *TopicManager {
	if ctx == nil {
		ctx = context.Background()
	}
	capacity := actualMessageBufferCapacity()
	if len(bufferCapacity) > 0 {
		capacity = bufferCapacity[0]
	}
	tm := &TopicManager{
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
	if summaryExtractor, err := newTopicSummaryExtractor(chatModel); err != nil {
		zap.L().Warn("初始化话题摘要提取器失败", zap.Error(err))
	} else {
		tm.summaryExtractor = summaryExtractor
	}
	if assignExtractor, err := newTopicAssignmentExtractor(chatModel); err != nil {
		zap.L().Warn("初始化话题分配器失败", zap.Error(err))
	} else {
		tm.assignExtractor = assignExtractor
	}
	tm.summaryFn = tm.generateSummary
	tm.assignFn = tm.generateTopicAssignments
	tm.wg.Add(2)
	go tm.summaryWorker()
	go tm.assignmentScheduler()
	return tm
}

func (tm *TopicManager) Close() {
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

func (tm *TopicManager) WithGroupLock(groupID int64, fn func() error) error {
	lock := tm.getGroupLock(groupID)
	lock.Lock()
	defer lock.Unlock()
	return fn()
}

func (tm *TopicManager) LoadFromDB(groupIDs []int64) error {
	for _, groupID := range groupIDs {
		if groupID == 0 {
			continue
		}

		for {
			var victimTopicID uint
			if err := tm.WithGroupLock(groupID, func() error {
				activeTopics, err := tm.store.ListActiveTopicThreads(tm.ctx, groupID)
				if err != nil {
					return err
				}
				if len(activeTopics) <= memory.MaxActiveTopicThreadsPerGroup {
					victimTopicID = 0
					return nil
				}
				victimTopicID = memory.OldestActiveTopicID(activeTopics)
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
			err := tm.WithGroupLock(groupID, func() error {
				activeTopics, err := tm.store.ListActiveTopicThreads(tm.ctx, groupID)
				if err != nil {
					return err
				}
				if len(activeTopics) <= memory.MaxActiveTopicThreadsPerGroup {
					return nil
				}
				if memory.OldestActiveTopicID(activeTopics) != victimTopicID {
					return memory.ErrTopicStateChanged
				}
				if err := tm.store.ArchiveTopicThreadForRepair(tm.ctx, groupID, victimTopicID); err != nil {
					return err
				}
				archived = true
				return nil
			})
			if errors.Is(err, memory.ErrTopicStateChanged) {
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
		if err := tm.WithGroupLock(groupID, func() error {
			activeTopics, err := tm.store.ListActiveTopicThreads(tm.ctx, groupID)
			if err != nil {
				return err
			}

			groupState := tm.ensureGroupState(groupID)
			groupState.topics = make(map[uint]*topicRuntimeState, len(activeTopics))
			for _, topic := range activeTopics {
				state := &topicRuntimeState{
					thread:       topic,
					tail:         tm.loadTopicTailMessages(tm.ctx, topic.ID),
					participants: tm.loadTopicParticipants(tm.ctx, topic.ID),
					dirty:        topic.SummaryUntilMessageLogID < topic.LastMessageLogID,
					pendingCount: 0,
				}
				if state.dirty {
					if count, err := tm.store.CountTopicMessagesAfterSummary(tm.ctx, topic.ID); err == nil {
						state.pendingCount = count
					}
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
		for _, topicID := range uniqueTopicIDs(dirtyArchivedIDs) {
			tm.enqueueSummaryTask(topicSummaryTask{groupID: groupID, topicID: topicID})
		}
	}
	return nil
}

func (tm *TopicManager) BuildPromptContext(ctx context.Context, groupID int64, buffer []*onebot.GroupMessage, topicQuery string) (TopicPromptContext, error) {
	if len(buffer) == 0 {
		return TopicPromptContext{}, nil
	}

	topicQuery = strings.TrimSpace(topicQuery)
	archivedHits := tm.searchArchivedTopicHitsByText(ctx, groupID, topicQuery)
	var selectedIDs []uint
	if err := tm.WithGroupLock(groupID, func() error {
		var err error
		selectedIDs, err = tm.selectPromptTopicIDsLocked(ctx, groupID, buffer[len(buffer)-1], archivedHits, topicQuery)
		return err
	}); err != nil {
		return TopicPromptContext{}, err
	}

	if len(selectedIDs) == 0 {
		return TopicPromptContext{
			RetrievalQuery: topicQuery,
		}, nil
	}
	var promptCtx TopicPromptContext
	err := tm.WithGroupLock(groupID, func() error {
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

func (tm *TopicManager) PersistMessage(ctx context.Context, input PersistMessageInput) error {
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
	tm.enqueueAssignment(topicAssignJob{
		groupID:      msg.GroupID,
		messageLogID: saved.ID,
		message:      cloneGroupMessage(msg),
	})
	return nil
}

func (tm *TopicManager) syncTopicVectors(ctx context.Context, topicIDs ...uint) error {
	var syncErr error
	for _, topicID := range uniqueTopicIDs(topicIDs) {
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

func (tm *TopicManager) enqueueAssignment(job topicAssignJob) {
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
		tm.clearAssignmentPending(job.groupID, []uint{job.messageLogID})
		zap.L().Warn("话题分配队列已满，消息暂不归属话题", zap.Int64("group_id", job.groupID), zap.Uint("message_log_id", job.messageLogID))
	}
}

func (tm *TopicManager) assignmentScheduler() {
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

func (tm *TopicManager) buildAssignmentCandidates(ctx context.Context, groupID int64, batch []topicAssignJob) []topicAssignmentCandidate {
	var queryParts []string
	for _, job := range batch {
		if text := messageTopicText(job.message); text != "" {
			queryParts = append(queryParts, text)
		}
	}
	archivedHits := tm.searchArchivedTopicHitsByText(ctx, groupID, strings.Join(queryParts, "\n"))
	candidates := make([]topicAssignmentCandidate, 0)
	seen := make(map[uint]struct{})

	_ = tm.WithGroupLock(groupID, func() error {
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

func (tm *TopicManager) assignmentCandidateFromTopicLocked(topic memory.TopicThread, status memory.TopicThreadStatus) topicAssignmentCandidate {
	state := tm.lookupTopicStateLocked(topic.GroupID, topic.ID)
	candidate := topicAssignmentCandidate{
		ID:            topic.ID,
		Status:        status,
		Summary:       renderTopicSummaryForAssignment(topic),
		LastMessageID: topic.LastMessageLogID,
	}
	if state != nil {
		candidate.Tail = renderTopicTailLines(state.tail, memory.TopicTailKeepMessages)
		candidate.Participants = participantNames(state.participants)
	}
	return candidate
}

func (tm *TopicManager) assignmentCandidateFromTopic(ctx context.Context, topic memory.TopicThread, status memory.TopicThreadStatus) topicAssignmentCandidate {
	candidate := topicAssignmentCandidate{
		ID:            topic.ID,
		Status:        status,
		Summary:       renderTopicSummaryForAssignment(topic),
		LastMessageID: topic.LastMessageLogID,
	}
	candidate.Tail = renderTopicTailLines(tm.loadTopicTailMessages(ctx, topic.ID), memory.TopicTailKeepMessages)
	candidate.Participants = participantNames(tm.loadTopicParticipants(ctx, topic.ID))
	return candidate
}

func (tm *TopicManager) assignmentItemsFromDecisions(batch []topicAssignJob, decisions []topicAssignmentDecision, candidates []topicAssignmentCandidate) []memory.TopicAssignmentBatchItem {
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
	items := make([]memory.TopicAssignmentBatchItem, 0, len(batch))
	for _, job := range batch {
		decision, ok := decisionByKey[assignmentMessageKey(job)]
		if !ok {
			continue
		}
		item := memory.TopicAssignmentBatchItem{
			MessageLogID: job.messageLogID,
			Action:       normalizeAssignmentAction(decision.Action),
			TopicID:      decision.TopicID,
			NewTopicKey:  decision.NewTopicKey,
			MatchReason:  assignmentMatchReason(decision),
			MatchScore:   clamp01(decision.Confidence),
		}
		switch item.Action {
		case memory.TopicAssignmentActionReuse:
			if status := candidateIDs[item.TopicID]; status != memory.TopicThreadStatusActive && status != memory.TopicThreadStatusArchived {
				item.Action = memory.TopicAssignmentActionNoTopic
				item.TopicID = 0
				item.MatchReason = string(memory.TopicAssignmentActionNoTopic)
			}
		case memory.TopicAssignmentActionNew:
			if strings.TrimSpace(item.NewTopicKey) == "" {
				item.NewTopicKey = assignmentMessageKey(job)
			}
		case memory.TopicAssignmentActionNoTopic:
			if strings.TrimSpace(item.MatchReason) == "" {
				item.MatchReason = string(memory.TopicAssignmentActionNoTopic)
			}
		default:
			item.Action = memory.TopicAssignmentActionNoTopic
			item.MatchReason = string(memory.TopicAssignmentActionNoTopic)
		}
		items = append(items, item)
	}
	return items
}

func (tm *TopicManager) scheduleAssignmentFlush(groupID int64) {
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

func (tm *TopicManager) runAssignmentFlush(groupID int64, batch []topicAssignJob) {
	defer tm.wg.Done()
	select {
	case tm.assignSem <- struct{}{}:
		defer func() { <-tm.assignSem }()
	case <-tm.stopCh:
		tm.finishAssignmentFlush(groupID, batch)
		return
	}

	ctx, cancel := tm.assignmentFlushContext(topicAssignTimeout)
	defer cancel()
	if err := tm.flushAssignmentBatch(ctx, groupID, batch); err != nil {
		zap.L().Warn("批量话题分配失败，消息暂不归属话题", zap.Int64("group_id", groupID), zap.Int("count", len(batch)), zap.Error(err))
	}
	tm.finishAssignmentFlush(groupID, batch)
}

func (tm *TopicManager) assignmentFlushContext(timeout time.Duration) (context.Context, context.CancelFunc) {
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

func (tm *TopicManager) finishAssignmentFlush(groupID int64, batch []topicAssignJob) {
	messageIDs := make([]uint, 0, len(batch))
	for _, job := range batch {
		messageIDs = append(messageIDs, job.messageLogID)
	}
	tm.clearAssignmentPending(groupID, messageIDs)
	tm.assignMu.Lock()
	delete(tm.assignInFlight, groupID)
	ready := !tm.closed && len(tm.assignBuffers[groupID]) >= tm.batchSize
	tm.assignMu.Unlock()
	if ready {
		tm.scheduleAssignmentFlush(groupID)
	}
}

func (tm *TopicManager) drainAssignmentQueueIntoBuffers() {
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

func (tm *TopicManager) drainAssignmentBuffers() {
	tm.drainAssignmentQueueIntoBuffers()
	tm.clearAssignmentBuffers()
}

func (tm *TopicManager) clearAssignmentBuffers() {
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

func (tm *TopicManager) flushAssignmentBatch(ctx context.Context, groupID int64, batch []topicAssignJob) error {
	if len(batch) == 0 {
		return nil
	}
	sort.SliceStable(batch, func(i, j int) bool {
		return batch[i].messageLogID < batch[j].messageLogID
	})
	candidates := tm.buildAssignmentCandidates(ctx, groupID, batch)
	decisions, err := tm.assignFn(ctx, groupID, batch, candidates)
	if err != nil {
		return err
	}
	items := tm.assignmentItemsFromDecisions(batch, decisions, candidates)
	result, err := tm.store.ApplyTopicAssignmentBatch(ctx, memory.TopicAssignmentBatchInput{
		GroupID: groupID,
		Items:   items,
	})
	if err != nil {
		return err
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
	return nil
}

func (tm *TopicManager) recoverPendingAssignments(groupIDs []int64) error {
	for _, groupID := range groupIDs {
		if groupID == 0 {
			continue
		}
		for {
			pending, err := tm.store.ListPendingTopicAssignmentMessages(tm.ctx, groupID, tm.batchSize)
			if err != nil {
				return err
			}
			if len(pending) == 0 {
				break
			}

			batch := make([]topicAssignJob, 0, len(pending))
			for _, log := range pending {
				if log.ID == 0 {
					continue
				}
				batch = append(batch, topicAssignJob{
					groupID:      groupID,
					messageLogID: log.ID,
					message:      messageLogToGroupMessage(log),
				})
			}
			if len(batch) == 0 {
				break
			}
			if err := tm.flushAssignmentBatch(tm.ctx, groupID, batch); err != nil {
				return err
			}
		}
	}
	return nil
}

func (tm *TopicManager) selectPromptTopicIDsLocked(ctx context.Context, groupID int64, latest *onebot.GroupMessage, archivedHits []memory.TopicThreadSearchHit, topicQuery string) ([]uint, error) {
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
	return uniqueTopicIDs(selectedIDs), nil
}

func (tm *TopicManager) renderPromptContextLocked(ctx context.Context, groupID int64, buffer []*onebot.GroupMessage, selectedIDs []uint, retrievalQuery string) (TopicPromptContext, error) {
	if len(buffer) == 0 {
		return TopicPromptContext{}, nil
	}

	var builder strings.Builder
	var injected []uint
	for _, topicID := range uniqueTopicIDs(selectedIDs) {
		state := tm.lookupTopicStateLocked(groupID, topicID)
		var topic *memory.TopicThread
		if state != nil {
			topicCopy := state.thread
			topic = &topicCopy
		} else {
			var err error
			topic, err = tm.store.GetTopicThread(ctx, topicID)
			if err != nil {
				return TopicPromptContext{}, err
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

	return TopicPromptContext{
		Prompt:         builder.String(),
		RetrievalQuery: retrievalQuery,
		TopicIDs:       injected,
		MainTopicID:    mainTopicID,
	}, nil
}

func (tm *TopicManager) ensureTopicSummaryFresh(ctx context.Context, topicID uint) (*memory.TopicThread, error) {
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

func (tm *TopicManager) ensureTopicSummaryFreshOnce(ctx context.Context, topicID uint) (*memory.TopicThread, error) {
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

		oldSummary := memory.ParseTopicSummary(current.SummaryJSON)
		summaryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		summary, err := tm.summaryFn(summaryCtx, oldSummary, pendingLogs)
		cancel()
		if err != nil {
			return nil, err
		}
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

func (tm *TopicManager) beginTopicSummaryCall(topicID uint) (*topicSummaryCall, bool) {
	tm.summaryMu.Lock()
	defer tm.summaryMu.Unlock()

	if call, ok := tm.summaryInFlight[topicID]; ok {
		return call, false
	}

	call := &topicSummaryCall{done: make(chan struct{})}
	tm.summaryInFlight[topicID] = call
	return call, true
}

func (tm *TopicManager) finishTopicSummaryCall(topicID uint, call *topicSummaryCall, topic *memory.TopicThread, err error) {
	tm.summaryMu.Lock()
	defer tm.summaryMu.Unlock()

	call.topic = copyTopicThread(topic)
	call.err = err
	close(call.done)
	delete(tm.summaryInFlight, topicID)
}

func (tm *TopicManager) summaryWorker() {
	defer tm.wg.Done()
	for {
		select {
		case <-tm.stopCh:
			return
		case task := <-tm.summaryQueue:
			topic, err := tm.store.GetTopicThread(tm.ctx, task.topicID)
			if err == nil && topic != nil && topic.LastMessageLogID > topic.SummaryUntilMessageLogID {
				if tm.WithGroupLock(task.groupID, func() error {
					if tm.hasPendingAssignmentInRangeLocked(task.groupID, topic.SummaryUntilMessageLogID, topic.LastMessageLogID) {
						return memory.ErrTopicStateChanged
					}
					return nil
				}) == memory.ErrTopicStateChanged {
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
				_ = tm.WithGroupLock(task.groupID, func() error {
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

func (tm *TopicManager) syncTopicStateLocked(ctx context.Context, groupID int64, topic memory.TopicThread) {
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

func (tm *TopicManager) markAssignmentPending(groupID int64, messageLogID uint) {
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

func (tm *TopicManager) clearAssignmentPending(groupID int64, messageLogIDs []uint) {
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

func (tm *TopicManager) hasPendingAssignmentInRangeLocked(groupID int64, afterID uint, untilID uint) bool {
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

func (tm *TopicManager) collectCandidatesLocked(ctx context.Context, groupID int64, msg *onebot.GroupMessage, archivedHits []memory.TopicThreadSearchHit, topicQuery string) ([]topicCandidate, error) {
	query := messageTopicText(msg)
	archiveQuery := strings.TrimSpace(topicQuery)
	replyTopicID := tm.resolveReplyTopicID(ctx, msg)
	candidates := make([]topicCandidate, 0)
	seen := make(map[uint]struct{})

	for _, topic := range tm.activeTopicsLocked(groupID) {
		state := tm.lookupTopicStateLocked(groupID, topic.ID)
		candidate := topicCandidate{
			TopicID:            topic.ID,
			Status:             topic.Status,
			ReplyMatched:       replyTopicID != 0 && replyTopicID == topic.ID,
			SemanticScore:      max(topicSemanticSimilarity(query, topic, state), 0.15),
			ParticipantOverlap: topicParticipantOverlap(msg, topic, state),
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
				ParticipantOverlap: topicParticipantOverlap(msg, hit.Topic, archivedState),
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
					ParticipantOverlap: topicParticipantOverlap(msg, *topic, replyState),
					KeywordContinuity:  topicKeywordContinuity(query, *topic),
					LastMessageLogID:   topic.LastMessageLogID,
				})
			}
		}
	}

	return candidates, nil
}

func (tm *TopicManager) searchArchivedTopicHitsByText(ctx context.Context, groupID int64, query string) []memory.TopicThreadSearchHit {
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

func (tm *TopicManager) resolveReplyTopicID(ctx context.Context, msg *onebot.GroupMessage) uint {
	if msg == nil || msg.Reply == nil || msg.Reply.MessageID == 0 {
		return 0
	}
	log, err := tm.store.GetMessageLogByID(strconv.FormatInt(msg.Reply.MessageID, 10))
	if err != nil {
		return 0
	}
	return log.TopicThreadID
}

func (tm *TopicManager) enqueueSummaryLocked(groupID int64, topicID uint, state *topicRuntimeState) {
	if state == nil || state.queued {
		return
	}
	state.queued = true
	tm.enqueueSummaryTask(topicSummaryTask{groupID: groupID, topicID: topicID})
}

func (tm *TopicManager) enqueueSummaryTask(task topicSummaryTask) {
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

func (tm *TopicManager) activeTopicsLocked(groupID int64) []memory.TopicThread {
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

func (tm *TopicManager) loadTopicTailMessages(ctx context.Context, topicID uint) []*onebot.GroupMessage {
	logs, err := tm.store.ListRecentTopicMessages(ctx, topicID, memory.TopicTailKeepMessages)
	if err != nil {
		return nil
	}
	msgs := make([]*onebot.GroupMessage, 0, len(logs))
	for _, log := range logs {
		msgs = append(msgs, messageLogToGroupMessage(log))
	}
	return msgs
}

func (tm *TopicManager) loadTopicParticipants(ctx context.Context, topicID uint) []memory.TopicParticipantRef {
	participants, err := tm.store.ListRecentTopicParticipants(ctx, topicID, memory.TopicTailKeepMessages)
	if err != nil {
		return nil
	}
	return participants
}

func (tm *TopicManager) refreshTopicState(ctx context.Context, topicID uint) error {
	topic, err := tm.store.GetTopicThread(ctx, topicID)
	if err != nil {
		return err
	}
	return tm.WithGroupLock(topic.GroupID, func() error {
		_, err := tm.reloadTopicStateLocked(ctx, topic.GroupID, topicID)
		return err
	})
}

func (tm *TopicManager) reloadTopicStateLocked(ctx context.Context, groupID int64, topicID uint) (*memory.TopicThread, error) {
	topic, err := tm.store.GetTopicThread(ctx, topicID)
	if err != nil {
		return nil, err
	}
	tm.syncTopicStateLocked(ctx, groupID, *topic)
	return copyTopicThread(topic), nil
}

func (tm *TopicManager) ensureGroupState(groupID int64) *topicGroupState {
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

func (tm *TopicManager) lookupTopicStateLocked(groupID int64, topicID uint) *topicRuntimeState {
	tm.statesMu.RLock()
	groupState := tm.groupStates[groupID]
	tm.statesMu.RUnlock()
	if groupState == nil {
		return nil
	}
	return groupState.topics[topicID]
}

func (tm *TopicManager) ensureTopicStateLocked(groupID int64, topicID uint) *topicRuntimeState {
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

func (tm *TopicManager) getGroupLock(groupID int64) *sync.Mutex {
	tm.locksMu.Lock()
	defer tm.locksMu.Unlock()
	lock, ok := tm.groupLocks[groupID]
	if !ok {
		lock = &sync.Mutex{}
		tm.groupLocks[groupID] = lock
	}
	return lock
}

func (tm *TopicManager) generateSummary(ctx context.Context, oldSummary memory.TopicSummaryV1, newMessages []memory.MessageLog) (memory.TopicSummaryV1, error) {
	if tm.summaryExtractor == nil {
		return memory.TopicSummaryV1{}, fmt.Errorf("topic summary extractor not configured")
	}

	var msgLines []string
	for _, log := range newMessages {
		text := messageLogTopicText(log)
		if text == "" {
			continue
		}
		msgLines = append(msgLines, text)
	}

	oldSummaryJSON, err := memory.MarshalTopicSummary(oldSummary)
	if err != nil {
		return memory.TopicSummaryV1{}, err
	}
	target := &topicSummarySubmission{}
	summaryCtx, cancel := context.WithTimeout(withTopicSummaryTarget(ctx, target), 30*time.Second)
	defer cancel()

	prompt := fmt.Sprintf("请根据旧摘要和新增消息，调用一次 %s 工具提交最新的话题摘要，不要输出普通文本。\n字段固定为 title,gist,facts,participants,open_loops,recent_turns,keywords。\nfacts 只写已经确认且对后续有用的稳定事实；open_loops 只写尚未解决、后续可能要接上的事项；recent_turns 保留近期推进，不复述全部聊天。\nparticipants 中每项包含 nickname 和 position。\n旧摘要：%s\n新增消息：\n%s", topicSummaryToolName, oldSummaryJSON, strings.Join(msgLines, "\n"))
	_, err = tm.summaryExtractor.Generate(summaryCtx, []*schema.Message{
		schema.SystemMessage("你负责维护群聊当前话题的结构化摘要。你必须调用工具提交结果，不要输出普通文本。"),
		schema.UserMessage(prompt),
	}, buildTopicSummaryOptions()...)
	if err != nil {
		return memory.TopicSummaryV1{}, err
	}

	return normalizeTopicSummarySubmission(target), nil
}

func messageLogToGroupMessage(log memory.MessageLog) *onebot.GroupMessage {
	msg := messageLogBaseGroupMessage(log)
	msg.Content = messageLogTopicText(log)
	msg.FinalContent = strings.TrimSpace(log.Content)
	return msg
}

func (tm *TopicManager) generateTopicAssignments(ctx context.Context, groupID int64, messages []topicAssignJob, candidates []topicAssignmentCandidate) ([]topicAssignmentDecision, error) {
	if tm.assignExtractor == nil {
		return nil, fmt.Errorf("topic assignment extractor not configured")
	}
	target := &topicAssignmentSubmission{}
	assignCtx, cancel := context.WithTimeout(withTopicAssignmentTarget(ctx, target), topicAssignTimeout)
	defer cancel()

	prompt := buildTopicAssignmentPrompt(groupID, messages, candidates)
	_, err := tm.assignExtractor.Generate(assignCtx, []*schema.Message{
		schema.SystemMessage("你负责把群聊消息批量归入话题。你必须调用工具提交结果，不要输出普通文本。"),
		schema.UserMessage(prompt),
	}, buildTopicAssignmentOptions()...)
	if err != nil {
		return nil, err
	}
	if len(target.Assignments) == 0 {
		return nil, fmt.Errorf("topic assignment tool returned no assignments")
	}
	return normalizeTopicAssignmentSubmission(target), nil
}

func cloneGroupMessage(msg *onebot.GroupMessage) *onebot.GroupMessage {
	if msg == nil {
		return nil
	}
	cloned := *msg
	if msg.Reply != nil {
		replyCopy := *msg.Reply
		cloned.Reply = &replyCopy
	}
	if len(msg.Images) > 0 {
		cloned.Images = append([]onebot.ImageInfo(nil), msg.Images...)
	}
	if len(msg.Videos) > 0 {
		cloned.Videos = append([]onebot.VideoInfo(nil), msg.Videos...)
	}
	if len(msg.Faces) > 0 {
		cloned.Faces = append([]onebot.FaceInfo(nil), msg.Faces...)
	}
	if len(msg.AtList) > 0 {
		cloned.AtList = append([]int64(nil), msg.AtList...)
	}
	if len(msg.Forwards) > 0 {
		cloned.Forwards = append([]onebot.ForwardMessage(nil), msg.Forwards...)
	}
	return &cloned
}

func renderTopicPromptSection(topic *memory.TopicThread, state *topicRuntimeState) string {
	if topic == nil {
		return ""
	}
	summary := memory.ParseTopicSummary(topic.SummaryJSON)
	title := strings.TrimSpace(summary.Title)
	if title == "" {
		title = fmt.Sprintf("话题 %d", topic.ID)
	}

	var lines []string
	lines = append(lines, "### "+title)
	if gist := strings.TrimSpace(summary.Gist); gist != "" {
		lines = append(lines, "概况："+gist)
	}
	if len(summary.Facts) > 0 {
		lines = append(lines, "已确认："+strings.Join(summary.Facts, "；"))
	}
	if len(summary.Participants) > 0 {
		parts := make([]string, 0, len(summary.Participants))
		for _, participant := range summary.Participants {
			if participant.Nickname == "" || participant.Position == "" {
				continue
			}
			parts = append(parts, participant.Nickname+"："+participant.Position)
		}
		if len(parts) > 0 {
			lines = append(lines, "各方立场："+strings.Join(parts, "；"))
		}
	}
	if len(summary.OpenLoops) > 0 {
		lines = append(lines, "未完事项："+strings.Join(summary.OpenLoops, "；"))
	}
	if len(summary.RecentTurns) > 0 {
		lines = append(lines, "最近摘要："+strings.Join(summary.RecentTurns, "；"))
	}
	if topic.Status == memory.TopicThreadStatusActive && state != nil {
		if tail := renderTopicTailLines(state.tail, topicPromptTailLines); tail != "" {
			lines = append(lines, "关键原文：\n"+tail)
		}
	}
	return strings.Join(lines, "\n")
}

func renderTopicTailLines(tail []*onebot.GroupMessage, limit int) string {
	if limit <= 0 || len(tail) == 0 {
		return ""
	}
	if len(tail) > limit {
		tail = tail[len(tail)-limit:]
	}
	lines := make([]string, 0, len(tail))
	for _, msg := range tail {
		text := messageTopicText(msg)
		if text == "" {
			continue
		}
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n")
}

func renderTopicSummaryForAssignment(topic memory.TopicThread) string {
	summary := memory.ParseTopicSummary(topic.SummaryJSON)
	parts := []string{strings.TrimSpace(summary.Title), strings.TrimSpace(summary.Gist)}
	if len(summary.Facts) > 0 {
		parts = append(parts, strings.Join(summary.Facts, "；"))
	}
	if len(summary.OpenLoops) > 0 {
		parts = append(parts, "未完事项："+strings.Join(summary.OpenLoops, "；"))
	}
	if len(summary.Keywords) > 0 {
		parts = append(parts, "关键词："+strings.Join(summary.Keywords, "，"))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func participantNames(participants []memory.TopicParticipantRef) []string {
	names := make([]string, 0, len(participants))
	seen := make(map[string]struct{}, len(participants))
	for _, participant := range participants {
		name := strings.TrimSpace(participant.Nickname)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
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

func normalizeAssignmentAction(action string) memory.TopicAssignmentAction {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case string(memory.TopicAssignmentActionReuse):
		return memory.TopicAssignmentActionReuse
	case string(memory.TopicAssignmentActionNew):
		return memory.TopicAssignmentActionNew
	default:
		return memory.TopicAssignmentActionNoTopic
	}
}

func assignmentMatchReason(decision topicAssignmentDecision) string {
	action := normalizeAssignmentAction(decision.Action)
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		if action == memory.TopicAssignmentActionNoTopic {
			return string(memory.TopicAssignmentActionNoTopic)
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

func messageLogTopicText(log memory.MessageLog) string {
	return strings.TrimSpace(log.OriginalContent)
}

func topicSemanticSimilarity(query string, topic memory.TopicThread, state *topicRuntimeState) float64 {
	summary := memory.ParseTopicSummary(topic.SummaryJSON)
	summaryText := strings.TrimSpace(summary.Title + "\n" + summary.Gist + "\n" + strings.Join(summary.Facts, "\n"))
	tailText := ""
	if state != nil {
		tailText = renderTopicTailLines(state.tail, memory.TopicTailKeepMessages)
	}
	return max(textSimilarity(query, summaryText), textSimilarity(query, tailText))
}

func topicParticipantOverlap(msg *onebot.GroupMessage, topic memory.TopicThread, state *topicRuntimeState) float64 {
	if msg == nil {
		return 0
	}
	if state != nil {
		for _, participant := range state.participants {
			if participant.UserID != 0 && participant.UserID == msg.UserID {
				return 1
			}
			if participant.UserID == 0 && participant.Nickname != "" && participant.Nickname == msg.Nickname {
				return 1
			}
		}
	}
	return 0
}

func copyTopicThread(topic *memory.TopicThread) *memory.TopicThread {
	if topic == nil {
		return nil
	}
	copied := *topic
	return &copied
}

func topicKeywordContinuity(query string, topic memory.TopicThread) float64 {
	summary := memory.ParseTopicSummary(topic.SummaryJSON)
	if len(summary.Keywords) == 0 || strings.TrimSpace(query) == "" {
		return 0
	}
	matched := 0
	for _, keyword := range summary.Keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		if strings.Contains(query, keyword) {
			matched++
		}
	}
	if matched == 0 {
		return 0
	}
	return float64(matched) / float64(len(summary.Keywords))
}

func textSimilarity(left, right string) float64 {
	left = normalizeTopicText(left)
	right = normalizeTopicText(right)
	if left == "" || right == "" {
		return 0
	}
	if strings.Contains(right, left) || strings.Contains(left, right) {
		return 0.95
	}

	leftSet := runeBigramSet(left)
	rightSet := runeBigramSet(right)
	if len(leftSet) == 0 || len(rightSet) == 0 {
		return 0
	}

	intersection := 0
	for gram := range leftSet {
		if _, ok := rightSet[gram]; ok {
			intersection++
		}
	}
	if intersection == 0 {
		return 0
	}
	union := len(leftSet) + len(rightSet) - intersection
	return float64(intersection) / float64(union)
}

func normalizeTopicText(text string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		if unicode.IsSpace(r) {
			continue
		}
		if unicode.IsPunct(r) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func runeBigramSet(text string) map[string]struct{} {
	runes := []rune(text)
	if len(runes) < 2 {
		return map[string]struct{}{text: {}}
	}
	result := make(map[string]struct{}, len(runes)-1)
	for i := 0; i < len(runes)-1; i++ {
		result[string(runes[i:i+2])] = struct{}{}
	}
	return result
}

func uniqueTopicIDs(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
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
	if bufferCapacity < 10 {
		if bufferCapacity < 1 {
			return 1
		}
		return bufferCapacity
	}
	size := bufferCapacity / 2
	if size < 10 {
		size = 10
	}
	if size > 50 {
		size = 50
	}
	return size
}
