package modelstats

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type key struct {
	Bucket time.Time
	Task   string
	Model  string
}

type aggregate struct {
	RequestCount       int64
	FailureCount       int64
	LatencyMSSum       int64
	TotalTokens        int64
	PromptTokens       int64
	CachedTokens       int64
	UsageReportedCount int64
}

type Recorder struct {
	mu      sync.Mutex
	pending map[key]aggregate
	db      *gorm.DB
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

var defaultRecorder atomic.Pointer[Recorder]

func NewRecorder() *Recorder {
	return &Recorder{pending: make(map[key]aggregate), stop: make(chan struct{}), done: make(chan struct{})}
}

func SetDefault(recorder *Recorder) { defaultRecorder.Store(recorder) }

func (r *Recorder) Start(db *gorm.DB) {
	if r == nil || db == nil {
		return
	}
	r.db = db
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		defer close(r.done)
		for {
			select {
			case <-ticker.C:
				r.flush()
			case <-r.stop:
				r.flush()
				return
			}
		}
	}()
}

func (r *Recorder) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() { close(r.stop) })
	if r.db != nil {
		<-r.done
	}
}

func (r *Recorder) Record(task, modelName string, duration time.Duration, failed bool, totalTokens, promptTokens, cachedTokens int, usageReported bool) {
	if r == nil || task == "" || modelName == "" {
		return
	}
	k := key{Bucket: time.Now().UTC().Truncate(time.Hour), Task: task, Model: modelName}
	r.mu.Lock()
	item := r.pending[k]
	item.RequestCount++
	if failed {
		item.FailureCount++
	}
	latency := duration.Milliseconds()
	item.LatencyMSSum += latency
	if usageReported {
		item.TotalTokens += int64(totalTokens)
		item.PromptTokens += int64(promptTokens)
		item.CachedTokens += int64(cachedTokens)
		item.UsageReportedCount++
	}
	r.pending[k] = item
	r.mu.Unlock()
}

func (r *Recorder) flush() {
	if r == nil || r.db == nil {
		return
	}
	r.mu.Lock()
	batch := r.pending
	r.pending = make(map[key]aggregate)
	r.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		for k, item := range batch {
			if err := tx.Exec(`INSERT INTO model_call_hourly(bucket_start,task,model,request_count,failure_count,latency_ms_sum,total_tokens,prompt_tokens,cached_tokens,usage_reported_count)
				VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(bucket_start,task,model) DO UPDATE SET
				request_count=model_call_hourly.request_count+EXCLUDED.request_count,
				failure_count=model_call_hourly.failure_count+EXCLUDED.failure_count,
				latency_ms_sum=model_call_hourly.latency_ms_sum+EXCLUDED.latency_ms_sum,
				total_tokens=model_call_hourly.total_tokens+EXCLUDED.total_tokens,
				prompt_tokens=model_call_hourly.prompt_tokens+EXCLUDED.prompt_tokens,
				cached_tokens=model_call_hourly.cached_tokens+EXCLUDED.cached_tokens,
				usage_reported_count=model_call_hourly.usage_reported_count+EXCLUDED.usage_reported_count`,
				k.Bucket, k.Task, k.Model, item.RequestCount, item.FailureCount, item.LatencyMSSum, item.TotalTokens, item.PromptTokens, item.CachedTokens, item.UsageReportedCount).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		return
	}
	zap.L().Warn("写入模型调用统计失败，将保留到下次重试", zap.Error(err))
	r.mu.Lock()
	for k, item := range batch {
		current := r.pending[k]
		mergeAggregate(&current, item)
		r.pending[k] = current
	}
	r.mu.Unlock()
}

func mergeAggregate(target *aggregate, source aggregate) {
	target.RequestCount += source.RequestCount
	target.FailureCount += source.FailureCount
	target.LatencyMSSum += source.LatencyMSSum
	target.TotalTokens += source.TotalTokens
	target.PromptTokens += source.PromptTokens
	target.CachedTokens += source.CachedTokens
	target.UsageReportedCount += source.UsageReportedCount
}

