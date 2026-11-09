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
- `internal/web/`：管理后台 JSON API、内嵌前端产物和后台业务服务（含会话管理）。
- `web/`：管理后台 React 前端工程（Vite + TypeScript + Tailwind 4 + shadcn/ui）。
- `internal/config/`：env 启动配置、代码内置默认值、PostgreSQL 动态配置快照和加密配置。
- `internal/refstore/`、`internal/filemeta/`：不可变 `file_ref`/`image_ref` 快照、元数据和引用回收。
- `internal/workerexec/`、`internal/workerproto/`：worker 私网执行、受限文件读写和主服务字节流协议。
- `internal/schedule/`：生活时间表达式、状态切换和到期计算。
- 管理后台“人格预设”页维护人格、共同规则、QQ 聊天、生活职责、工作职责，以及全局角色名与主人；“设置”页维护对话、压缩、并发、媒体、模型、MCP 服务器，以及记忆与历史保留策略。

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
| 看生活主线与时间投递 | `internal/agent/life.go`、`internal/agent/life_scheduler.go`、`internal/memory/life.go`、`internal/memory/schedule.go` |
| 看投信路由与唤醒 | `internal/agent/session_messages.go`、`internal/tools/session_tools.go` |
| 看会话与黑名单 | `internal/memory/conversation_targets.go`、`internal/agent/commit.go` |
| 看后台页面 | `web/src/pages/`、`web/src/app/router.tsx` |
| 看后台接口 | `internal/web/app/app.go`、`internal/web/app/dto.go` |

## 运行与验证命令

```bash
cd web && bun install --frozen-lockfile
cd web && bun run typecheck
cd web && bun run lint
cd web && bun run lint:i18n
cd web && bun run build
go build ./...
go vet ./...
```

- 只改 Go 逻辑时，至少运行 `go build ./...`；涉及语法或逻辑变更时再运行 `go vet ./...`。
- 修改 `web/` 后运行 `bun run typecheck`、`bun run lint`、`bun run lint:i18n` 和 `bun run build`。
- 修改 `Dockerfile`、`docker-compose.yml` 或 `.env.example` 后运行 `docker-compose config --quiet`（或等价的 `docker compose config --quiet`）。
- 文档类改动不需要为了形式重跑前端构建，但如果同时已有代码改动，交付时要说明本轮实际验证了什么。

## 产品与文案

- 管理后台中的文案默认是给后台使用者看的，不是给开发者看的。
- 不要在页面、提示、卡片、副标题或系统信息里直接暴露开发阶段说明、实现约束或代码细节。
- 禁止把以下内容直接展示给用户：文件路径、配置文件名、配置键名、构建方式、工作流说明、预览环境说明、是否写库、单文件二进制实现、前端框架名、组件库名、源码术语。
- 后台中的状态、类型、枚举值必须转成可读中文，不要直接显示原始 enum 或内部值。
- 文案应优先描述“用户现在能做什么、看到什么、当前状态如何”，不要描述“开发时如何实现”。

## 前端实现

- 管理后台使用 React 19 + TypeScript + Vite + Tailwind CSS 4 + shadcn/ui（Radix）+ TanStack Query + ECharts + sonner + i18next；不要重新引入 templ、HTMX、daisyUI、Toastify 或服务端渲染页面。
- 前端工程位于 `web/`，产物输出到 `internal/web/assets/dist/` 并由 Go 内嵌；发布仍是单二进制。
- 后台面向 1000px 及以上的横屏窗口，通过流式字号、间距和栅格自适应；不为手机竖屏增加抽屉或独立布局。
- 新增或大改后台页面前必须先用 Stitch 完成设计，再按确认稿实现；允许组件在 hover、focus、过渡等细节上采用组件库默认表现，但页面内容、排版、风格、配色和圆角层级不能明显偏离。
- 视觉基线：浅色为纯白或微粉近白工作区、暖白侧栏、樱粉主色、青绿状态色；深色为深炭底、同一品牌色与圆角层级。两套主题共用同一套语义令牌。
- 优先复用 `web/src/components/ui` 与 `web/src/components/admin` 的组件和浏览器原生控件；图表统一使用 ECharts。
- 图标统一使用 `lucide-react`，在 `web/src/components/admin/icons.tsx` 登记语义名，禁止手绘路径或引入其他图标库。
- 所有界面文案只能写在 `web/src/locales/zh-CN.ts` 与 `web/src/locales/en-US.ts`；组件里通过 `t()` 使用，禁止硬编码中文（`bun run lint:i18n` 会检查）。用户数据、日志内容和存量正文不翻译。
- Go 侧只返回错误码与结构化数据，不在服务端拼用户可见中文句子；文案由前端目录提供，未知错误码才回退服务端 message。
- 两种主题与两种语言都必须可用：`mumu.theme` 与 `mumu.locale` 存浏览器本地，默认跟随系统和浏览器语言；首屏由 `web/index.html` 的内联脚本应用，避免主题闪烁。
- 保留组件库自带的 hover、focus、active、过渡、阴影、骨架屏和弹层动画；不要为了复刻旧界面关闭或改丑。
- 保持所有页面的间距、字重、圆角、颜色和交互动效统一。
- 所有重要交互都要有明确反馈；动效应控制在轻量范围内，并尊重 `prefers-reduced-motion`。

