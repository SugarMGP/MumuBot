# AGENTS

本目录负责 MumuBot 的运行编排：OneBot 提交、transcript 生命周期、模型请求、同步话题压缩、工具回执和撤回补偿。

## 入口与职责

- `react_agent.go` / `generation.go`：Agent 依赖、工具清单、ADK Agent、generation 构造、启动停止和运行态查询。
- `session.go`：会话工作集、待处理输入、OneBot 消息 ID、媒体索引和上下文装配。
- `session_compaction.go`：安全压缩候选、checkpoint 应用、token 校准和失败退避。
- `session_failure.go`：失败批次标记与压缩前置门，防止同一批输入被周期重跑。
- `session_tools.go`：工具 transcript 幂等回执、大结果文件和中断修复。
- `session_restore.go`：从 checkpoint、visible 行和 pending input 恢复；不再从 `message_logs` 回灌原文。
- `worker_files.go` / `personal_files.go`：worker 文件字节客户端、life 文件适配器和真实文件/图片投递。
- `compaction.go`：同一主模型的受限压缩器和 `finishCompaction` 提交。
- `run.go`：模型调用边界注入、可见序号、压缩触发和 provider 用量校准。
- `commit.go` / `reorder.go`：到达序号提交、消息落库和自身消息处理。
- `private.go`：好友私聊收发与单任务调度。
- `life.go`：唯一生活主线的运行、装配、摘要检查和首次启动处境。
- `life_scheduler.go`：持久日程的到期投递与睡眠恢复。
- `life_prompt.go`：生活场合的职责说明。
- `capacity.go`：模型上下文窗口切换前的真实工作集容量检查。
- `retention.go`：启动/周期清理、工具结果和文件快照引用回收。
- `recall.go`：撤回标记、待补偿和原消息不改写。

## 编排边界

