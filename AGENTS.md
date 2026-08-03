# AGENTS

本文件约束本仓库内 AI/人工协作时的默认开发行为。

## 项目概览

- MumuBot 是运行在 QQ 群里的智能体：通过 OneBot 11 接收群消息，使用 ReAct（观察、思考、行动循环）决定是否回复、调用工具或保持沉默。
- 主链路由 `main.go` 启动：配置加载、模型客户端、记忆系统、聊天 agent、管理后台和退出清理都在这里串起来。
- 语义能力分成几条边界清楚的链路：话题工作记忆负责当前对话脉络，长期记忆负责稳定事实，learner 只学习群文化，后台负责查看、审核和管理。

## 目录地图

- `internal/agent/`：运行编排层，连接 OneBot、话题、学习、工具、模型和思考循环。
- `internal/onebot/`：napcat-sdk 接入、OneBot 11 Adapter、消息事件解析和发送 API。
- `internal/topic/`：话题工作记忆、话题归属、摘要刷新、归档检索和提示词上下文。
- `internal/memory/`：PostgreSQL 数据模型、pgvector/pg_trgm 检索、长期记忆、消息日志、成员画像。
- `internal/learning/`：群文化学习，只处理黑话、风格卡片和成员画像。
- `internal/tools/`：暴露给 ReAct agent 和学习审核流程使用的工具。
- `internal/web/`：管理后台 HTTP 服务、页面模板、前端资源和后台业务服务。
- `config/`：运行配置、人格提示词和 MCP（模型上下文协议，用于接入外部工具）示例。

## 常用入口

| 任务 | 入口 |
|------|------|
| 看启动顺序 | `main.go` |
| 看消息如何进入 bot | `internal/onebot/client.go`、`internal/agent/message.go` |
| 看回复决策 | `internal/agent/think.go` |
| 看话题分配和摘要 | `internal/topic/manager.go`、`internal/topic/assignment.go` |
| 看长期记忆入库 | `internal/memory/claim_extractor.go`、`internal/memory/memory_ingest.go` |
| 看群文化学习 | `internal/learning/learner.go` |
| 看后台页面 | `internal/web/views/*.templ`、`internal/web/app/app.go` |

## 运行与验证命令

```bash
npm ci
npm run build
templ generate ./internal/web/views
go build ./...
go vet ./...
```

- 只改 Go 逻辑时，至少运行 `go build ./...`；涉及语法或逻辑变更时再运行 `go vet ./...`。
- 修改 `.templ` 后运行 `templ generate ./internal/web/views`。
- 修改 `internal/web/assets/src/` 后运行 `npm run build`。
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
- 后台只保证 PC 端正常使用，不为移动端增加抽屉、响应式重排或独立适配；页面保持至少 1180px 的稳定工作区。
- 新增或大改后台页面前必须先用 Stitch 完成设计，再按确认稿实现。允许 daisyUI 控件细节与设计稿略有不同，但页面内容、排版、风格、配色和圆角层级不能明显偏离。
- 页面以纯白或微粉近白为底，暖白侧栏、樱粉主色和青绿状态色为主要视觉语言；整体年轻、Anime Friendly、多圆角但不幼稚，避免大面积粉色背景和板正的传统企业后台风格。
- 优先复用 daisyUI 组件和浏览器原生控件，不重复手搓按钮、输入框、下拉菜单、弹窗、提示、加载态和分页；图表统一使用 ECharts。
- 保持所有页面的间距、字重、圆角、颜色和交互动效统一。
- 所有重要交互都要有明确反馈；动效应控制在轻量范围内，并尊重 `prefers-reduced-motion`。

## 模板与资源

- 后台页面模板位于 `internal/web/views/*.templ`。
- 修改 `.templ` 文件后，必须运行 `templ generate ./internal/web/views`。
- 前端资源源码位于 `internal/web/assets/src/`。
- 修改前端资源后，必须运行 `npm run build`。
- `internal/web/assets/dist/` 是构建产物，不手工修改，不纳入版本控制。

## 更新日志与发布

- 根目录 `CHANGELOG.md` 是 GitHub Release 的发布说明来源。
- 更新日志遵循 Keep a Changelog 1.1.0 风格：保留 `Unreleased` 区块，版本按倒序排列，变更类型使用中文小标题：`新增`、`变更`、`废弃`、`移除`、`修复`、`安全`。
- 每条更新日志使用“**加粗概括**：详细说明”的形式，先说明用户或维护者能感知到的结果，再补充影响范围。
- 只有项目代码、运行配置、用户文档或发布工作流产生了使用者或维护者可感知的变化时，才更新 `CHANGELOG.md`。
- 更新日志面向使用者和维护者，描述影响和结果，不堆砌 git 提交，不写内部实现流水账。
- 调研报告、方案讨论、审查过程、验证记录和 `AGENTS.md` 等协作说明不属于项目变更，不写入 `CHANGELOG.md`。
- 发布新版本前，必须把 `Unreleased` 中待发布的内容移动到 `## [x.y.z] - YYYY-MM-DD` 版本区块；tag 使用 `vx.y.z`，并确保版本区块存在，否则发布流程会失败。
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
- 不要改动与当前任务无关的脏文件。
- `docs/` 下的研究报告默认是只读基线，除非用户明确要求，否则不修改。
- 未经用户明确要求，不创建 commit、切换分支或推送远端。