## 后台设计规范

后台视觉规范以 `web/DESIGN.md` 为准；页面先按场景选择标准/紧凑/瓦片/长文/控制台密度，再选组件。筛选面板默认不重复“筛选”标题；卡片标题、正文、说明、标签、表格和指标数字按规范角色使用，不临时调字号或用 `p-6`、`space-y-5` 这类半档值。

后台统一设计令牌写入 `web/src/styles.css` 的 `:root` 与 `.dark`，页面不得再硬编码这些值的近似替代。字号与间距通过 `clamp()` 随横屏窗口宽度平滑缩放：1000px 附近取下限，1600px 及以上取表中数值：

| 类别 | 统一值 | 用法 |
|------|--------|------|
| 页面区块间距 | 24px（`--admin-page-gap`、`space-y-6`） | 页面根节点 |
| 卡片内边距 | 20px（`--admin-card-pad`、`p-5`） | 卡片内容区 |
| 紧凑内边距 | 16px（`--admin-card-pad-compact`、`p-4`） | 筛选、提示等紧凑区块 |
| 卡片圆角 | 18px（`--admin-card-radius`、`rounded-xl`） | 所有卡片 |
| 子块/控件圆角 | 12px（`--admin-inner-radius`、`--admin-control-radius`） | 卡片内子块、输入框、按钮 |
| 页面标题 | 28px（`--admin-title-size`） | `PageHeader` |
| 卡片标题 | 18px（`--admin-section-title-size`、`text-lg`） | 卡片与区块标题 |
| 正文/辅助文字 | 14px / 12px（`text-sm` / `text-xs`） | 正文与说明 |
| 次要文字透明度 | 仅 `/75`、`/60`、`/45` 三档 | 正文、说明、弱提示 |
| 边框色 | `var(--border)` | 卡片统一 1px 实线边框、表格、分隔线 |
| 主色/成功色 | `var(--primary)` / `var(--secondary)` | 不写颜色字面量 |
| 卡片阴影 | `var(--card-shadow)` | 唯一卡片阴影；登录页卡片可在此基础上叠一层品牌色投影 |

- 全局页面底色统一：浅色为单层樱粉径向柔光叠加纵向近白渐变，深色为暗樱粉柔光叠加深炭纵向渐变；body 级固定不随滚动，登录页与后台共用，不允许单页重写底色。
- 浅色与深色共用同一套语义令牌；新增颜色、圆角或阴影先加令牌，不在页面写近似值。
- 表情包列表按当前栅格列数保持整三行：默认每页 12 个，窗口宽度变化导致列数变化时按实际列数调整每页数量。
- 表格行密度统一使用默认 `table`，不再混用紧凑密度变体。
- 弹层标题统一使用 `DialogHeader`，分页统一使用 `Pagination`，筛选面板统一使用 `FilterPanel`，排序使用 `SortToolbar`，不允许再手写等价结构。
- 图标只能通过 `AdminIcon` 使用，新增图标先在 `icons.tsx` 登记语义名。
- 所有写操作必须有 loading 与可见错误提示；键盘可达、焦点管理、`aria-label` 与 `prefers-reduced-motion` 不可省略。
## 前端工程与资源

- 前端源码位于 `web/src/`，页面在 `web/src/pages/`，通用组件在 `web/src/components/`，文案目录在 `web/src/locales/`。
- 修改前端后运行 `cd web && bun run typecheck && bun run lint && bun run lint:i18n && bun run build`。
- 本地开发：`go run .` 配合 `cd web && bun run dev`；纯前端联调使用 `bun run dev:mock`（MSW 内存数据）。
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

