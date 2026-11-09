# AGENTS

本目录负责 PostgreSQL 持久化、transcript/checkpoint、话题分层检索、长期记忆与具体情感。

- `runtime_store.go` 分配 input/visible 序号；`visible_seq` 在模型请求成功后写入，待处理输入保持 NULL。
- worker/life 摘要覆盖并 visible 的旧轨迹按 `history_days` 清理；`worker_sessions.context_summary` 与 `life_state.context_summary` 保存检查点，`context_through_id` 之后的 transcript 才进入下一次模型上下文。worker 的 `context_start_id` 是显式新上下文边界，之前的历史不进入工作集、到保留期后可独立清理；仍在库的记录可通过历史工具回读，已清理的如实不可用。
- `CommitLifeCheckpoint` / `CommitWorkerCheckpoint` 在同一事务里写入 visible 序号、检查点边界和 life 代头；`markVisibleTx` 是普通成功请求与检查点共用的分配入口。
- life 检查点把摘要保存在 `life_state.context_summary`，代头不进检查点；不要虚构未持久化的 `life_summary` transcript 行。摘要正文不是 file/ref 生命周期根。diaries 保存日期主键的 Markdown，不建记忆/话题/消息关联。内部投信只写目标 `agent_message` transcript 行，行号即内部编号，不再有 `session_messages` 表。
- `life_state` 是唯一 life 的单例状态（醒着/睡着、当前活动、最近睡眠、摘要检查点、运行问题）；`schedules` 保存一次性或 cron 日程，`life_next` 使用固定槽位唯一索引。
- `message_logs.file_refs` 保存真实文件消息的底层定位元信息、待取 `file_ref` 和快照 `image_ref`，临时下载 URL 不落库；历史与恢复必须同时保留展示正文和附件位置。引用快照本身在媒体目录按 SHA-256 去重，不新增 SQL 表。
- `worker_sessions.context_start_id` 与 `context_summary/context_through_id` 一起决定 worker 工作集；显式起点只允许在派工事务里写入。
- `conversation_transcript` 是工作集，不是永久记录。聊天压缩只删除 checkpoint 覆盖的可见行；life/worker 另按摘要覆盖、历史期限和当前工作集清理，worker 的 `context_start_id` 也可关闭旧段。任何删除都必须保留完整工具调用/回执边界，原文在 `message_logs` 按最低保留期保存，仍在当前/排队 transcript 引用的原文延后删除。
- `topic_compaction.go` 必须在一个事务内校验消息归属和摘要、写入话题摘要/归属/is_anchor 来源/概览/检查点并删除覆盖行；未分配消息超过软阈值时拒绝提交，候选里未分配消息的旧归属会清空，任一步失败全部回滚。
- `topic_digest.go` 的概览、active topic 顺序必须稳定；dormant 话题可搜索但不进入当前摘要块；置顶记忆由 `pinned_memory` 代头单独渲染。
- active topic 退出时不再由程序把 open loop 机械复制进 overview；话题完全按遗忘曲线保留，压缩模型只按真实状态整理有意义的未完事项。
- 话题摘要不可变，按 topic/version 保存；`is_anchor` 标记关键消息，`message_log_id` 是消息内部 ID，撤回不清空、不失效。
- `topics_v2.go` 的关键词检索先做精确子串匹配，再用字符二元组相似度（阈值 0.30）补召回，排序使用单一 similarity/更新时间/内部 ID；不做中文分词、向量或多层重排模型；跨会话结果必须保留来源。话题检索仍先在 Go 中过滤，但摘要、anchor 和时间范围必须批量读取，不能逐话题查询；时间范围用 `topic_assignments` 的现存原文和 `topic_summary_sources.message_time` 判断，原文删掉后仍保留来源事件时间。
- `memory_v2.go` 和 `sticker.go` 的分页使用数据库排序、keyset 条件和 LIMIT，不把匹配集全部取回 Go 再排序；检索表达式必须与 trigram 索引一致。
- `memory_v2.go` 只用 `subject_type + subject_target + memory_type`；subject 允许 group/user/self/object，memory_type 允许 profile/experience；旧 kind 和来源字段已删除。
- 同一主体、同类型、同内容哈希只对 active 条目精确去重；归档条目重复创建不得复活；更新、归档必须基于读取过的当前版本，恢复入口不保留。
- `manageMemory` 的批量变更必须在一个事务内全成功或全回滚，不设条数上限；主 Agent 不物理删除；后台可版本检查后编辑有效正文与置顶、归档，已归档记录可确认后永久删除并级联清理修订，不能恢复。
- 非置顶记忆和所有话题共用 `retention.go` 的指数曲线；检索与直接读取都只在数据库强化成功后记录，同 run 同类同 ID 一次，压缩/后台/装配不强化。到期判断始终按当前时间，分页快照只冻结内容与排序；创建记忆只结算同主体/类型/hash 的到期重复项。
- 普通正文最多 8000 字。
- `emotion_v2.go` 维护全局自由感受：ID 是身份，允许同名并存；按 `id+version` 事务更新，24 小时半衰期读时淡化；低于 0.001 时用实际跨阈值时间写 `ended_at`、递增版本并追加完整修订，读取前惰性结算并由维护循环补扫；`emotion_revisions` 保存完整状态快照。情感正文不是工具结果文件的生命周期根。
- 自然平复补扫必须遍历全部未结束条目或由数据库直接筛出到期项，不能只看固定前 500 条；`ended_at` 使用实际跨阈值时间，不能先把小数小时截成整数。
- `pinned.go` 只返回 active pinned，置顶记忆不按 token 裁剪；情感与状态交给 `getCurrentStatus` 查询。
- 原文、合并转发和聊天媒体按 `history_days` 保留最低期限，到期且不再被 transcript 直接引用的原文才删除；同媒体仍被其他保留原文引用时不删，收藏表情副本独立保留。工具结果文件和文件/图片快照只由现存 transcript 中对话/执行轨迹行的引用闭包（`result_ref`、payload 文本、`r` 嵌套内容）保活，代头、摘要、系统提醒、记忆和日记里的裸 ref 不保活，压缩即时释放不再引用的候选，孤儿按启动/每日补扫清理。不恢复 Embedding 补全、后台学习水位、知识证据强制或向量检索。
- 迁移定义在 `internal/migration/`；空库初始化到 v18，当前 v17 按显式事务保留数据升级，v18 校验后复用，更早的 schema 明确拒绝；不使用 AutoMigrate。
- `Manager.WithContext` 只能替换数据库句柄，必须原样携带同一个 RefStore、MediaDir 及其它共享单例；工具使用轻量 Manager 不能丢失文件快照、pending 锁或 GC 锁。
- `Raw` 查询返回的字段必须通过 SQL alias 或明确的 GORM column 映射到接收字段；不能因为 `ID` 自动映射就假定 `context_summary/context_start_id/context_through_id` 也会映射。
- 删除 transcript 时先在原事务中收集本批 `result_ref`/`file_ref`/`image_ref` 候选，并在删除 `r` 全文前展开其嵌套引用；提交后按完整引用闭包即时释放失根候选。全库孤儿扫描只用于启动/每日补扫，宽限至少 24 小时。
- `visible_seq=NULL` 可能表示已经持久化但从未被后续成功请求读入的 assistant/tool 轨迹，不能据此判断记录不存在；worker 显式关闭的旧上下文按时间和完整工具链清理，不要求这些输出再次获得 visible。