## Go 代码结构

- 单个 `.go` 文件不超过 600 行；超过时按职责拆分，每个拆出的文件应有内聚的职责域，不要零散地把函数分到很多小文件。
- 同一函数内不要重复调用 `config.Get()`，提取为局部变量复用。
- 同一函数内不要重复调用 `strings.TrimSpace` 处理同一个值；如果上层已经 Trim 过，下层不应再 Trim。
- 不要在同一表达式或相邻行中对同一值重复调用（如 `a.bot.GetSelfID()` 在同一条件中出现两次），提取为局部变量。
- 同一函数中多次使用同一缓冲区/切片时，取一次后复用变量，不要重复加锁取数据。
- 项目当前无测试文件；未经用户明确要求，不新增 `*_test.go`。所有代码改动必须通过 `go build ./...`，涉及语法或逻辑变更时额外运行 `go vet ./...`。
- JSON 解析和序列化优先复用项目已有的 Sonic；`json.Number`、`json.RawMessage` 等标准库边界类型可保留。

## 死代码

- 发现死代码（永远不会执行的分支、赋值后立即被覆盖的变量、从未被读取的字段）应立即删除，不要保留"以后可能用到"的无用逻辑。
- 删除死代码时同步清理关联的 import，避免 `imported and not used` 编译错误。

## 生成文件

- `internal/web/views/*_templ.go` 由 templ 生成，禁止手动修改；需要改页面结构时修改对应的 `.templ` 文件并运行 `templ generate`。
- `internal/web/assets/dist/` 是前端构建产物，不手工修改，不纳入版本控制。

## 核心架构约束

- 话题工作记忆是主路径能力，不按“可关闭功能”设计，也不要为核心链路补可选回退开关。
- 对话模型只保留 high 和 low 两档：high 用于主 ReAct，话题、学习、分类和记忆等原中低档任务统一使用 low；不要恢复 mid 配置或兼容别名。
- 回复决策保持单次 ReAct `Generate`，不拆 Planner/Replyer；`classifyContext` 只做轻量的参与判断、检索词和风格场景规划，不生成第二套聊天上下文。
- 强提及、点名和回复机器人必须进入 ReAct；分类失败不能拦截强交互。每群保持单任务串行和必要的 rerun，普通触发继续使用既有概率门控。
- `think` 必须基于固定消息快照，思考期间新到消息留给下一轮；`speak` 保持一至五条消息，每条可独立设置 `reply_to` 和 `mentions`。
- 人格提示词只做增量调整；保留 `config/persona.prompt` 中 B站、贴吧、知乎和微博四个平台的参考原句，不恢复已删除的成组矫正案例。
- 学习系统只负责群文化学习：黑话、风格卡片、成员画像。学习系统不承担自动长期记忆写入职责。
- 自动长期记忆下沉只由话题摘要链路触发；显式工具调用（如 `saveMemory`）与该自动链路分开考虑，不要把 learner 扩展成长期记忆写入宿主。
- learner 的消费应由话题系统决定，只消费“话题系统已处理完成”的消息；不要让 learner 与话题摘要重复扫描同一批未判定原文。
- 成员画像保持全局 `user_id` 维度，不引入 `group_id` 维度，除非用户明确提出新的画像隔离需求。

## 数据与检索边界

- `GroupMessage.Content`、`MessageLog.TextContent` 保存原始文本；`GroupMessage.FinalContent`、`MessageLog.DisplayContent` 只用于展示给 bot 的聊天上下文。
- 话题归属、话题摘要、主动长期记忆检索、黑话匹配、风格分类、成员画像学习、长期记忆提取等语义链路默认只使用原始文本；如果原始文本为空，就直接跳过，不要回退到渲染后的展示文本。
- 当主动检索或话题检索词生成失败时，优先跳过该步骤，不要回退到最近聊天展示文本或其他启发式拼接结果。
- 持久化使用 PostgreSQL；当前按全新数据库设计，不保留旧数据迁移、兼容字段或过渡表。
- 第一版检索固定为 pgvector 精确向量、pg_trgm 文本召回和 RRF 融合；没有明确需求和测量依据时，不引入 BM25 扩展、HNSW、Milvus、Elasticsearch、MMR、reranker、聚类或额外分类器。

## 质量取向

- 对核心语义链路采用“宁缺毋滥”的方向：提取失败、分类失败、归属失败时，优先保留真实失败或 pending 状态，并记录日志，不要用启发式硬逻辑伪造结果。
- 不要为了“尽量有结果”引入保守规则 fallback、兼容型兜底写库、伪造终态或语义污染的降级路径；只有用户明确要求时才保留此类机制。
- 关闭、异常、队列压力等场景下，优先保留真实待处理状态，等待后续补偿处理，不要把未完成工作伪装成“已处理但无结果”。

## 消息保留规则

- 消息清理不能删除已分配话题的消息。
- 消息清理不能删除 learner 尚未处理的消息。