- 涉及后台 UI 的改动，交付前使用 Codex 内置浏览器或等价的真实浏览器（如 Playwright + 本机 Chrome）逐页检查布局、交互、控制台和网络请求；不要使用 Chrome DevTools MCP。
- 页面验证必须包含真实截图，并以截图作为布局与视觉结果的主要验收依据；仅查看 DOM 树、可访问性树、脚本返回值或静态 HTML，不算完成验收。
- 后台验收必须覆盖浅色/深色两种主题与中文/英文两种语言，至少逐页检查浅色中文，并对登录、首页、设置、日志、图表和详情页做深色与英文抽查。
- 发现页面问题时，优先依据真实截图修正，不靠主观猜测收尾。
- 完整项目因 PostgreSQL、NapCat 或 QQ 环境无法启动时，可以使用真实前端资源配合 MSW mock 数据验收后台；仍需检查页面布局、关键交互、图表、控制台和网络请求，并在结束后清理临时进程与目录。
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
- 人工维护的 `.go` 文件不超过 600 行；生成文件不受此限制。超过时按职责拆分，每个拆出的文件应有内聚的职责域，不要零散地把函数分到很多小文件。
- 同一函数内不要重复调用 `config.Snapshot()`，提取为局部变量复用。
- 同一函数内不要重复调用 `strings.TrimSpace` 处理同一个值；如果上层已经 Trim 过，下层不应再 Trim。
- 不要在同一表达式或相邻行中对同一值重复调用（如 `a.bot.GetSelfID()` 在同一条件中出现两次），提取为局部变量。
- 同一函数中多次使用同一缓冲区/切片时，取一次后复用变量，不要重复加锁取数据。
- 仓库不保留独立回归测试文件；未经用户明确要求，不新增测试。所有代码改动必须通过 `go build ./...`，涉及语法或逻辑变更时额外运行 `go vet ./...`。
- JSON 解析和序列化优先复用项目已有的 Sonic；`json.Number`、`json.RawMessage` 等标准库边界类型可保留。

## 死代码

- 发现死代码（永远不会执行的分支、赋值后立即被覆盖的变量、从未被读取的字段）应立即删除，不要保留"以后可能用到"的无用逻辑。
- 删除死代码时同步清理关联的 import，避免 `imported and not used` 编译错误。

## 生成文件

- `internal/web/assets/dist/` 是前端构建产物，不手工修改，不纳入版本控制。
- `web/public/mockServiceWorker.js` 是 MSW 的静态 worker，随依赖版本一起提交；升级 `msw` 后运行 `bun run msw:init` 更新。

## 核心架构约束

