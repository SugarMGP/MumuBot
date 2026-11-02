# memoryeval

用真实日志人工标注的小评测集，比较三种召回：

- `exact`：原始 query 当关键词，走精确子串；
- `fuzzy`：原始 query 当关键词，走 `mode=fuzzy`（`%` / `<%` + similarity 排序）；
- `expanded`：用 case 里的 `keywords`（同义词/别名/相关词）走精确子串，`match` 默认 `or`。

## 为什么仓库里没有真实评测数据

评测集需要真实日志和人工标注的 `expected_ids`，这两样不在代码库里。这里只提供可运行的工具和格式示例，不伪造结果。

## 怎么造一个小评测集

1. 从真实库里取最近一批长期记忆和用户问题：

```sql
SELECT id, subject_type, subject_target, memory_type, label,
       left(content, 160) AS preview, updated_at
FROM knowledge_items
WHERE status = 'active'
ORDER BY updated_at DESC
LIMIT 50;

SELECT message_time, conversation_kind, target_id, nickname,
       left(text_content, 160) AS preview
FROM message_logs
ORDER BY id DESC
LIMIT 100;
```

2. 从这些真实问题里挑 20–50 条“应该能找到某条记忆”的 query，人工确认期望的 `knowledge_items.id`，写成 JSONL：

```json
{"id":"q1","query":"上次说的那张显卡","keywords":["显卡","GPU","4090"],"match":"or","expected_ids":[123]}
```

`expected_ids` 必须来自真实库，不要猜。`keywords` 是模拟“模型查之前先给同义词”的扩展，和 exact/fuzzy 对比。

3. 跑：

```bash
MUMU_DATABASE_DSN='postgres://user:pass@host:5432/mumu_bot?sslmode=disable' \
  go run ./cmd/memoryeval -cases cases.jsonl -limit 5
```

输出会按 exact / fuzzy / expanded 分别给 `hit@1`、`hit@K` 和漏召 case。

## 怎么看结果

- `exact` 高、`fuzzy`/`expanded` 不涨：词法已经够，别上向量；
- `fuzzy` 涨、`expanded` 也涨：先调 `fuzzySimilarityThreshold` / `fuzzyWordSimilarityThreshold`（`internal/memory/memory_v2.go`）；
- 只有 `expanded` 涨：说明主要缺同义词，提示词/查询扩展比向量便宜；
- 三者都漏：才是“改述/语义”问题，再考虑向量，别反过来。

## 阈值

- 记忆 fuzzy：`fuzzySimilarityThreshold=0.20`、`fuzzyWordSimilarityThreshold=0.40`；
- 话题相似度：`topicSimilarityThreshold=0.30`（`internal/memory/text_similarity.go`）。

阈值是启发式起点，用评测集调，不要凭感觉改。
