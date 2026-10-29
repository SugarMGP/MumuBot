# AGENTS

本文件约束本仓库内 AI/人工协作时的默认开发行为。

## 项目概览

- MumuBot 是运行在 QQ 群和好友私聊里的智能体：通过 OneBot 11 接收群消息与好友私聊消息，使用 ReAct（观察、思考、行动循环）决定是否回复、调用工具或保持沉默。
- 主链路由 `main.go` 启动：配置加载、模型客户端、记忆系统、聊天 agent、管理后台和退出清理都在这里串起来。
- 语义能力分成几条边界清楚的链路：主 Agent 直接维护长期记忆和具体情感，同步话题压缩保存对话脉络与检查点，后台负责浏览与管理。

## 目录地图

- `internal/agent/`：运行编排层，连接 OneBot、transcript、同步压缩、工具、模型和思考循环。
- `internal/onebot/`：onebot-sdk 接入、OneBot 11 Adapter、消息事件解析和发送 API。
- `internal/memory/`：PostgreSQL 数据模型、pg_trgm 检索、transcript、checkpoint、长期记忆、情感、消息日志和成员画像。
- `internal/migration/`：数据库 schema 版本与一次性数据迁移，只依赖 GORM 和 memory 的模型。
- `internal/tools/`：暴露给主 Agent 和压缩 Agent 的受限工具集。
- `internal/web/`：管理后台 HTTP 服务、页面模板、前端资源和后台业务服务（含会话管理）。
- `config/`：运行配置、群聊人格提示词、私聊提示词和 MCP（模型上下文协议，用于接入外部工具）示例。

## 常用入口

| 任务 | 入口 |
|------|------|
| 看启动顺序 | `main.go` |
| 看消息如何进入 bot | `internal/onebot/client.go`、`internal/agent/message.go` |
| 看回复决策 | `internal/agent/think.go` |
| 看 transcript 与同步压缩 | `internal/agent/session.go`、`internal/agent/compaction.go` |
| 看话题提交与分层检索 | `internal/memory/topic_compaction.go`、`internal/memory/topics_v2.go` |
| 看长期记忆与情感 | `internal/memory/memory_v2.go`、`internal/memory/emotion_v2.go` |
| 看数据库启动迁移 | `internal/migration/`、`main.go` |
| 看私聊收发 | `internal/agent/private.go` |
| 看会话与黑名单 | `internal/memory/conversation_targets.go`、`internal/agent/commit.go` |
| 看后台页面 | `internal/web/views/*.templ`、`internal/web/app/app.go` |

## 运行与验证命令

```bash
bun install --frozen-lockfile
bun run gen:icons
bun run build
templ generate ./internal/web/views
go build ./...
go vet ./...
```

- 只改 Go 逻辑时，至少运行 `go build ./...`；涉及语法或逻辑变更时再运行 `go vet ./...`。
- 修改 `.templ` 后运行 `templ generate ./internal/web/views`。
- 修改 `internal/web/assets/src/` 后运行 `bun run build`。
- 修改 `Dockerfile`、`docker-compose.yml` 或 `.env.example` 后运行 `docker-compose config --quiet`（或等价的 `docker compose config --quiet`）。
- 文档类改动不需要为了形式重跑前端构建，但如果同时已有代码改动，交付时要说明本轮实际验证了什么。

## 产品与文案

- 管理后台中的文案默认是给后台使用者看的，不是给开发者看的。
- 不要在页面、提示、卡片、副标题或系统信息里直接暴露开发阶段说明、实现约束或代码细节。
- 禁止把以下内容直接展示给用户：文件路径、配置文件名、配置键名、构建方式、工作流说明、预览环境说明、是否写库、单文件二进制实现、前端框架名、组件库名、源码术语。
- 后台中的状态、类型、枚举值必须转成可读中文，不要直接显示原始 enum 或内部值。
- 文案应优先描述“用户现在能做什么、看到什么、当前状态如何”，不要描述“开发时如何实现”。