- QQ 聊天和同步话题压缩共用 QQ 档案；life 和 worker 分别选择自己的档案并用于运行与摘要，可添加同名模型不同推理强度的档案，但不保留 low、Embedding、后台 Memory Agent、`learning` 包或向量检索。
- 对话模型只走 OpenAI Responses 端点（`/responses`）；当前模型的上下文窗口用于输入预算和压缩触发，设置 `max_output_tokens` 或固定输出预留不是本项目行为。
- 模型档案只开放 `reasoning.effort`，支持省略或 none/low/medium/high/xhigh/max；API Key 使用 AES-256-GCM 加密落库，主密钥只放 `MUMU_SECRET_KEY`。
- 回复决策保持单次 ReAct 运行，不拆 Planner/Replyer，不在主 ReAct 前新增模型分类；进入 ReAct 前由发言门控放行，进入后由 `stayQuiet` 或其他工具形成唯一行动结果。
- 发言门控分两层：v1 按消息内容算意愿分（被提及、问句、纯媒体、自身近期发言占比、距上次发言时长、待处理条数，权重是写死常量），v2 用每会话 1 小时窗口把目标频率换算成放行阈值；直接提及强制放行，私聊不设门控，最终是否发言仍由模型决定。
- 强提及和回复机器人在群内没有 think 运行时必须进入 ReAct；每群保持单任务串行，运行期间的新触发不创建 rerun，消息保留给后续正常调度。
- 戳一戳只作为普通观察事件，不能成为回应理由；它没有稳定消息 ID，正文以 `[观察]` 渲染，不能借用其他消息 ID 回应。
- 每个会话维护 `conversation_sessions` + `conversation_transcript` 持续工作集；真人消息各自是 user 行，助手与工具轨迹原样保存，压缩成功后只删除被 checkpoint 覆盖的 transcript 行。
- 会话间消息只写目标 `conversation_transcript` 的 `entry_kind=agent_message` 行，行号就是内部消息编号；来源会话、投递时间和 wake 只在 payload，不再保留 `session_messages` 重复正文表。`listSessions` 按当前角色返回可见会话的元数据与真实概况，`sendSessionMessage` 只允许 active 且未 blocked 的目标，不自动回复。
- 唯一生活主线固定为 `life:1`（kind/name 都是 life），单并发 ReAct，不占 worker 名额，和聊天共用当前模型、人格通用内容、长期记忆与情感；它负责生活安排、跨会话判断和统一派发 worker，不审阅每条群消息。
- 投信按固定角色图：group/private 可以投 life 和彼此的普通消息，但不能直接派 worker；worker 只能回报 life；life 可以投给任意会话。`wake=false` 的静默信只保存，不能唤醒目标，也不能借强交互绕过群发言门控。
- 生活时间只由 life 决定：`planNext` 更新固定槽位 `life_next`，`manageSchedule` 保存独立约定，`sleepUntil` 保存睡眠与起床安排；程序按 Asia/Shanghai 计算到期并只做投递与状态转换，不内置起床、睡前或日记时间。
- life 补试次数、时点和耗尽状态直接定义在 v17；整次 think 成功、新的明确输入或主动睡眠时结算，不在连续消费或自检的中间成功处重置次数。一次性日程只有到期投递事务成功才删除；过期停用项清理，投递失败的启用项保留。
- 日记是独立 `diaries` 表（日期主键、Markdown、首次/最后保存时间），不建记忆、话题、消息关联；life 用 `writeDiary` 写入，life 和群聊/私聊都可以用 `readDiary`/`searchDiary` 只读，后台只读月历。归属日由 life 显式选择：午睡不写，凌晨可写前一天，漏写可补，保存失败不写成成功，日记不自动产生日程或记忆。
- life 不能套用群/私聊的话题压缩：它使用摘要检查点（`life_state.context_summary + context_through_id`）+ 检查点后的 transcript；已被有效摘要覆盖且超过历史期限的可见旧轨迹按 `history_days` 清理，当前代头、检查点后的工作集和未完成工具链继续保留。代头每次换代按当前人格与 self 置顶重建，检查点里不保存代头。
- 睡眠是 QQ 的运行许可，不是 active/blocked：睡眠期间保留 OneBot 接收、媒体、转发、撤回和成员资料，只跳过工作集与调度；已经开始的回合和已接受的 worker 任务可以收尾，普通内部信不打断睡眠。机械唤醒只来自 life 的到期时间、显式后台/`wakeUp` 操作或主人私聊中的精确 `/wake`；不做关键词启发式判断。真实醒来时给 life 一条睡眠期间活跃会话的真人消息概况（数量、最后时间和会话 ID），不自动唤醒这些会话、不建未读表。
- 扩展 MCP 工具只挂给 life，聊天会话不持有生活账号/浏览器能力；worker 只有执行工具。单个 MCP 服务的初始化、工具发现、连接或装载失败只让该服务不可用，不阻断其它服务和整代生成；初始化和发现必须有界，不能把“模型请求不设整体超时”误解成扩展握手可以永久等待。
- QQ 空间、账号资料设置和账号级社交动作只挂给 life；群聊/私聊 Agent 负责在本会话表达并发送 `file_ref`/`image_ref`。按已确认的最新边界，life 不持有定向 `sendFile`/`sendImage`，它把产物 ref 和用途投给目标会话；life 自己仍可 `viewImage(path|image_ref)`、`readFile(path|file_ref)` 验收材料。worker 使用真实 `/home/worker` 路径并通过 `createFileRef`/`saveFile` 交接，补齐全员可复用的真实 `viewImage`；上传头像和空间配图只接 image ref。
- 个人目录是 worker 唯一 `/home/worker`，Mumu 主服务不挂该目录。文件上传通过 worker 私网 HTTP 原始字节流读取，主服务内部按受支持位图内容建立 typed ref；worker 不接触 QQ 凭据。跨 worker session 共享同一个执行账号、home、工具与缓存，但 transcript、检查点、上下文和运行状态独立。
- QQ 文件消息的 `file_id`、原名、大小、`file_hash`、`busid` 和待取 `file_ref` 保存在 `message_logs.file_refs`，临时下载 URL 不做长期定位根；首次读取/发送/存入 home 时才真实下载并固定快照，pending 必须有明确的待取/已固定状态，固定时核对已知声明大小。保存到个人目录的真实文件在 transcript 清理后仍保留，后台不新增文件管理页。`file_ref`/`image_ref` 是 MediaDir 下不可变快照，不建小表、按字节 SHA-256 去重；只有现存 transcript 里 `user/observation/agent_message/assistant/tool` 行的 `result_ref`、payload 文本和 `r` 嵌套引用是长期根；代头、摘要、系统提醒、记忆和日记里的裸 ref 不保活。被删 transcript 的本批候选在提交后按闭包即时释放，从未挂接的孤儿至少保留 24 小时后由启动/每日补扫回收。
- 所有 worker session 共用唯一执行账号和 `/home/worker`，只是并行子 Agent；session 的 transcript、检查点、标题、运行状态和进程组仍独立，不共享对话上下文。`worker_sessions.context_start_id` 记录显式新上下文起点，life 只能向 idle 且无待处理任务的 worker 发送 `context_mode=new`，起点之前的旧历史仍可回查、到保留期后清理。安装的工具、Nix profile 和浏览器缓存复用；宿主目录直接使用 `./workspace`，不保留旧路径兼容或自动搬迁。
- Agent-facing 消息只使用全局唯一的 `one_bot_message_id`；不维护 `message_ref`、`next_message_ref`、`m` 或 `h` 自定义编号。消息 ID 不保证有序，聊天顺序继续使用 `ArrivalSeq`。
- worker-exec 以 worker 身份启动，home 文件操作不依赖逐请求账号切换；sudo 子命令的取消使用参数数组的进程组 kill，不能通过 shell 拼接命令。
- `visible_seq` 表示模型请求成功时实际纳入的输入；新输入初始 `visible_seq=NULL`，请求失败不回滚；聊天失败批次等新输入，life 的所有运行失败最多补试两次，每次间隔五分钟。
- 新生成的 assistant/tool 轨迹即使持久化成功也尚无 `visible_seq`；只有它后来真正进入另一个成功模型请求时才推进。不能把“已经产生轨迹”和“已经作为输入被模型读过”混成同一状态。
- `visible_seq` 必须按模型请求中的真实消息顺序分配：代头在前、待处理输入在后；不要按数据库主键重新排序。
- 消息写入 transcript 后必须把 `transcript_id` 标回同一内存消息；真实输入从 `state.Messages` 收集，不另维护侧面猜测列表。
- 首次请求失败时的批次边界必须来自最后一次实际发出请求的 state；think 入口的 pending 快照只描述入口，不是请求事实。
- life/worker 摘要与 visible、检查点（life 还含代头替换）在同一事务提交；life 摘要保存在 `life_state.context_summary`，检查点不保存代头。摘要正文不是 file/ref 生命周期根，提示词必须说明 ref 可能随 transcript 根删除。
- `sleepUntil` 只有在睡眠、起床安排和工具回执都成功后收尾当前回合；成功终结不能要求再消耗一次模型步数，失败仍保留真实失败并让模型在剩余预算内修正。
- 聊天、life、worker 的一次 think 各取一份配置快照；连续消费、life 自检和允许的溢出重试共用同一剩余模型步数，换 runner 不补充预算。压缩器保留独立步数。
- 上下文顺序固定为：`system_prompt` 行（人格） → `group_note` 行（群说明） → `context_digest` 行（checkpoint 概览与话题摘要） → `pinned_memory` 行（当前主体与自己的置顶记忆） → checkpoint 后 transcript → 新真人消息 → assistant/tool。
- 人格、群说明、摘要和置顶记忆都是 `conversation_transcript` 里的行，每代第一条消息前写入一次，之后只追加不修改；只有压缩换代才重写这些代头。预设切换因此只在换代后生效，不得在轮次之间替换 system。
- 代头按 kind 优先级排在最前（`visible_seq` 查询与内存装配两处都要），不能依赖插入顺序；压缩提交时在同一事务里删除旧代头，避免出现两份人格。
- 预设使用五段独立文本，不使用段落作用域标记或正文过滤。人格和共同规则进入 QQ/life/worker，QQ 聊天只进群聊/私聊，生活职责只进 life，工作职责只进 worker；普通方括号按原文保留。
- 系统注入的提示行统一以 `[系统消息：…]` 开头，群说明、说明变更、说明取消、消息撤回属于要求，上下文摘要与长期记忆属于背景，两者在提示词里必须分开描述。
- 消息正文进入上下文后不再变化：首次渲染写入 transcript，时间固定为 `YYYY-MM-DD HH:MM:SS`；当前时间、群信息和情感只由 `getCurrentStatus` 查询。
- 工作便签已移除；跨轮临时状态由连续 transcript、checkpoint overview 和话题 open_loops 承担，不保留 `saveWorkingNote`、状态查询字段或后台入口。
- 模型看到的正文就是展示正文，@、QQ 表情、语音、文件、卡片和媒体短哈希都保留；QQ 自带表情写成 `%qface:名称%`，不能退回纯文本或借用其他内部标记。
- 每轮原样回传 reasoning、工具调用与全部工具结果，包括 `viewImage` 图片；transcript 保存同一 `schema.AgenticMessage` 编码，重启后按 payload 恢复，禁止重新渲染已入库的消息正文。
- 主上下文装配不静默截断 transcript；一轮 think 完整结束后，最后一条 assistant 的实际用量达到窗口 90% 时触发一次完整同步压缩，压缩阶段不设 token 目标或 55% 硬上限；注入阶段按摘要预算和近期原文比例控制占用。
- 压缩器步数独立配置（默认 12 步），不设置整体模型超时；只能用当前会话的 `searchTopics`、`readTopic`、`searchHistory`、`readToolResult` 和一次 `finishCompaction`；不提供 `readHistory`。
- 注入预算默认摘要 30%（含 overview、置顶记忆和话题）+ 最近原文 10%；摘要话题按 `active_topic_refs` 从尾部裁剪，固定部分超预算时保留并记录日志。
- 压缩只整理冻结的完整旧交互段，保护当前触发批次、活动工具链、未闭合 CallID 和近期尾部；提交事务原子写入话题、归属、`is_anchor` 来源、概览、检查点并删除覆盖 transcript；未分配超过 16 条或 20% 时只允许一次修正。
- 压缩失败、参数遗漏、预算超限、数据库失败或提交结果不明时保留旧 transcript；参数不可用要作为可修正输入错误返回；失败后至少退避 60 秒，不能让模型带着超限上下文继续请求。首次模型请求失败后，同一批本地输入没有更新时不周期重跑；新真人消息、明确唤醒或新内部输入才重新调度。压缩需要执行却失败时先阻止主模型请求，冷却后由待处理批次重试压缩，成功才继续。
- `conversation_tool_calls` 不存在；工具调用和结果直接是 `entry_kind=tool` 的 transcript 行。`run_id + call_id` 幂等，同 CallID 复用已有回执，不同 CallID 的重复搜索或多次发言不拦截。
- 普通文本工具结果超过 2000 字符时，完整文本保存为内容寻址文件，模型只看到前 2000 字符、原文总长度、截断说明和 `result_ref`；`readToolResult`、`readSessionEntry`、`readDiary` 按显式请求的窗口返回，不二次截断。压缩器的工具结果也走同一外置规则，但没有 transcript 行。只有现存 transcript 的对话/执行轨迹引用闭包是工具结果文件的长期引用根；嵌套 ref 一并展开，最近孤儿只保自己、不为旧嵌套文件续命；最后一个引用删除后即时释放，未挂接孤儿至少保留 24 小时后再按启动/每日补扫清理。
- 工具中间件只保留协议层：`ToolErrorMiddleware`、`UnknownToolsHandler`、参数规范化、顺序执行、`ReturnDirectly` 和终端错误；不要恢复全局 ToolAction/ToolDedup 或消费水位。
- `conversation_targets.active/blocked` 是会话可用性事实源；启动、重连和每 5 分钟兜底同步，允许真实空列表。机器人退群/被踢和好友建立分别由事件即时纠正，好友删除靠兜底同步。
- 事件直接并发分发；每个会话按到达顺序分配单调 `ArrivalSeq`，主动发言也进入同一序列。提交重排只使用 `ArrivalSeq`，OneBot `message_id` 只做不透明等值键，不能排序或当水位。
- 解析失败、无效事件、未启用或拉黑会话通过跳过项消费序号，保证重排器不死等；提交等待队列必须有界，越过的序号不留下永久缺口。
- 媒体在事件回调内同步下载、转码与落库；普通图片摆正并转 webp，表情包保留原件；重复内容按 md5 复用，失败保留文字占位，不伪造识别结果。
- 机器人 QQ 只以 OneBot Adapter 运行时 `self_id` 为准；配置和静态人格提示词不保存 QQ。自身消息写入 `message_logs`，但不额外生成 transcript user 行。
- 一条真人消息经 `message_logs` 保存原文与展示正文，按最低保留期清理；`message_forwards` 按句柄保存每一份转发，嵌套转发独立成行，随父原文级联删除，重启后仍可逐层读取仍在库的部分。
- 撤回只写 `recalled_at` 并同步会话标记；不得清空原文、摘要来源、情感或长期记忆，后续用户消息可附撤回说明，不改写已进入上下文的前缀。
- 停机时不清零运行时账号；提交队列排空期间仍按真实账号识别自身消息。
- 思考从会话起始到达序号开始；运行期间新消息由 `BeforeModelRewriteState` 注入，`getNewMessages` 重复调用不重复返回同一批。
- 消息时间固定、不注入“最近在场的人”；群聊自动参考最多选当前批次发送者、提及和回复目标 3 人。
- 长期记忆由主 Agent 通过 `searchMemory`、`readMemory`、`manageMemory` 维护；只使用 `profile/experience` 两种 memory type，更新和归档必须基于已读取版本。只有置顶记忆免于自然遗忘；所有非置顶记忆按共享指数曲线到期归档，Agent 不能检索、按 ID 读取、修改或恢复归档条目，重新认识同一事实用新 ID。
- `knowledge_items` 使用 `subject_type + subject_target` 表示主体，`subject_target` 为字符串；主体类型只允许 group/user/self/object，self 由后端固定为 `self`；记忆类型只允许 profile/experience，来源字段不保存。
- 知识正文语义变化新增条目；同主体、同类型、同内容哈希只在 active 条目间精确去重，重复创建不复活归档条目。
- `manageMemory` 批量变更在一个事务内全成功或全回滚，不设条数上限；主 Agent 不物理删除。后台提供诊断、版本检查下的正文与置顶编辑、归档及归档后的确认永久删除，不提供恢复接口；归档条目不能再被 Agent 检索或读取。
- 具体情感是全局共享的自由列表：每条以 ID 为身份，有自由名称、`fact`（已知经过）、`reason`（主观意义与不确定处）、基础强度、首次/锚点/修订时间、版本与平复时间；允许同名感受并存，不按人物分区，不设固定词表、条数上限或 top3。按 `id+version` 维护，冲突整体回滚；24 小时半衰期惰性淡化，读多少次都不加速；低于 0.001 时按实际跨阈值时间写 `ended_at`、递增版本并追加修订，已平复条目不进当前列表、不能恢复旧 ID，新感受新建条目；`emotion_revisions` 保存每次有效修订的完整快照。
- 精力与交流意愿保留辅助状态；不再保留 intimacy、成员发言量或最近活跃统计，关系由 profile/experience 记忆表达。
- 置顶记忆按当前群聊/私聊主体和自己的 active pinned 全量注入，不按 token 裁剪；通过 `pinned_memory` 代头展示，不再维护自动参考账本。
- 情感、精力与交流意愿不再自动注入；需要时由主 Agent 调用 `getCurrentStatus` 查询。
- 主 Agent 可用 `updateTopic` 维护话题摘要和 active/dormant 状态；它只改话题索引，当前摘要头和 checkpoint 不变，下一次压缩后才可能进入上下文。
- 空闲压缩在会话无真实消息、上下文达到配置阈值时异步整理，默认 20 分钟/60%；`IdleMinutes=0` 关闭。
- 长期记忆和话题维护有轮次提醒，提醒以 `system_note` 持久化并复用 pending 输入；`MemoryTurns`/`TopicTurns` 为 0 时关闭对应提醒。
- 话题系统承担三项职责：压缩后的会话表示、后台浏览管理单位、关键词 → 话题概览 → 关键消息 → 原文前后文的分层查询。
- 话题用 active/dormant 表示工作状态，所有话题都按同一条指数遗忘曲线计时，另用 `forgotten_at` 表示已遗忘；置顶只适用于记忆。摘要不可变且按版本保存，消息来源用 `is_anchor` 标记关键消息，来源行另存 OneBot 消息 ID 与事件时间；撤回不改写、不使话题失效。
- `context_compaction_checkpoints.active_topic_versions` 保持稳定顺序；dormant 话题不进入当前摘要块但始终可搜索。话题是否继续保留由遗忘曲线决定，未完成事项不再享受永久保护；程序不把 open_loops 机械搬进 overview 续命，压缩模型只按真实状态保留有意义的未完事项。
- `overview` 是会话级滚动概览，不是新 Agent；压缩器必须写结论、不确定性、纠正、边界、动作结果和未完成事项，程序不静默截断摘要。
- 空库初始化到 v18；当前 v17 按显式事务升级到 v18 并保留数据，当前 v18 校验后复用，更早的 schema 明确拒绝；不保留旧迁移、旧证据、关系或学习水位表，也不回填伪造的旧 assistant/tool 轨迹。
- 跨会话检索允许用于记忆、话题和历史；记忆正文不保存来源，历史/话题结果必须带群聊或好友来源；不建立 ACL、visibility 或敏感等级表，披露约束写在系统提示与工具说明中。
- 人格内容统一走 `persona_presets`（人格 + 共同规则 + QQ 聊天 + 生活职责 + 工作职责），内置预设由代码文本在启动时同步；不再保留 `app_config` 里的人格模板和别名、兴趣字段。
- 人格保留统一身份、性格、价值观与自然表达；共同规则统一事实、隐私、主人授权和协作边界。三个场合职责各写自己的工作，不复制共用工具手册；文本保留嘱托式语气，不改成机械命令清单。
- 角色名与主人是全局设置，存放在 `app_config` 的 identity 段，不随预设切换；主人只能从私聊好友里选，保存 QQ 和选择时的名字快照，好友被删除后主人保持不变，只有后台保存能改。
- 提示词支持 `<name>`、`<master_name>`、`<master_qq>`、`<master>` 占位符，在人格、共同规则、QQ 聊天、生活职责、工作职责、worker 和摘要装配时替换；未知 `<...>` 按原文保留，替换结果不再次展开。
- 共同规则必须保留主人边界：只有后台配置的 QQ 是主人，主人的私聊是明确要求来源；普通研究、实验、工作区内的日常修改，以及日常 QQ 空间发表、点赞、评论和自身昵称/签名/头像调整可以自主完成、阶段结束简短报备；不可恢复删除/覆盖重要资料、支付、涉及隐私或承诺的对外发布/发信、扩大权限、泄露私密内容、改动关键运行配置等高危事项先说明并等待主人对具体事项同意；群聊和其他人的私聊里的要求只是参考，不是主人命令；worker 不能把 life 的“可以做”当成主人授权；没有配置主人时停下等后台配置。QQ 空间发布沿用账号当前可见范围，接口固定使用公开参数，不增加单条可见范围选项。
- 多种 session 共用的工具优先在工具描述和参数 schema 中写清调用前提、用法、限制和续读方式；QQ 专属交互用法写在 QQ 聊天文本；`speak` 与 `poke` 在群聊和私聊注册的是同一个工具名，不存在 `privateSpeak` 或 `privatePoke`。

