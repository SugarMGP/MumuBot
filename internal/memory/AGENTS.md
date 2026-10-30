# AGENTS

本目录负责 PostgreSQL 持久化、transcript/checkpoint、话题分层检索、长期记忆与具体情感。

- `runtime_store.go` 分配 input/visible 序号；`visible_seq` 在模型请求成功后写入，待处理输入保持 NULL；`reference_seen` 持久化 auto/tool 两个 seen 账本。
- `conversation_transcript` 是工作集，不是永久记录；只允许压缩事务删除 checkpoint 覆盖的可见行，原文在 `message_logs` 永久保留。
- `topic_compaction.go` 必须在一个事务内校验消息归属和摘要、写入话题摘要/归属/is_anchor 来源/概览/检查点并删除覆盖行；未分配消息超过软阈值时拒绝提交，候选里未分配消息的旧归属会清空，任一步失败全部回滚。
- `topic_digest.go` 的概览、active topic 和 reference baseline 顺序必须稳定；dormant 话题可搜索但不进入当前摘要块。
- active topic 退出时若仍有 open loop，程序必须把未完成事项补进 overview，不能只依赖模型自觉。
- 话题摘要不可变，按 topic/version 保存；`is_anchor` 标记关键消息，`message_log_id` 是消息内部 ID，撤回不清空、不失效。
- `topics_v2.go` 的关键词检索只做精确子串召回，单一 similarity 只用于排序；不做中文分词、向量或多层重排模型；跨会话结果必须保留来源。话题检索仍先在 Go 中过滤，但摘要、anchor 和时间范围必须批量读取，不能逐话题查询。
- `memory_v2.go` 和 `sticker.go` 的分页使用数据库排序、keyset 条件和 LIMIT，不把匹配集全部取回 Go 再排序；检索表达式必须与 trigram 索引一致。
- `memory_v2.go` 只用 `subject_type + subject_target + memory_type`；subject 允许 group/user/self/object，memory_type 允许 profile/experience；旧 kind 和来源字段已删除。
- 同一主体、同类型、同内容哈希精确去重；归档条目重复创建不得复活；更新、归档、恢复必须基于读取过的当前版本。
- `manageMemory` 的 1–8 项变更必须在一个事务内全成功或全回滚；主 Agent 不物理删除，后台只启用或归档。
- 普通正文最多 8000 字。
- `emotion_v2.go` 固定 12 类情感，同一对象同一情感仅一份当前状态；变化写 revisions，读取时惰性衰减，不跑定时衰减写库。
- `reference.go` 的自动参考约 1200 token：pinned 记忆 1000、情感与状态 200；auto/tool seen 分离，只有 auto 中确实失效的项生成墓碑。
- 原文、媒体和已发送消息永久保留；不再恢复消息日志清理、Embedding 补全、后台学习水位、知识证据强制或向量检索。
- 迁移定义在 `internal/migration/`；v17 只接受空库，旧 schema 明确拒绝，不自动迁移或 DROP；不使用 AutoMigrate。
