# AGENTS

本目录负责持久化和检索：PostgreSQL 数据表、pgvector/pg_trgm 混合检索、长期记忆、消息日志、成员画像、风格模式和表情包记录。

## 入口与职责

- `manager.go`：数据库连接、查询和后台清理任务初始化。
- `migrations_v1.go`：空库 v1 初始化、旧库 v1 迁移、schema 版本和迁移锁。
- `memory_ingest.go`、`merge_decider.go`：长期记忆合并决策、证据校验和写入。
- `learning_write.go`：learner 双游标输入与群文化/成员特征事务写入。
- `member_profile.go`：全局成员画像、群名片和带证据的成员特征。
- `style_pattern.go`、`jargon.go`、`sticker.go`：群文化和表情包相关数据。
- `mood.go`：固定单例的情绪状态及其自然衰减。

## 数据边界

- 长期记忆只接收完整 `MemoryClaim`；缺主体、空内容、无效类型、无消息证据、跨群、越过快照或主体不在固定范围时明确拒绝，不伪装成成功。
- 话题摘要和 `saveMemory` 使用相同的内联命题规则；每条 claim 只能用自己的证据消息作者和回复目标验证成员主体，不能借用同批其他 claim 的证据。
- 生产模型直接决定 claim 的主体和类型；不新增二次 claim 分类模型、`AllowedKinds`、昵称到 QQ 转换器或 open loop 强制目标化。
- 自动长期记忆写入只由话题摘要链路触发；learner 不写长期记忆。
- 成员画像保持全局 `user_id` 维度，除非明确需求，不引入 `group_id` 隔离。
- 消息日志的展示内容不能反向作为语义输入；语义链路必须读取原始文本字段。

## 存储规则

- 向量维度、证据来源和幂等约束必须在写入边界校验，不做兜底写库。
- memories、style_patterns、jargons、member_traits 的证据关系和精确幂等由数据库约束保证，不只依赖应用层查重。
- v1 只允许两种入口：全空库 `initializeV1Schema`，或无版本表但旧业务表完整时 `migrateV1`；两者不是别名。后续迁移使用 `migrateV2` 等顺序命名，不能修改已经发布的 v1 SQL。
- 启动迁移使用显式 DDL、单事务和 transaction-level advisory lock，不调用 `AutoMigrate`、模型或 OneBot API；运行账户仍需创建 vector、pg_trgm 和 schema 的权限。
- `memories.subject_user_id` 为非空整数，`0` 为群级，正数为 QQ；数据库不保存 `scope`。`memory_evidence` 只保存消息复合主键，消息撤回后保留审计关系并按剩余有效证据决定是否归档。
- 检索只使用 pgvector 精确向量、pg_trgm 和 RRF；没有明确需求和测量依据时不增加近似索引或额外检索中间件。
- 固定快照原文先规范成一份混合查询：拼接文本只生成一次 embedding，原始消息片段分别参与 pg_trgm 召回；话题和主动长期记忆共用该查询，不重复调用 embedding。
- rejected 黑话或风格模式只有获得新证据时才恢复为 candidate；已审核 active 内容不被自动学习覆盖。
- 表达方式的原始示例通过 `style_pattern_evidence` 关联 `message_logs.text_content` 动态读取；不增加 JSON 示例字段，查询必须限制当前群、未撤回消息和工具快照上界。
- 成员最近发言和群名片使用消息真实时间单调更新，延迟或乱序旧消息不能覆盖新状态。
- 清理旧消息时不能删除已分配话题的消息，也不能删除 learner 尚未处理的消息。
- 迁移、清理、衰减这类后台任务要可重复执行，不依赖一次性内存状态。

## 修改注意

- 新增字段要先确认是否属于长期记忆、话题、学习或后台展示，不要把多个职责塞进同一模型。
- GORM 模型变更后必须新增顺序 migration 并检查最终 schema、外键删除语义和表达式唯一索引；禁止让当前 struct 漂移反向改变旧 migration。
