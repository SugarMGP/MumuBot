package agent

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"mumu-bot/internal/config"
)

// 门控权重来自 MaiBot 的离线拟合，作为上线起点，之后用本项目日志重新拟合
const (
	gateIntercept         = -1.2005
	gateMentionWeight     = 1.5852
	gateAtOtherWeight     = -1.1157
	gateQuestionWeight    = 0.4807
	gateMediaOnlyWeight   = -1.0281
	gateSelfRatioWeight   = 2.6525
	gateMessageNumWeight  = -0.0367
	gateIdleWeight        = 0.0571
	gatePendingWeight     = 0.1012
	gateForcedExpectation = 0.91
	gateWindowSize        = time.Hour
	gateRecentWindow      = 5 * time.Minute
	gateMinSamples        = 20
)

// gateStaticThresholds 样本不足时的离线阈值表，按保留比例插值
var gateStaticThresholds = []struct {
	keep float64
	rate float64
}{
	{0.0, 1.0}, {0.1, 0.605}, {0.2, 0.482}, {0.3, 0.430}, {0.4, 0.376},
	{0.5, 0.336}, {0.6, 0.313}, {0.7, 0.290}, {0.8, 0.250}, {0.9, 0.210}, {1.0, 0.180},
}

// gateBatch 窗口内的一批待处理消息
type gateBatch struct {
	at          time.Time
	probability float64
	forced      bool
}

// gateWindow 单个群的门控窗口
type gateState struct {
	mu      sync.Mutex
	batches []gateBatch
	replies []time.Time
}

// gateStore 管理各群的门控窗口
type gateStore struct {
	mu      sync.Mutex
	windows map[int64]*gateState
}

func newGateStore() *gateStore {
	return &gateStore{windows: map[int64]*gateState{}}
}

// window 取群的门控窗口
func (s *gateStore) window(groupID int64) *gateState {
	s.mu.Lock()
	defer s.mu.Unlock()
	window, ok := s.windows[groupID]
	if !ok {
		window = &gateState{}
		s.windows[groupID] = window
	}
	return window
}

// markReply 记录一次真实发言
func (s *gateStore) markReply(groupID int64) {
	window := s.window(groupID)
	window.mu.Lock()
	defer window.mu.Unlock()
	window.replies = append(window.replies, time.Now())
}

// record 记录一批待处理消息的意愿分
func (w *gateState) record(at time.Time, probability float64, forced bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.batches = append(w.batches, gateBatch{at: at, probability: probability, forced: forced})
	w.prune(at)
}

// prune 清理窗口外的数据
func (w *gateState) prune(now time.Time) {
	before := now.Add(-gateWindowSize)
	kept := w.batches[:0]
	for _, batch := range w.batches {
		if batch.at.After(before) {
			kept = append(kept, batch)
		}
	}
	w.batches = kept
	replies := w.replies[:0]
	for _, at := range w.replies {
		if at.After(before) {
			replies = append(replies, at)
		}
	}
	w.replies = replies
}

// threshold 按目标频率算出本轮的放行阈值
func (w *gateState) threshold(now time.Time, frequency float64) float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prune(now)

	expectedProactive := 0.0
	expectedForced := 0.0
	var scores []float64
	for _, batch := range w.batches {
		if batch.forced {
			expectedForced += gateForcedExpectation
			continue
		}
		expectedProactive += batch.probability
		scores = append(scores, batch.probability)
	}
	if expectedProactive <= 0 {
		return 0
	}
	target := frequency * (expectedProactive + expectedForced)
	keep := (target - expectedForced) / expectedProactive
	keep = clampFloat(keep, 0, 1)
	// 实际发言多于目标时收紧保留比例，只收紧不放宽
	if excess := float64(len(w.replies)) - target; excess > 0 && target > 0 {
		keep *= clampFloat(1-excess/target, 0, 1)
	}
	if len(scores) < gateMinSamples {
		return staticGateThreshold(keep)
	}
	sort.Float64s(scores)
	budget := keep * expectedProactive
	accumulated := 0.0
	for i := len(scores) - 1; i >= 0; i-- {
		accumulated += scores[i]
		if accumulated >= budget {
			return scores[i]
		}
	}
	return 0
}

// staticGateThreshold 样本不足时用离线阈值表插值
func staticGateThreshold(keep float64) float64 {
	if keep <= gateStaticThresholds[0].keep {
		return gateStaticThresholds[0].rate
	}
	for i := 1; i < len(gateStaticThresholds); i++ {
		if keep <= gateStaticThresholds[i].keep {
			prev, cur := gateStaticThresholds[i-1], gateStaticThresholds[i]
			span := cur.keep - prev.keep
			if span <= 0 {
				return cur.rate
			}
			return prev.rate + (cur.rate-prev.rate)*(keep-prev.keep)/span
		}
	}
	return gateStaticThresholds[len(gateStaticThresholds)-1].rate
}