## 前端实现

- 管理后台使用 Go/templ、Tailwind CSS 4、daisyUI、HTMX、Toastify 和 ECharts；少量通用行为使用集中式原生 JavaScript。不要重新引入 Preline、Alpine 或 React/Vue SPA。
- 后台面向 1000px 及以上的横屏窗口，通过流式字号、间距和栅格自适应；不为手机竖屏增加抽屉或独立布局。
- 新增或大改后台页面前必须先用 Stitch 完成设计，再按确认稿实现。允许 daisyUI 控件细节与设计稿略有不同，但页面内容、排版、风格、配色和圆角层级不能明显偏离。
- 页面以纯白或微粉近白为底，暖白侧栏、樱粉主色和青绿状态色为主要视觉语言；整体年轻、Anime Friendly、多圆角但不幼稚，避免大面积粉色背景和板正的传统企业后台风格。
- 优先复用 daisyUI 组件和浏览器原生控件，不重复手搓按钮、输入框、下拉菜单、弹窗、提示、加载态和分页；图表统一使用 ECharts。
- 后台所有图标必须使用 Lucide 官方线性图标：`internal/web/views/icons.templ` 与 `internal/web/assets/src/icons.generated.js` 由 `scripts/generate-icons.mjs` 从 npm 包 `lucide-static` 生成（`bun run gen:icons && templ generate ./internal/web/views`），禁止手工修改这两个文件、手绘路径或引入其他图标库；新增图标先在生成脚本的 mapping 中登记，语义要直观且互不混淆。
- 保持所有页面的间距、字重、圆角、颜色和交互动效统一。
- 所有重要交互都要有明确反馈；动效应控制在轻量范围内，并尊重 `prefers-reduced-motion`。

## 后台设计规范

后台统一设计令牌写入 `internal/web/assets/src/admin.css` 的 `:root`，页面不得再硬编码这些值的近似替代。字号与间距通过 `clamp()` 随横屏窗口宽度平滑缩放：1000px 附近取下限，1600px 及以上取表中数值：

| 类别 | 统一值 | 用法 |
|------|--------|------|
| 页面区块间距 | 24px（`--admin-page-gap`、`space-y-6`） | 页面根节点 |
| 卡片内边距 | 20px（`--admin-card-pad`、`p-5`） | `card-body` |
| 紧凑内边距 | 16px（`--admin-card-pad-compact`、`p-4`） | 筛选、提示等紧凑区块 |
| 卡片圆角 | 18px（`--admin-card-radius`、`rounded-box`） | 所有卡片 |
| 子块/控件圆角 | 12px（`--admin-inner-radius`、`--admin-control-radius`） | 卡片内子块、输入框、按钮 |
| 页面标题 | 28px（`--admin-title-size`） | `PageHeader` |
| 卡片标题 | 18px（`--admin-section-title-size`、`text-lg`） | 卡片与区块标题 |
| 正文/辅助文字 | 14px / 12px（`text-sm` / `text-xs`） | 正文与说明 |
| 次要文字透明度 | 仅 `/75`、`/60`、`/45` 三档 | 正文、说明、弱提示 |
| 边框色 | `var(--color-base-300)` | 卡片统一 1px 实线边框、表格、分隔线 |
| 主色/成功色 | `var(--color-primary)` / `var(--color-secondary)` | 不写颜色字面量 |
| 卡片阴影 | `0 1px 2px rgb(32 33 45 / 0.05), 0 8px 24px rgb(32 33 45 / 0.055)` | 唯一卡片阴影；登录页卡片可在此基础上叠一层品牌色投影 |