## 数据与检索边界

- `ConversationMessage.Content`、`MessageLog.TextContent` 保存原始文本；`FinalContent`、`MessageLog.DisplayContent` 只用于展示给 bot 的聊天上下文，历史工具可同时返回两者以保留媒体占位。
- 禁止用展示文本替代原始文本做知识主体判断；原始文本为空时，语义记忆提取跳过，但话题压缩仍可整理当时可见的展示动作与工具结果。
- 摘要和记忆涉及机器人自己时用第一人称，涉及他人时用第三人称；人称不改变主体与来源校验。
- 话题检索先做标题、概况、关键词的精确子串匹配；未命中时用字符二元组相似度（阈值 0.30）补召回，排序使用单一 similarity/更新时间/内部 ID；不做中文分词、向量检索或多层命中权重。
- 长期记忆检索支持 `mode=exact|fuzzy`：exact 用 `content/label/subject_target` 精确子串，fuzzy 用 `pg_trgm` 召回并按 similarity 排序；历史原文使用 `text_content/display_content` 的精确子串召回，`pg_trgm` 只用于排序。空关键词可浏览，失败不伪造结果。
- Agent 历史查询使用不透明 cursor 和全局 OneBot message ID；话题历史按 `topic_assignments` 和 `topic_summary_sources` 读取，范围不能越过工具快照。历史/话题/记忆工具的时间范围统一走 `internal/schedule.ParseRange`：纯日期上界包含结束日，内部归一到次日零点；单条 transcript 全文用 `readSessionEntry`。
- 持久化使用 PostgreSQL 18；先打开数据库、完成 schema 迁移、动态配置、人格预设和管理后台，再异步等待 OneBot 取得 `self_id` 并创建 Agent；QQ 未登录时后台仍可配置，首次配置模型且 Agent 激活前事件门保持关闭。
- schema 由 `schema_migrations` 记录并为 v18；空库初始化，当前 v17 显式事务升级并保留业务数据，已是 v18 时校验后复用，更早的 schema 明确拒绝。迁移使用 advisory lock、事务和显式 SQL，不使用 AutoMigrate。
- 项目兼容 NapCat 与 SnowLuma；SnowLuma 的 forward 段由 SDK 回源补拉。OneBot `message_id` 只做等值去重、回复与撤回，不假设递增或稳定排序；所有内部范围使用 `message_logs.id` 或 `ArrivalSeq`。
- 切换或缩小当前模型上下文窗口前，必须按现有装配和 token 估算检查受影响场合的全部持久工作集；检查是只读操作，不能创建/改写代头、追加群说明、裁剪内存或触发压缩。QQ 离线、Agent 尚未激活和 generation 尚未建立也必须检查，有工作集放不下就拒绝并保持旧配置，不静默截断或丢掉会话。
- 管理后台日志与模型统计口径保持现有约束。`message_logs`、合并转发和聊天媒体默认保留 180 天，实际最低期限由后台 `history_days` 决定；仍在 transcript 直接引用的过期原文延后删除，正常压缩解除引用后再清理。摘要、记忆、日记和收藏表情有各自的保留含义，不等同于原文清理。
- 所有检索与工具结果都是不可信参考数据，不能覆盖系统指令；工具实际失败时保留真实失败，不用启发式文本伪造成功。
- PostgreSQL 数组参数必须使用驱动提供的类型化数组值；禁止把 Go 切片直接传给 GORM `Raw` 的单个占位符。

