package modelstats

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	callbacktpl "github.com/cloudwego/eino/utils/callbacks"
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

func (r *Recorder) Record(task, modelName string, duration time.Duration, failed bool, totalTokens int, usageReported bool) {
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
			if err := tx.Exec(`INSERT INTO model_call_hourly(bucket_start,task,model,request_count,failure_count,latency_ms_sum,total_tokens,usage_reported_count)
				VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(bucket_start,task,model) DO UPDATE SET
				request_count=model_call_hourly.request_count+EXCLUDED.request_count,
				failure_count=model_call_hourly.failure_count+EXCLUDED.failure_count,
				latency_ms_sum=model_call_hourly.latency_ms_sum+EXCLUDED.latency_ms_sum,
				total_tokens=model_call_hourly.total_tokens+EXCLUDED.total_tokens,
				usage_reported_count=model_call_hourly.usage_reported_count+EXCLUDED.usage_reported_count`,
				k.Bucket, k.Task, k.Model, item.RequestCount, item.FailureCount, item.LatencyMSSum, item.TotalTokens, item.UsageReportedCount).Error; err != nil {
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
	target.UsageReportedCount += source.UsageReportedCount
}

type startedAtKey struct{}

func Handler(task, modelName string) callbacks.Handler {
	recorder := defaultRecorder.Load()
	return callbacktpl.NewHandlerHelper().
		ChatModel(&callbacktpl.ModelCallbackHandler{
			OnStart: func(ctx context.Context, _ *callbacks.RunInfo, _ *model.CallbackInput) context.Context {
				return context.WithValue(ctx, startedAtKey{}, time.Now())
			},
			OnEnd: func(ctx context.Context, _ *callbacks.RunInfo, output *model.CallbackOutput) context.Context {
				total, reported := 0, false
				if output != nil && output.TokenUsage != nil {
					total, reported = output.TokenUsage.TotalTokens, true
				}
				recorder.Record(task, modelName, elapsed(ctx), false, total, reported)
				return ctx
			},
			OnError: func(ctx context.Context, _ *callbacks.RunInfo, _ error) context.Context {
				recorder.Record(task, modelName, elapsed(ctx), true, 0, false)
				return ctx
			},
		}).
		Embedding(&callbacktpl.EmbeddingCallbackHandler{
			OnStart: func(ctx context.Context, _ *callbacks.RunInfo, _ *embedding.CallbackInput) context.Context {
				return context.WithValue(ctx, startedAtKey{}, time.Now())
			},
			OnEnd: func(ctx context.Context, _ *callbacks.RunInfo, output *embedding.CallbackOutput) context.Context {
				total, reported := 0, false
				if output != nil && output.TokenUsage != nil {
					total, reported = output.TokenUsage.TotalTokens, true
				}
				recorder.Record(task, modelName, elapsed(ctx), false, total, reported)
				return ctx
			},
			OnError: func(ctx context.Context, _ *callbacks.RunInfo, _ error) context.Context {
				recorder.Record(task, modelName, elapsed(ctx), true, 0, false)
				return ctx
			},
		}).Handler()
}

func elapsed(ctx context.Context) time.Duration {
	started, _ := ctx.Value(startedAtKey{}).(time.Time)
	if started.IsZero() {
		return 0
	}
	return time.Since(started)
}

type AggregateRow struct {
	Label              string  `json:"label"`
	RequestCount       int64   `json:"request_count"`
	FailureCount       int64   `json:"failure_count"`
	AverageLatencyMS   float64 `json:"average_latency_ms"`
	TotalTokens        int64   `json:"total_tokens"`
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
	var snapshot Snapshot
	base := `FROM model_call_hourly`
	args := []any{}
	if !since.IsZero() {
		base += ` WHERE bucket_start>=?`
		args = append(args, since)
	}
	if err := db.WithContext(ctx).Raw(`SELECT '全部' label, COALESCE(sum(request_count),0) request_count, COALESCE(sum(failure_count),0) failure_count,
		CASE WHEN sum(request_count)>0 THEN sum(latency_ms_sum)::float8/sum(request_count) ELSE 0 END average_latency_ms,
		COALESCE(sum(total_tokens),0) total_tokens, COALESCE(sum(usage_reported_count),0) usage_reported_count `+base, args...).Scan(&snapshot.Totals).Error; err != nil {
		return Snapshot{}, err
	}
	if err := db.WithContext(ctx).Raw(`SELECT task label, sum(request_count) request_count, sum(failure_count) failure_count,
		sum(latency_ms_sum)::float8/sum(request_count) average_latency_ms, sum(total_tokens) total_tokens, sum(usage_reported_count) usage_reported_count `+base+` GROUP BY task ORDER BY request_count DESC`, args...).Scan(&snapshot.ByTask).Error; err != nil {
		return Snapshot{}, err
	}
	for i := range snapshot.ByTask {
		snapshot.ByTask[i].Label = taskLabel(snapshot.ByTask[i].Label)
	}
	if err := db.WithContext(ctx).Raw(`SELECT model label, sum(request_count) request_count, sum(failure_count) failure_count,
		sum(latency_ms_sum)::float8/sum(request_count) average_latency_ms, sum(total_tokens) total_tokens, sum(usage_reported_count) usage_reported_count `+base+` GROUP BY model ORDER BY request_count DESC`, args...).Scan(&snapshot.ByModel).Error; err != nil {
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

func taskLabel(task string) string {
	switch task {
	case "react":
		return "主对话"
	case "topic_assignment":
		return "话题归属"
	case "topic_summary":
		return "话题摘要"
	case "memory_merge":
		return "记忆合并"
	case "learning_culture":
		return "群文化学习"
	case "learning_profile":
		return "成员画像学习"
	case "learning_review":
		return "学习审核"
	case "vision":
		return "图片与视频理解"
	case "embedding":
		return "向量检索"
	default:
		return task
	}
}