- 全局页面底色统一：单层樱粉径向柔光叠加纵向近白渐变（body 级、固定不随滚动），登录页与后台页面共用同一套背景，不允许单页重写底色。
- 表情包列表按当前栅格列数保持整三行：默认每页 12 个，窗口宽度变化导致列数变化时按实际列数调整每页数量。
- 表格行密度统一使用默认 `table`，不再混用紧凑密度变体。
- 弹层标题统一使用 `DialogHeader`，分页统一使用 `ListFooter`，筛选面板统一使用 `FilterPanel`，排序使用 `SortToolbar`，不允许再手写等价结构。
- 图标只能通过 `AdminIcon` 或生成脚本的 JS 图标映射使用，新增图标先登记 mapping。
- 所有 HTMX 导航控件必须保留 `aria-busy`/指针反馈；请求失败必须有可见提示，表单提交必须有 loading 状态。
## 模板与资源

- 后台页面模板位于 `internal/web/views/*.templ`。
- 修改 `.templ` 文件后，必须运行 `templ generate ./internal/web/views`。
- 前端资源源码位于 `internal/web/assets/src/`。
- 修改前端资源后，必须运行 `bun run build`。
- `internal/web/assets/dist/` 是构建产物，不手工修改，不纳入版本控制。

## 更新日志与发布

- 根目录 `CHANGELOG.md` 是 GitHub Release 的发布说明来源。
- 更新日志遵循 Keep a Changelog 1.1.0 风格：保留 `Unreleased` 区块，版本按倒序排列，变更类型使用中文小标题：`新增`、`变更`、`废弃`、`移除`、`修复`、`安全`。
- 每条更新日志使用“**加粗概括**：详细说明”的形式，先说明用户或维护者能感知到的结果，再补充影响范围。
- 只有项目代码、运行配置、用户文档或发布工作流产生了使用者或维护者可感知的变化时，才更新 `CHANGELOG.md`。
- 更新日志面向使用者和维护者，描述影响和结果，不堆砌 git 提交，不写内部实现流水账。
- 调研报告、方案讨论、审查过程、验证记录和 `AGENTS.md` 等协作说明不属于项目变更，不写入 `CHANGELOG.md`。
- 发布新版本前，必须把 `Unreleased` 中待发布的内容移动到 `## [x.y.z] - YYYY-MM-DD` 版本区块；tag 使用 `vx.y.z`，并确保版本区块存在，否则发布流程会失败。
- `main`/Pull Request 的持续集成与 `v*` tag 发布使用独立 workflow：前者只执行单次构建和静态检查，后者才执行完整平台矩阵、容器镜像与 GitHub Release，禁止让同一提交因 main push 和 tag push 重复跑发布矩阵。
- `v*` tag 发布时先构建并推送 `linux/amd64`、`linux/arm64` 镜像到 GitHub Container Registry，再创建 GitHub Release；使用仓库内置 `GITHUB_TOKEN` 和 `packages: write`，不依赖额外仓库密钥。

## 验证

- 涉及后台 UI 的改动，交付前使用 Codex 内置浏览器逐页检查布局、交互、控制台和网络请求；不要使用 Chrome DevTools MCP。
- 页面验证必须包含真实截图，并以截图作为布局与视觉结果的主要验收依据；仅查看 DOM 树、可访问性树、脚本返回值或静态 HTML，不算完成验收。
- 发现页面问题时，优先依据真实截图修正，不靠主观猜测收尾。
- 完整项目因 PostgreSQL、NapCat 或 QQ 环境无法启动时，可以使用真实模板和前端资源配合 mock 数据验收后台；仍需检查页面布局、关键交互、图表、控制台和网络请求，并在结束后清理临时进程与目录。
- 本机缺少 PostgreSQL、NapCat 或 QQ 等真实集成环境时，不把这一既知条件重复当作代码审查阻断；改用构建、静态检查和调用链复核，并在交付时说明未覆盖的真实集成测试。
- 构建、静态检查和浏览器验收串行执行；结束后清理本轮启动的进程和临时目录。

## 修改原则

