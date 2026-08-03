# AGENTS

本目录负责持久化和检索：PostgreSQL 数据表、pgvector/pg_trgm 混合检索、长期记忆、消息日志、成员画像、风格模式和表情包记录。

## 入口与职责

- `manager.go`：数据库连接、扩展启用、最终表结构和后台清理任务初始化。
- `claim_extractor.go`：长期记忆 claim（结构化事实候选）提取和允许类型校验。
- `memory_ingest.go`、`merge_decider.go`：长期记忆合并决策、证据校验和写入。
- `learning_write.go`：learner 双游标输入与群文化/成员特征事务写入。
- `member_profile.go`：全局成员画像、群名片和带证据的成员特征。
- `style_pattern.go`、`jargon.go`、`sticker.go`：群文化和表情包相关数据。
- `mood.go`：固定单例的情绪状态及其自然衰减。

## 数据边界

- 长期记忆只接收有效 claim；空内容、无效类型、证据不足时跳过并记录日志。
- 调用方提供 `AllowedKinds` 时必须在代码边界校验；话题摘要的 open loops 只允许写为 `goal`。
- 自动长期记忆写入只由话题摘要链路触发；learner 不写长期记忆。
- 成员画像保持全局 `user_id` 维度，除非明确需求，不引入 `group_id` 隔离。
- 消息日志的展示内容不能反向作为语义输入；语义链路必须读取原始文本字段。

## 存储规则

- 向量维度、证据来源和幂等约束必须在写入边界校验，不做兜底写库。
- memories、style_patterns、jargons、member_traits 的证据关系和精确幂等由数据库约束保证，不只依赖应用层查重。
- 当前 schema 面向全新 PostgreSQL 数据库，不维护旧数据兼容迁移、冗余字段或过渡表。
- 启动时在表结构迁移前执行固定的 `CREATE EXTENSION IF NOT EXISTS` 启用 vector 和 pg_trgm；服务端扩展文件和数据库权限仍属于部署前提。
- 检索只使用 pgvector 精确向量、pg_trgm 和 RRF；没有明确需求和测量依据时不增加近似索引或额外检索中间件。
- 固定快照原文先规范成一份混合查询：拼接文本只生成一次 embedding，原始消息片段分别参与 pg_trgm 召回；话题和主动长期记忆共用该查询，不重复调用 embedding。
- rejected 黑话或风格模式只有获得新证据时才恢复为 candidate；已审核 active 内容不被自动学习覆盖。
- 表达方式的原始示例通过 `style_pattern_evidence` 关联 `message_logs.text_content` 动态读取；不增加 JSON 示例字段，查询必须限制当前群、未撤回消息和工具快照上界。
- 成员最近发言和群名片使用消息真实时间单调更新，延迟或乱序旧消息不能覆盖新状态。
- 清理旧消息时不能删除已分配话题的消息，也不能删除 learner 尚未处理的消息。
- 迁移、清理、衰减这类后台任务要可重复执行，不依赖一次性内存状态。

## 修改注意

- 新增字段要先确认是否属于长期记忆、话题、学习或后台展示，不要把多个职责塞进同一模型。
- GORM（Go 的数据库 ORM）模型变更后要检查最终 schema、外键删除语义和表达式唯一索引；当前不兼容旧数据。