- QQ 聊天和压缩共用 QQ 档案，life 与 worker 各自的运行和摘要使用独立所选档案；不要恢复 low、后台 Learner、Embedding 或独立压缩模型。
- 主聊天是单次 ReAct，不拆 Planner/Replyer；是否发言最终由模型和工具决定，门控只做前置过滤。
- 每群/每好友只允许一个 think 运行；life 单并发且不占 worker 名额；运行期间新消息进入会话，不创建 rerun，后续正常调度继续消费。
- 思考从会话起始到达序号开始；`BeforeModelRewriteState` 在新消息到达时注入，同一批消息不合并。聊天首次模型请求失败后，同一批本地输入没有更新时不周期重跑；新真人消息、明确唤醒或新内部输入才重新调度。
- 失败批次的上限取自最后一次实际发出的 request state；think 入口 pending 只说明入口收到了什么，不能替代本次请求真正纳入的输入。
- `conversation_transcript` 是唯一工作集；不要在 `runAgent` 事件流或 `AfterAgent` 另装一套 transcript。
- user transcript 在消息落库后创建；assistant 在模型成功后保存；工具 wrapper 在调用前后保存 running/终态回执。
- 模型请求成功后才给本次请求实际包含的 transcript 行写 `visible_seq`；请求失败不推进、不回滚；聊天失败批次等新输入，life 由生活时钟按持久状态最多补试两次、间隔五分钟。
- 模型新返回的 assistant 不在本次请求输入里，不得提前写 `visible_seq`；assistant/tool 的持久化可读性由轨迹本身保证。
- `visible_seq` 按请求消息顺序分配，不能用数据库 ID 重排；代头在前，工具结果在请求它的 assistant 之后。
- 消息写入 transcript 后必须把行号标回同一内存消息对象，真实输入从 `state.Messages` 按顺序收集；不要再维护 `VisibleTranscriptIDs` 之类的侧面猜测列表。
- life/worker 的摘要提交在同一事务内完成 visible 序号、检查点边界（life 还含代头替换）；life 摘要保存在 `life_state.context_summary`，代头由 Finalize 装入当前请求。摘要正文不是 file/ref 生命周期根。
- `sleepUntil` 只有在睡眠状态、起床安排和工具回执都成功后才允许收尾当前回合；成功回执后的终结不能依赖下一次模型节点或额外步数，失败继续让模型在剩余步数内修正。
- `readSessionHistory` 的 entries 返回已持久化的 assistant/tool 轨迹，user/observation/agent_message 仍只算实际进入成功请求的输入；单条全文用 `readSessionEntry`。`file_refs` 随消息日志保存，历史返回附件元信息，不把临时 URL 当长期资源。
- `agent_message` 是独立的会话间内部消息条目，不是真人 user 行；它使用会话本地 `Order` 维持待处理顺序，不占用 OneBot `ArrivalSeq`。目标空闲时启动 ReAct，运行中在下一个模型边界单独补入且不重复。`wake=false` 只保存，不触发新回合，也不作为群门控的强交互。
- 重启恢复必须按 transcript payload 原样恢复 user/assistant/tool/internal 消息，禁止重新渲染已固定正文。
- 遗留 running/planned 工具改成 `unknown`，assistant 工具调用没有对应回执时补 `not_executed`，都不自动重放。
- `run_id + call_id` 是工具回执幂等键；不同 CallID 的重复搜索、读取和多次发言不拦截。
- 普通文本工具结果超过 2000 字符时只把前 2000 字符、原文总长度、截断说明和 `result_ref` 交给模型，完整内容写内容寻址文件；压缩器使用同一外置规则但没有 transcript 行。`readToolResult`、`readSessionEntry`、`readDiary`、`readFile` 保留显式读取窗口；`viewImage` 图片本体保留。只有现存 transcript 的引用闭包（`result_ref`、payload 文本内的 `file_ref`/`image_ref` 和 `r` 嵌套内容）是长期引用根，最后一个引用删除后释放；未挂接孤儿至少保留 24 小时再按启动/每日补扫。
- 投信路由在共享入口一次校验：group/private 可以投 life 和彼此的普通消息，但不能派 worker；worker 只能回报 life；life 可以投给任意会话。life 向 worker 派工时可用 `context_mode=new`，只允许 idle 且无待处理任务的 worker；事务内写 `context_start_id` 并清空旧摘要，旧历史仍可回查。
- 生活日程只在事务内推进并投递，提交成功后才写内存；`life_next` 到期且 life 正在睡时机械恢复 awake，主人私聊的精确 `/wake` 是唯一人工机械唤醒，其他内部信不打断睡眠。
- QQ 睡眠是运行许可：提交时在原文落库后跳过工作集与调度；即时调度、防抖、并发入口、周期扫描和空闲压缩都要检查。
- worker 程序故障回报 life；failed/cancelled 不因周期扫描自动重跑，等 life 明确投来新指令。所有 worker 共用唯一账号和 `/home/worker`，只是并行子 Agent；运行态并发跟启用数量热更新，0 通过 active 停用，不能把并发组件的 0 当成禁用。
- 工具中间件只保留 `ToolTranscriptMiddleware`、`ToolErrorMiddleware`、Unknown handler、参数规范和顺序执行；不要恢复全局 ToolAction/ToolDedup 或消费水位。
- 压缩只在一轮 think 完整结束后、最后一条 assistant 的实际用量达到窗口 90% 时触发，不设 token 目标或 55% 硬上限；注入阶段按摘要预算和近期原文比例控制占用。
- 模型、人格、MCP 和运行态使用 generation 快照；模型/人格/MCP 等需要热切换的内容替换 generation，并发数与三种单轮步数在下一个 think 生效且不重启进程。每次 think 只取一次配置快照，构造 runner 和连续子步骤都复用它。切换或缩小模型窗口前用只读容量预览检查受影响场合的持久工作集，放不下就拒绝并保留旧 generation。
- 压缩候选只包含完整、已可见、协议闭合的旧交互段，保护当前批次、活动工具链、未闭合 CallID 和最近尾部。
- 压缩器只能读当前会话并使用 `searchTopics`、`readTopic`、`searchHistory`、`readToolResult` 和 `finishCompaction`；不提供 `readHistory`，不能写记忆、情感、便签或外部系统。
- 压缩失败、覆盖遗漏、预算超限、数据库失败和结果不明都不能删除 transcript；参数不可用按可修正输入错误处理，失败后退避 60 秒；需要压缩却失败时先阻止主模型，冷却后重试压缩，成功才继续。
- provider 返回上下文溢出时允许一次强制压缩后重试主请求；仍失败则保留状态结束本轮。
- 模型请求不设置整体超时，只限制连接和 TLS 握手 10 秒；Agent 关闭时的上下文取消仍然生效。
- 聊天、生活、工作三种单轮步数分别配置，在下一次 think 开始时读取；压缩器步数独立配置。
- 三种单轮步数是该次 think 的总预算；life 自检、连续消费和允许的溢出重试都在剩余预算内，换一个 runner 不重新获得完整步数。
- 容量检查覆盖 QQ 离线、Agent/generation 尚未建立和首次激活；它只能用持久工作集与临时代头预览实际装配，不能写代头、追加群说明、修复工具、Prune 或触发压缩。
- 发言、戳一戳、贴表情、撤回等外部侧效应仍由工具自身和 OneBot API 保证真实成功，不伪造完成状态。
- 撤回只打标记，不改写 transcript 前缀；后续消息附撤回说明。
- 自己发送的消息写入 `message_logs` 但不额外生成 transcript user 行，因为动作已由 assistant/tool 轨迹表达。
- 群聊/私聊持有 ref-only 文件工具，不能读写个人 home；life/worker 保留 home 路径工具并通过 `createFileRef`/`saveFile` 交接。life 成功结束仍无未来安排时，同一 think 只追加一次自检；之后明确不安排就尊重，不再记录反复催促。
- 已确认边界：life 不持有定向 QQ 文件/图片发送，由目标群聊/私聊 Agent 发送到当前会话；聊天与 life 统一使用 `getUserProfile(QQ|self)`；life-only `cancelWorker` 只取消当前回合，不删槽位、历史或文件；worker 补真实 `viewImage`（home 路径或 image_ref）与按需 `readFile`，不新增搜索或执行框架。worker 的 QQ 待取附件通过主服务受限回调解析，不持有 QQ 客户端。
- worker 后台任务统一使用命名 tmux、任务目录、日志和退出码文件，不新增 jobs/PTY/完成通知服务。取消模型回合不等于 tmux 任务停止；共享 home 中的其他任务不得擅自接管、修改或停止。
- worker 重启恢复必须直接使用 `max(context_through_id, context_start_id-1)` 作为当前工作集读取边界，不先加载全部旧轨迹再在内存补剪；旧历史仍可经历史工具在保留期内回读。
- MCP 单个服务的初始化和工具发现设置独立有界期限，超时后关闭该服务并继续其它服务；成功连接的长期生命周期仍绑定 Agent 上下文。模型请求本身不设置整体超时。

- life 整次 think 成功后才清除失败与补试状态；连续消费和无安排自检的中间成功不能补充重试次数。新的明确输入和主动睡眠会结算旧补试，睡眠期间不投递失败补试。生活页只读展示 worker 现有状态和 transcript，不另建任务表。

- QQ/life/worker 的人格装配统一使用五段预设：共同人格与规则加各自职责，当前会话编号由程序提供；不恢复作用域标记解析。worker 摘要和代头在同一事务换代，检查点只存背景摘要，不沿用默认的强制继续任务提示。