- 优先做直接、可维护的实现，不保留多余的兼容层或过渡性写法。
- 不要为了抽象而抽象；只有在多个页面稳定复用时再提炼帮助函数或共享结构。
- 优先使用 Go 标准库和现有依赖；不新增单实现接口、generic repository 或只为未来预留的结构。
- 只增强现有功能和效果，不自行扩展新的产品功能。
- 修改代码前必须从相关模块的完整调用链、状态来源和持久化约束出发定位根因，在所有调用方共享的正确位置一次性解决；禁止只针对单个症状反复叠加补丁、兼容分支或重复兜底。
- 不要改动与当前任务无关的脏文件。
- `docs/` 下已有的研究报告默认是只读基线，除非用户明确要求，否则不修改；调研结论、临时分析和面向用户的方案可以在 `docs/` 下新建文件作为交付物。
- 未经用户明确要求，不为 `docs/` 下的调研或方案创建 commit，也不推送远端。
- 未经用户明确要求，不创建 commit、切换分支或推送远端。

## Go 代码结构

- 人工维护的说明性注释统一使用中文，末尾不加句号；保留标识符、协议名、工具指令和许可证声明的原文，不手改生成文件。
- 注释优先解释用途、约束和原因，不重复显而易见的代码；导出符号注释以符号名开头，保留工具之间现有的长分隔注释。
- Go 代码统一使用 gofmt；import 按标准库、项目内包、第三方依赖分组，组内排序。仅整理风格时不修改业务逻辑、接口或提示词。
- 人工维护的 `.go` 文件不超过 600 行；templ 等生成文件不受此限制。超过时按职责拆分，每个拆出的文件应有内聚的职责域，不要零散地把函数分到很多小文件。
- 同一函数内不要重复调用 `config.Get()`，提取为局部变量复用。
- 同一函数内不要重复调用 `strings.TrimSpace` 处理同一个值；如果上层已经 Trim 过，下层不应再 Trim。
- 不要在同一表达式或相邻行中对同一值重复调用（如 `a.bot.GetSelfID()` 在同一条件中出现两次），提取为局部变量。
- 同一函数中多次使用同一缓冲区/切片时，取一次后复用变量，不要重复加锁取数据。
- 仓库不保留独立回归测试文件；未经用户明确要求，不新增测试。所有代码改动必须通过 `go build ./...`，涉及语法或逻辑变更时额外运行 `go vet ./...`。
- JSON 解析和序列化优先复用项目已有的 Sonic；`json.Number`、`json.RawMessage` 等标准库边界类型可保留。

## 死代码

- 发现死代码（永远不会执行的分支、赋值后立即被覆盖的变量、从未被读取的字段）应立即删除，不要保留"以后可能用到"的无用逻辑。
- 删除死代码时同步清理关联的 import，避免 `imported and not used` 编译错误。

## 生成文件

- `internal/web/views/*_templ.go` 由 templ 生成，禁止手动修改；需要改页面结构时修改对应的 `.templ` 文件并运行 `templ generate`。
- `internal/web/assets/dist/` 是前端构建产物，不手工修改，不纳入版本控制。
- `internal/web/views/icons.templ` 与 `internal/web/assets/src/icons.generated.js` 由 `scripts/generate-icons.mjs` 生成，同样不纳入版本控制；首次构建前先运行 `bun run gen:icons`。

## 核心架构约束

