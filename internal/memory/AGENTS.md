# AGENTS

本目录负责 PostgreSQL 持久化、transcript/checkpoint、话题分层检索、长期记忆与具体情感。

- `runtime_store.go` 只通过 `conversation_sessions` 分配 input/visible/message 序号；`visible_seq` 在模型请求成功后写入，待处理输入保持 NULL。
- `conversation_transcript` 是工作集，不是永久记录；只允许压缩事务删除 checkpoint 覆盖的可见行，原文在 `message_logs` 永久保留。
- `topic_compaction.go` 必须在一个事务内校验覆盖完整性、写入话题摘要/归属/source/anchor/概览/检查点并删除覆盖行；任一步失败全部回滚。
- `topic_digest.go` 的概览、active topic 和 reference baseline 顺序必须稳定；dormant 话题可搜索但不进入当前摘要块。
- active topic 退出时若仍有 open loop，程序必须把未完成事项补进 overview，不能只依赖模型自觉。
- 话题摘要不可变，按 topic/version 保存；anchor/source 是消息内部 ID，撤回不清空、不失效。
- `topics_v2.go` 的关键词检索只做精确子串和 pg_trgm，不做中文分词、向量或重排模型；跨会话结果必须保留来源。
- `memory_v2.go` 的知识类型只允许 fact/preference/constraint/goal/term/expression/alias；`subject_user_id` 是主体，`conversation_kind + target_id` 只是来源。
- 同一主体、同类型、同内容哈希精确去重；归档条目重复创建不得复活；更新、归档、恢复必须基于读取过的当前版本。
- `manageMemory` 的 1–8 项变更必须在一个事务内全成功或全回滚；主 Agent 不物理删除，后台只启用或归档。
- 普通正文最多 8000 字。
- `emotion_v2.go` 固定 12 类情感，同一对象同一情感仅一份当前状态；变化写 revisions，读取时惰性衰减，不跑定时衰减写库。
- `reference.go` 的自动参考固定约 1200 token：核心 800、便签与约定 200、情感与状态 200；完整条目原子选取。
- 原文、媒体和已发送消息永久保留；不再恢复消息日志清理、Embedding 补全、后台学习水位、知识证据强制或向量检索。
- 迁移定义在 `internal/migration/`；v15 保留旧证据/关系/学习/私聊摘要/心情为 `legacy_*` 历史资料，不使用 AutoMigrate 或 DROP CASCADE。
