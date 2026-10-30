# AGENTS

本目录负责 MumuBot 的运行编排：OneBot 提交、transcript 生命周期、模型请求、同步话题压缩、工具回执和撤回补偿。

## 入口与职责

- `react_agent.go`：Agent 依赖、工具清单、ADK Agent、启动停止和运行态查询。
- `session.go`：会话工作集、待处理输入、OneBot 消息 ID、媒体索引、参考回执和上下文装配。
- `session_compaction.go`：安全压缩候选、checkpoint 应用、token 校准和失败退避。
- `session_tools.go`：工具 transcript 幂等回执、大结果文件和中断修复。
- `session_restore.go`：从 checkpoint、visible 行、pending input 和近期原文恢复。
- `compaction.go`：同一主模型的受限压缩器和 `finishCompaction` 提交。
- `run.go`：模型调用边界注入、可见序号、压缩触发和 provider 用量校准。
- `commit.go` / `reorder.go`：到达序号提交、消息落库和自身消息处理。
- `private.go`：好友私聊收发与单任务调度。
- `recall.go`：撤回标记、待补偿和原消息不改写。

## 编排边界

- 主聊天和压缩共用当前模型；不要恢复 low、后台 Learner、Embedding 或独立压缩模型。
- 主聊天是单次 ReAct，不拆 Planner/Replyer；是否发言最终由模型和工具决定，门控只做前置过滤。
- 每群/每好友只允许一个 think 运行；运行期间新消息进入会话，不创建 rerun，后续正常调度继续消费。
- 思考从会话起始到达序号开始；`BeforeModelRewriteState` 在新消息到达时注入，同一批消息不合并。
- `conversation_transcript` 是唯一工作集；不要在 `runAgent` 事件流或 `AfterAgent` 另装一套 transcript。
- user transcript 在消息落库后创建；assistant 在模型成功后保存；工具 wrapper 在调用前后保存 running/终态回执。
- 模型请求成功后才给本次请求实际包含的 transcript 行写 `visible_seq`；请求失败不推进、不回滚，也不自动重试。
- `visible_seq` 按请求消息顺序分配，不能用数据库 ID 重排；内部参考在待处理消息之前，工具结果在请求它的 assistant 之后。
- `agent_message` 是独立的会话间内部消息条目，不是真人 user 行；它使用会话本地 `Order` 维持待处理顺序，不占用 OneBot `ArrivalSeq`。目标空闲时启动 ReAct，运行中在下一个模型边界单独补入且不重复。
- `reference_seen` 的 auto/tool 回执只在模型请求成功后合并；失败时保留 pending transcript，不推进 seen。
- 重启恢复必须按 transcript payload 原样恢复 user/assistant/tool/internal 消息，禁止重新渲染已固定正文。
- 遗留 running/planned 工具改成 `unknown`，assistant 工具调用没有对应回执时补 `not_executed`，都不自动重放。
- `run_id + call_id` 是工具回执幂等键；不同 CallID 的重复搜索、读取和多次发言不拦截。
- 大文本工具结果超过约 4000 token 时只把预览交给模型，完整内容写内容寻址文件；`viewImage` 图片本体保留。
- 工具中间件只保留 `ToolTranscriptMiddleware`、`ToolErrorMiddleware`、Unknown handler、参数规范和顺序执行；不要恢复全局 ToolAction/ToolDedup 或消费水位。
- 压缩只在估算输入达到窗口 90% 时触发，目标 40%，55% 为可接受上限；超过上限不得提交。
- 模型、人格、MCP 和运行态使用 generation 快照；配置切换只替换 generation，不重启进程。
- 压缩候选只包含完整、已可见、协议闭合的旧交互段，保护当前批次、活动工具链、未闭合 CallID 和最近尾部。
- 压缩器只能读当前会话并使用 `searchTopics`、`readTopic`、`searchHistory`、`readToolResult` 和 `finishCompaction`；不提供 `readHistory`，不能写记忆、情感、便签或外部系统。
- 压缩失败、覆盖遗漏、预算超限、数据库失败和结果不明都不能删除 transcript；失败后退避 60 秒。
- provider 返回上下文溢出时允许一次强制压缩后重试主请求；仍失败则保留状态结束本轮。
- 模型请求不设置整体超时，只限制连接和 TLS 握手 10 秒；Agent 关闭时的上下文取消仍然生效。
- 主 Agent 每轮默认最多 50 步；压缩器步数独立配置。
- 发言、戳一戳、贴表情、撤回等外部侧效应仍由工具自身和 OneBot API 保证真实成功，不伪造完成状态。
- 撤回只打标记，不改写 transcript 前缀；后续消息附撤回说明。
- 自己发送的消息写入 `message_logs` 但不额外生成 transcript user 行，因为动作已由 assistant/tool 轨迹表达。
