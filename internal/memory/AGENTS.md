# AGENTS

本目录负责 PostgreSQL 持久化、统一整理事务、知识证据和混合检索。

- conversation.go 是后台写入入口；在同群 advisory lock 下原子保存归属、摘要、知识和统一进度。
- knowledge_commit.go 是共同知识提交器，供统一整理和显式 saveMemory 使用；同群范围、主体、完整证据、固定上界与乐观并发校验不可绕过。
- saveMemory 的外部消息编号在 knowledge_claims.go 解析；后台直接使用已读取的内部消息 ID，不保存负数主体。
- knowledge_scan.go 从 learning_states 单一水位读取连续原文；旧 assignment 不再作为前置消费门槛。
- knowledge_history.go 只读取本群、固定上界、未撤回的原文；展示文本不能反向作为语义输入。
- knowledge_items 只保存启用和归档知识，新知识默认启用，旧知识省略状态保留原状态；知识语义改变必须新建，关系为双端点，不另建通用实体仓库。
- 每组1-16条消息是一份完整证据，任一必要消息撤回则整组失效；无其他完整组时归档，不自动恢复旧解释。
- 普通关系要求两端启用及独立有效依据；supersedes 允许旧端归档。端点归档或证据失效时同事务归档不合法关系，不因重复保存恢复归档状态。
- reviewed_through_id 仅作维护后的快照可见上界；维护查询允许核对缺证据的归档知识，正常召回不放行。
- topic_summaries 的 embedding 可以为空，知识和摘要向量共用有预算的后台补全；不在保存事务调用模型。
- 成员基础资料仍是全局 user_id，不恢复 speaking/phrase 特征学习。
- 已发布迁移不能修改；新增迁移顺序执行，用事务和显式 SQL，不使用 AutoMigrate。
- v4 将旧学习水位改为统一整理水位，必要时回退以接续未完成任务，保留原话题身份。
- 消息清理不得删除已有话题或未完成统一整理的消息。
- 检索使用现有 pgvector、pg_trgm 和 RRF；无测量依据不增加图数据库、近似索引或重排模型。