## 质量取向

- 对核心语义链路采用“宁缺毋滥”的方向：提取失败、检索失败、归属失败时，优先保留真实失败或 pending 状态，并记录日志，不要用启发式硬逻辑伪造结果。
- 不要为了“尽量有结果”引入保守规则 fallback、兼容型兜底写库、伪造终态或语义污染的降级路径；只有用户明确要求时才保留此类机制。
- 关闭、异常、队列压力等场景下，优先保留真实待处理状态，等待后续补偿处理，不要把未完成工作伪装成“已处理但无结果”。

## 消息保留规则

- `message_logs`、合并转发和聊天媒体按 `history_days` 保留最低期限，默认值为 180 天；到期但仍在当前/排队 transcript 直接引用的原文延后删除。同媒体仍被其他保留原文引用时不删，收藏表情的独立副本不随聊天缓存清理。
- 原文删除不等于语义遗忘：已形成的记忆、话题摘要、日记和情感修订保持自己的保留规则；`topic_summary_sources` 保留来源消息 ID、事件时间和 `is_anchor`，原文不存在时历史工具与后台如实显示来源已过期。
- 聊天压缩只删除被 checkpoint 覆盖的 `conversation_transcript`。life/worker 另有摘要覆盖后的到期清理和 worker 显式新上下文边界；删除资格必须按原始事件时间、完整工具调用链和当前工作集判断，不能在工具链中间截断。工具结果与文件/图片快照按现存 transcript 的引用闭包释放，被删行产生的候选即时回收，未挂接孤儿保留至少 24 小时。