- 主聊天和同步话题压缩共用唯一 high 模型；不再保留 low、Embedding、后台 Memory Agent、`learning` 包或向量检索，也不为它们补兼容路径。
- 对话模型只走 OpenAI Responses 端点（`/responses`）；`model_tiers.high.context_window` 用于输入预算和压缩触发，设置 `max_output_tokens` 或固定输出预留不是本项目行为。
- 回复决策保持单次 ReAct 运行，不拆 Planner/Replyer，不在主 ReAct 前新增模型分类；进入 ReAct 前由发言门控放行，进入后由 `stayQuiet` 或其他工具形成唯一行动结果。
- 发言门控分两层：v1 按消息内容算意愿分（被提及、问句、纯媒体、自身近期发言占比、距上次发言时长、待处理条数，权重是写死常量），v2 用每会话 1 小时窗口把目标频率换算成放行阈值；直接提及强制放行，私聊不设门控，最终是否发言仍由模型决定。
- 强提及、点名和回复机器人在群内没有 think 运行时必须进入 ReAct；每群保持单任务串行，运行期间的新触发不创建 rerun，消息保留给后续正常调度。
- 戳一戳只作为普通观察事件，不能成为回应理由；它没有稳定消息 ID，正文以 `[观察]` 渲染，不能借用其他消息 ID 回应。
- 每个会话维护 `conversation_sessions` + `conversation_transcript` 持续工作集；真人消息各自是 user 行，助手与工具轨迹原样保存，压缩成功后只删除被 checkpoint 覆盖的 transcript 行。
- `m1`、`m2` 是 `message_logs.message_ref` 上的稳定会话编号，压缩删除 transcript 后仍然存在；`h12345` 是全局内部消息 ID，只用于历史读取，不能当作当前会话可回复编号。
- `visible_seq` 表示模型请求成功时实际纳入的输入；新输入初始 `visible_seq=NULL`，请求失败不回滚、不自动重试，下一次由新消息触发。
- `visible_seq` 必须按模型请求中的真实消息顺序分配：摘要/内部参考在前、待处理输入在后；不要按数据库主键重新排序。
- 上下文顺序固定为：稳定系统指令和工具 → checkpoint 概览 → active 话题摘要 → `reference_baseline` → checkpoint 后 transcript → 新内部参考 → 新真人消息 → assistant/tool。
- 消息正文进入上下文后不再变化：首次渲染写入 transcript，时间固定为 `YYYY-MM-DD HH:MM:SS`；当前时间、群信息和情感只由 `getCurrentStatus` 查询。
- 工作便签已移除；跨轮临时状态由连续 transcript、checkpoint overview 和话题 open_loops 承担，不保留 `saveWorkingNote`、状态查询字段或后台入口。
- 模型看到的正文就是展示正文，@、QQ 表情、语音、文件、卡片和媒体短哈希都保留；QQ 自带表情写成 `%qface:名称%`，不能退回纯文本或借用其他内部标记。
- 每轮原样回传 reasoning、工具调用与全部工具结果，包括 `viewImage` 图片；transcript 保存同一 `schema.AgenticMessage` 编码，重启后按 payload 恢复，禁止重新渲染已入库的消息正文。
- 主上下文装配不静默截断 transcript；预算达到窗口 90% 时触发一次完整同步压缩，目标 40%，55% 为可接受上限，超过 55% 不提交。
- 压缩最多两次模型尝试，共享 12 步和 300 秒；只能用当前会话的 `searchTopics`、`readTopic`、`searchHistory`、`readHistory`、`readToolResult` 和一次 `finishCompaction`。
- 压缩只整理冻结的完整旧交互段，保护当前触发批次、活动工具链、未闭合 CallID 和近期尾部；提交事务原子写入话题、归属、来源、概览、检查点并删除覆盖 transcript。
- 压缩失败、参数遗漏、预算超限、数据库失败或提交结果不明时保留旧 transcript；失败后至少退避 60 秒，不能让模型带着超限上下文继续请求。
- `conversation_tool_calls` 不存在；工具调用和结果直接是 `entry_kind=tool` 的 transcript 行。`run_id + call_id` 幂等，同 CallID 复用已有回执，不同 CallID 的重复搜索或多次发言不拦截。
- 大工具结果超过约 4000 token 时，完整文本保存为内容寻址文件，模型只看到约 1000 token 预览和 `result_ref`；孤立文件至少保留 24 小时后再清理。
- 工具中间件只保留协议层：`ToolErrorMiddleware`、`UnknownToolsHandler`、参数规范化、顺序执行、`ReturnDirectly` 和终端错误；不要恢复全局 ToolAction/ToolDedup 或消费水位。
- `conversation_targets.active/blocked` 是会话可用性事实源；启动、重连和每 5 分钟兜底同步，允许真实空列表。机器人退群/被踢和好友建立分别由事件即时纠正，好友删除靠兜底同步。
- 事件直接并发分发；每个会话按到达顺序分配单调 `ArrivalSeq`，主动发言也进入同一序列。提交重排只使用 `ArrivalSeq`，OneBot `message_id` 只做不透明等值键，不能排序或当水位。
- 解析失败、无效事件、未启用或拉黑会话通过跳过项消费序号，保证重排器不死等；提交等待队列必须有界，越过的序号不留下永久缺口。
- 媒体在事件回调内同步下载、转码与落库；普通图片摆正并转 webp，表情包保留原件；重复内容按 md5 复用，失败保留文字占位，不伪造识别结果。
- 机器人 QQ 只以 OneBot Adapter 运行时 `self_id` 为准；配置和静态人格提示词不保存 QQ。自身消息写入 `message_logs`，但不额外生成 transcript user 行。
- 一条真人消息经 `message_logs` 永久保存原文与展示正文；`message_forwards` 按句柄保存每一份转发，嵌套转发独立成行，重启后仍可逐层读取。
- 撤回只写 `recalled_at` 并同步会话标记；不得清空原文、摘要来源、情感或长期记忆，后续用户消息可附撤回说明，不改写已进入上下文的前缀。
- 停机时不清零运行时账号；提交队列排空期间仍按真实账号识别自身消息。
- 思考从会话起始到达序号开始；运行期间新消息由 `BeforeModelRewriteState` 注入，`getNewMessages` 重复调用不重复返回同一批。
- 消息时间固定、不注入“最近在场的人”；群聊自动参考最多选当前批次发送者、提及和回复目标 3 人。
- 长期记忆由主 Agent 通过 `searchMemory`、`readMemory`、`manageMemory` 维护；类型只允许 fact/preference/constraint/goal/term/expression/alias，更新和归档必须基于已读取版本。
- `knowledge_items` 用 `subject_user_id` 表示主体（0 群组、运行时 self 自身、正数人物），模型输入 `-1` 必须在工具边界解析；`conversation_kind + target_id` 只是来源，不是检索隔离。
- 知识正文语义变化新增条目；同主体、同类型、同内容哈希精确去重，重复创建不复活归档条目。
- `manageMemory` 一批 1–8 项全成功或全回滚；主 Agent 不物理删除，后台只提供启用、归档和查看修订。
- 具体情感固定 12 类；同一对象同一情感只保留一份当前状态，变化写入 `emotion_revisions`。强度只接收轻微/明显/强烈/已平复，按真实时间惰性衰减，不做定时写库或独立情感判断模型。
- 精力与交流意愿保留辅助状态；好感度继续保持全局 `user_id` 维度，不按群隔离，也不由情感状态替代。
- 自动内部参考预算固定约 1200 token：核心记忆 800、明确约定 200、情感与总体状态 200；完整条目原子选取，无内容不凑满。
- 内部参考是独立 user 类型 `internal_context`，不计真人消息、不触发门控；只在版本、语义或强度档位变化时追加，压缩写入新的 `reference_baseline`，不回头修改旧 transcript。
- 话题系统承担三项职责：压缩后的会话表示、后台浏览管理单位、关键词 → 话题概览 → 关键消息 → 原文前后文的分层查询。
- 话题只有 active/dormant，统一群聊和私聊作用域；摘要不可变且按版本保存，来源分 source/anchor，撤回不改写、不使话题失效。
- `context_compaction_checkpoints.active_topic_versions` 保持稳定顺序；dormant 话题不进入当前摘要块但始终可搜索。退出 active 仍有 open loop 时，程序必须把未完成事项补进 overview。
- `overview` 是会话级滚动概览，不是新 Agent；压缩器必须写结论、不确定性、纠正、边界、动作结果和未完成事项，程序不静默截断摘要。
- 首次从旧版升级接续旧摘要与预算内近期原文，不回填伪造的旧 assistant/tool 轨迹；旧证据、关系、学习水位、私聊摘要和旧心情原表保留为 `legacy_*`。
- 跨会话检索允许用于记忆、话题和历史，结果必须带群聊/好友来源；不建立 ACL、visibility 或敏感等级表，披露约束写在系统提示与工具说明中。
- 系统提示只做增量调整：群聊与私聊分别维护 `config/persona_group.prompt` 和 `config/persona_private.prompt`，不复用同一模板，也不恢复已删除的成组矫正案例。