// clampFloat 限制数值区间
func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// gateAllow 判断这一批消息是否值得跑一轮思考
func (a *Agent) gateAllow(groupID int64, session *Session, pending []*sessionUser, forced bool) bool {
	if len(pending) == 0 {
		return false
	}
	now := time.Now()
	probability := a.gateProbability(session, pending)
	if forced {
		a.gates.window(groupID).record(now, 1, true)
		return true
	}
	window := a.gates.window(groupID)
	threshold := window.threshold(now, a.gateFrequency(groupID))
	probability *= a.gateDecay(groupID)
	window.record(now, probability, false)
	// 限流生效时意愿分被压到 0，这时任何阈值都不该放行
	return probability > 0 && probability >= threshold
}

// gateFrequency 取本群的目标发言频率，时段规则优先
func (a *Agent) gateFrequency(groupID int64) float64 {
	cfg := config.Get()
	base := cfg.Chat.TalkFrequency
	if cfg.Chat.EnableTimeRules {
		for _, rule := range cfg.Chat.TimeRules {
			if rule.GroupID != 0 && rule.GroupID != groupID {
				continue
			}
			start, end, ok := parseTimeRange(rule.TimeRange)
			if !ok {
				continue
			}
			now := time.Now()
			current := now.Hour()*60 + now.Minute()
			if start <= end && current >= start && current < end {
				return rule.TalkValue
			}
			if start > end && (current >= start || current < end) {
				return rule.TalkValue
			}
		}
	}
	return base
}

// gateDecay 防话痨衰减，发言过多时压低意愿分
func (a *Agent) gateDecay(groupID int64) float64 {
	cfg := config.Get()
	limitCfg := cfg.Chat.RateLimit
	if !limitCfg.Enabled || limitCfg.PeriodSec <= 0 || limitCfg.MaxMessages <= 0 {
		return 1
	}
	ctx, cancel := a.persistenceContext()
	defer cancel()
	start := time.Now().Add(-time.Duration(limitCfg.PeriodSec) * time.Second)
	count, err := a.memory.WithContext(ctx).GetMessageCountByTime(groupID, a.bot.GetSelfID(), start)
	if err != nil {
		return 1
	}
	maxMessages := float64(limitCfg.MaxMessages)
	current := float64(count)
	if current >= maxMessages {
		return 0
	}
	minProb := clampFloat(limitCfg.MinProb, 0, 1)
	return clampFloat((maxMessages-current)/maxMessages, minProb, 1)
}

// gateProbability 按消息内容算出发言意愿分
func (a *Agent) gateProbability(session *Session, pending []*sessionUser) float64 {
	mention := false
	atOther := false
	question := false
	textSeen := false
	for _, user := range pending {
		content := user.Content
		if content != "" {
			textSeen = true
		}
		if strings.Contains(content, "@") {
			atOther = true
		}
		if strings.ContainsAny(content, "?？") {
			question = true
		}
		if user.Mentioned || (a.persona != nil && a.persona.IsMentioned(content)) {
			mention = true
		}
	}
	if mention {
		atOther = false
	}
	mediaOnly := !textSeen
	total, self, lastSelf := session.RecentWindow(time.Now(), gateRecentWindow)
	selfRatio := 0.0
	if total > 0 {
		selfRatio = float64(self) / float64(total)
	}
	idle := 3600.0
	if !lastSelf.IsZero() {
		idle = time.Since(lastSelf).Seconds()
	}
	logit := gateIntercept +
		gateMentionWeight*boolToFloat(mention) +
		gateAtOtherWeight*boolToFloat(atOther) +
		gateQuestionWeight*boolToFloat(question) +
		gateMediaOnlyWeight*boolToFloat(mediaOnly) +
		gateSelfRatioWeight*clampFloat(selfRatio, 0, 1) +
		gateMessageNumWeight*math.Max(0, float64(total)) +
		gateIdleWeight*math.Log1p(math.Max(0, idle)) +
		gatePendingWeight*math.Min(20, float64(len(pending)))
	return 1 / (1 + math.Exp(-logit))
}

// boolToFloat 把布尔转成 0/1
func boolToFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

// parseTimeRange 解析时段配置
func parseTimeRange(value string) (int, int, bool) {
	var startHour, startMin, endHour, endMin int
	if _, err := fmt.Sscanf(value, "%d:%d-%d:%d", &startHour, &startMin, &endHour, &endMin); err != nil {
		return 0, 0, false
	}
	return startHour*60 + startMin, endHour*60 + endMin, true
}