// Record 记录一次模型请求；失败请求也计入时延与失败数
func Record(task, modelName string, duration time.Duration, failed bool, total, prompt, cached int, usageReported bool) {
	recorder := defaultRecorder.Load()
	if recorder == nil {
		return
	}
	recorder.Record(task, modelName, duration, failed, total, prompt, cached, usageReported)
}

type AggregateRow struct {
	Label              string  `json:"label"`
	RequestCount       int64   `json:"request_count"`
	FailureCount       int64   `json:"failure_count"`
	AverageLatencyMS   float64 `json:"average_latency_ms"`
	TotalTokens        int64   `json:"total_tokens"`
	PromptTokens       int64   `json:"prompt_tokens"`
	CachedTokens       int64   `json:"cached_tokens"`
	UsageReportedCount int64   `json:"usage_reported_count"`
}

type SeriesRow struct {
	BucketStart  time.Time `json:"bucket_start"`
	RequestCount int64     `json:"request_count"`
	TotalTokens  int64     `json:"total_tokens"`
	LatencyMSSum int64     `json:"latency_ms_sum"`
}

type Snapshot struct {
	Totals  AggregateRow   `json:"totals"`
	ByTask  []AggregateRow `json:"by_task"`
	ByModel []AggregateRow `json:"by_model"`
	Series  []SeriesRow    `json:"series"`
}

func Query(ctx context.Context, db *gorm.DB, since time.Time) (Snapshot, error) {
	snapshot := Snapshot{
		ByTask: []AggregateRow{}, ByModel: []AggregateRow{}, Series: []SeriesRow{},
	}
	base := `FROM model_call_hourly`
	args := []any{}
	if !since.IsZero() {
		base += ` WHERE bucket_start>=?`
		args = append(args, since)
	}
	if err := db.WithContext(ctx).Raw(`SELECT '全部' label, COALESCE(sum(request_count),0) request_count, COALESCE(sum(failure_count),0) failure_count,
		CASE WHEN sum(request_count)>0 THEN sum(latency_ms_sum)::float8/sum(request_count) ELSE 0 END average_latency_ms,
		COALESCE(sum(total_tokens),0) total_tokens, COALESCE(sum(prompt_tokens),0) prompt_tokens, COALESCE(sum(cached_tokens),0) cached_tokens, COALESCE(sum(usage_reported_count),0) usage_reported_count `+base, args...).Scan(&snapshot.Totals).Error; err != nil {
		return Snapshot{}, err
	}
	if err := db.WithContext(ctx).Raw(`SELECT task label, sum(request_count) request_count, sum(failure_count) failure_count,
		sum(latency_ms_sum)::float8/sum(request_count) average_latency_ms, sum(total_tokens) total_tokens, sum(prompt_tokens) prompt_tokens, sum(cached_tokens) cached_tokens, sum(usage_reported_count) usage_reported_count `+base+` GROUP BY task ORDER BY request_count DESC`, args...).Scan(&snapshot.ByTask).Error; err != nil {
		return Snapshot{}, err
	}
	if err := db.WithContext(ctx).Raw(`SELECT model label, sum(request_count) request_count, sum(failure_count) failure_count,
		sum(latency_ms_sum)::float8/sum(request_count) average_latency_ms, sum(total_tokens) total_tokens, sum(prompt_tokens) prompt_tokens, sum(cached_tokens) cached_tokens, sum(usage_reported_count) usage_reported_count `+base+` GROUP BY model ORDER BY request_count DESC`, args...).Scan(&snapshot.ByModel).Error; err != nil {
		return Snapshot{}, err
	}
	bucket := seriesBucketExpression(since)
	if err := db.WithContext(ctx).Raw(`SELECT `+bucket+` bucket_start,sum(request_count) request_count,sum(total_tokens) total_tokens,sum(latency_ms_sum) latency_ms_sum `+base+` GROUP BY 1 ORDER BY 1`, args...).Scan(&snapshot.Series).Error; err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func seriesBucketExpression(since time.Time) string {
	if since.IsZero() {
		return `date_trunc('month', bucket_start)`
	}
	duration := time.Since(since)
	if duration > 14*24*time.Hour {
		return `date_trunc('day', bucket_start)`
	}
	if duration > 2*24*time.Hour {
		return `date_bin('6 hours', bucket_start, TIMESTAMPTZ '2000-01-01')`
	}
	return `bucket_start`
}