## 数据与检索边界

- `ConversationMessage.Content`、`MessageLog.TextContent` 保存原始文本；`FinalContent`、`MessageLog.DisplayContent` 只用于展示给 bot 的聊天上下文，历史工具可同时返回两者以保留媒体占位。
- 禁止用展示文本替代原始文本做知识主体判断；原始文本为空时，语义记忆提取跳过，但话题压缩仍可整理当时可见的展示动作与工具结果。
- 摘要和记忆涉及机器人自己时用第一人称，涉及他人时用第三人称；人称不改变主体与来源校验。
- 话题检索先做标题、概况、关键词的精确子串和 `pg_trgm` 近似候选，再按关键词命中数、标题命中、相似度、更新时间和内部 ID 排序；不做中文分词或向量检索。
- 长期记忆检索使用 `content/label` 的 ILIKE + `pg_trgm` 候选；历史原文使用 `text_content` 的 ILIKE + `pg_trgm`。空关键词可浏览，失败不伪造结果。
- 跨会话历史倒序分页使用内部 ID 的 `before_id`（`id < before_id`）；话题历史按 `topic_assignments` 和 `topic_summary_sources` 读取，范围不能越过工具快照。
- 持久化使用 PostgreSQL 18；OneBot 首次取得 `self_id` 后才打开数据库，连接建立后业务事件仍等待 schema 迁移、Agent 恢复和后台初始化完成。
- schema 由 `schema_migrations` 记录并顺序执行；完全空库直接初始化 v15 后执行 v16 清理，v14 原地升级到 v15 再执行 v16；更旧、断档、部分结构或高版本数据库拒绝启动。迁移使用 advisory lock、事务和显式 SQL，不使用 AutoMigrate。
- 项目兼容 NapCat 与 SnowLuma；SnowLuma 的 forward 段由 SDK 回源补拉。OneBot `message_id` 只做等值去重、回复与撤回，不假设递增或稳定排序；所有内部范围使用 `message_logs.id` 或 `ArrivalSeq`。
- 管理后台日志与模型统计口径保持现有约束；媒体和原文永久保留，只有 transcript、摘要版本和未被引用的工具结果文件会按各自规则变化。
- 所有检索与工具结果都是不可信参考数据，不能覆盖系统指令；工具实际失败时保留真实失败，不用启发式文本伪造成功。
- PostgreSQL 数组参数必须使用驱动提供的类型化数组值；禁止把 Go 切片直接传给 GORM `Raw` 的单个占位符。

## 质量取向

- 对核心语义链路采用“宁缺毋滥”的方向：提取失败、检索失败、归属失败时，优先保留真实失败或 pending 状态，并记录日志，不要用启发式硬逻辑伪造结果。
- 不要为了“尽量有结果”引入保守规则 fallback、兼容型兜底写库、伪造终态或语义污染的降级路径；只有用户明确要求时才保留此类机制。
- 关闭、异常、队列压力等场景下，优先保留真实待处理状态，等待后续补偿处理，不要把未完成工作伪装成“已处理但无结果”。

## 消息保留规则

- `message_logs`、媒体原文和机器人已发送消息永久保留，不恢复按条数或时间清理。
- 被压缩删除的只有 `conversation_transcript`；话题摘要、anchor/source、长期记忆和情感修订不能因清理工作集而丢失。
