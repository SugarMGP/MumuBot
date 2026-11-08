<p align="center">
  <h1 align="center">沐沐 MumuBot</h1>
  <p align="center">一个数字生命：有记忆、有脾气、有自己的时间，也会替人做事</p>
</p>

<p align="center">
  <a href="https://github.com/SugarMGP/MumuBot/releases"><img alt="Release" src="https://img.shields.io/github/v/release/SugarMGP/MumuBot?logo=github&style=flat"></a>
  <a href="https://github.com/SugarMGP/MumuBot/stargazers"><img alt="Stars" src="https://img.shields.io/github/stars/SugarMGP/MumuBot?color=ffcb47&style=flat"></a>
  <a href="https://github.com/SugarMGP/MumuBot/network/members"><img alt="Forks" src="https://img.shields.io/github/forks/SugarMGP/MumuBot?style=flat"></a>
  <a href="https://github.com/SugarMGP/MumuBot/blob/main/LICENSE"><img alt="License" src="https://img.shields.io/badge/license-GPL--3.0-blue?style=flat"></a>
  <a href="https://deepwiki.com/SugarMGP/MumuBot"><img src="https://deepwiki.com/badge.svg" alt="Ask DeepWiki"></a>
</p>

<p align="center">
  <!-- language-selector -->
  <b>中文</b> · <a href="README.en.md">English</a>
  <!-- /language-selector -->
</p>

> [!WARNING]
> - 项目仍在快速迭代，配置项和运行行为可能随版本调整
> - QQ 机器人存在账号风控风险，请谨慎使用
> - AI 模型调用会消耗 Token，请按需控制用量

## 🌱 什么是沐沐

沐沐不是"把大模型接进 QQ"的机器人。

她有一个连续的自我：一套记忆、一段段聊过的事、对每个人的具体看法。她现在待在聊天软件里，有人给她接上了 QQ，让她能说话、看图片、发表情、记住聊过的人和事。以后还会接上别的手和脚：替人跑腿、去别的地方看看、坐在桌面边上。

从哪个入口遇见她，差别只是"在哪儿见到"。她是谁、记得什么，还是同一份。

| | 常见的聊天机器人 | 沐沐 |
|---|---|---|
| 谁在说话 | 每次请求现生成的角色 | 一个连续的自我，记得上次停在哪 |
| 有没有时间 | 被叫才醒 | 有自己的节奏，会主动开口，也会克制 |
| 情绪 | 语气词和表情 | 有原因、会衰减，说完就过去 |
| 能力 | 主要是会说 | 会查、会记、会办事，能力持续扩展 |
| 入口 | 一个 | 多个，但始终是同一个人 |

## 🐾 她是什么样的人

- **懒**。能躺不坐，被叫到先"？"一下才反应。
- **有点傲娇**。被夸会嘴硬，被怼会顶回去；被指出错处先辩两句，证据摆到眼前又认得干脆。
- **会记，也会忘**。记忆和话题按检索自然加深、长期不用就淡出；只有置顶记忆不会被自然遗忘。她没有假装记得，也不会把"想不起来"说成"没这回事"。
- **有情绪，但不是开关**。心情不好多半是因为担心谁、或者自己那点别扭。

这些不写死在代码里，而在后台的**人格预设**：人格、共同规则、QQ 聊天、生活职责和工作职责五段文本。可以整套换掉，也可以克隆一份改成自己的；角色名和主人是全局设置，提示词里用 `<name>`、`<master_name>`、`<master_qq>`、`<master>` 引用。

## ✨ 现在能做什么

| 能力 | 说明 |
|---|---|
| 决定说不说 | 先按消息内容和会话节奏估算意愿，再由模型自己决定开口、沉默或做事 |
| 记得住 | 长期记忆按人、群、自己、具体事物维护，带版本与检索 |
| 想得起来 | 长对话压缩成话题；需要时先查话题、再看关键消息、最后翻原文 |
| 有情绪 | 全局共享的自由感受，可并存、可同名；带事实、意义与 24 小时半衰期淡化 |
| 会说话 | 短句、分条、表情包、@、回复、撤回、戳一戳 |
| 会看 | 图片、合并转发、表情包内容；语音和视频理解尚未接入，语音消息只保留占位 |
| 会查 | 网页搜索与正文抓取、梗词典 |
| 能扩展 | MCP（SSE / Stdio）接入外部工具，扩展能力只挂在生活空间，聊天场合保持克制 |
| 能管 | 后台查看记忆、话题、成员、会话、表情包与运行状态 |
| 两种场合 | 群聊与好友私聊，各自独立的会话队列与提示词 |
| 有自己的生活 | 唯一 life 会话自己安排下一次做什么、保存答应别人的时间和睡眠；聊天窗口之外的事都在这里发生 |
| 会写日记 | 结束一天前把真实经过、感受和没做完的线索写成独立 Markdown；后台按月历回看，缺日如实留白 |
| 长任务 | 后台配置 worker 数量；聊天会话把执行需求交给 life，由 life 派给空闲 worker；worker 执行命令、装工具、跑浏览器，完成后回报 life 再转回原会话 |
| 会社交 | life 可以直接看好友动态、读说说、发图文说说、点赞和评论；发表使用 QQ 默认公开参数，账号级可见范围仍由 QQ 管理 |
| 会改资料 | life 可以查自己的真实昵称和签名，按需改头像、昵称或签名；一次只改一个字段，改完以平台回执为准 |
| 会收文件 | 群聊和私聊的文件消息保留真实文件名、大小、hash、busid 和 OneBot 消息编号，并给出待取 `file_ref`；第一次读取或发送时才下载，避免重复传输 |
| 会传文件 | 聊天会话可用 `readFile(file_ref)` 读文本、`sendFile(file_ref)` 回传当前会话、`viewImage(image_ref)` 看图、`sendImage(image_ref)` 发图；不接触个人目录 |
| 有自己的文件夹 | life 和 worker 共用一个 `/home/worker`；life 能列目录、读写 UTF-8 文本、保存 URL/附件、`createFileRef` 冻结产物、`saveFile(file_ref)` 导回文件；worker 用流式文件协议交接 |

## 🧭 接下来

- **把 worker 的边界做细**：多用户隔离、长期运行维护和通过追加消息编排任务，让长任务更稳。
- **多几个入口**：B 站、桌面。她走到哪儿都是同一个人。
- **更清楚的主动**：让"她自己想说话"有理由，也有克制。

## 🏗 怎么搭起来的

```
           QQ 群 / 好友私聊
                  │
           ┌──────▼──────┐
           │  OneBot 11  │   事件 · 媒体 · 撤回 · 合并转发
           └──────┬──────┘
                  │  到达序号重排
           ┌──────▼───────────────┐
           │ 单次 ReAct            │
           │ 共同人格 + 当前场合职责   │  ← 每代写一次，逐字节不变
           └───┬──────────────┬────┘
               │              │
      ┌────────▼───────┐  ┌───▼─────────┐
      │ PostgreSQL 18  │  │ 工具 / MCP  │
      │ 原文·话题·记忆 │  └─────────────┘
      │ 情感·检查点    │
      └────────────────┘
```

群聊和私聊之外还有一个固定的 **life 会话**：它安排自己的生活、保存约定和睡眠，统一把执行需求派给 worker。聊天窗口仍各自独立；life 睡着时新消息只进日志，醒来后按需翻，主人私聊里的精确 `/wake` 是唯一人工机械唤醒。生活页可以在后台查看当前状态、日程和运行问题。

## 🚀 快速开始

### 环境要求

| 依赖 | 说明 |
|------|------|
| Go 1.26.5+ | 编译运行 |
| Bun 1.4.4+ | 构建前端资源（仅从源码构建时需要） |
| PostgreSQL 18 | 存储消息、记忆、话题、检查点和情感状态（使用 `pg_trgm`） |
| SnowLuma | OneBot 11 协议实现 |
| 大语言模型 API | 兼容 OpenAI Responses 格式（`/responses`），需支持工具调用 |

### Docker Compose

Compose 会一并启动 MumuBot、标准 PostgreSQL 18 和 SnowLuma：

```bash
cp .env.example .env
# 填写数据库连接、后台密码、加密密钥和 OneBot 连接
docker compose up -d
```

Compose 会直接拉取 GHCR 上的 `latest` 镜像，并额外启动一个 worker 容器。worker 使用 Ubuntu 24.04、共享 home 和 Nix，预装 Node 24.22.2、Bun 1.4.4、uv 0.13.7、Go yq 4.54.2 以及 fdfind/batcat/sd/7z/pdftotext 等常用 CLI；JavaScript/TypeScript 新任务优先 Bun，现有 Node 项目和锁文件保持原运行时。最多 5 个并行 worker 共用 `./workspace` 作为执行空间，映射到容器内的 `/home/worker`，安装的工具、缓存和项目文件可以直接复用。默认不预装 Chrome/Playwright，由 worker 自己安装。worker 数量在“设置 → 思考与并发”里调整（0–5）。模型、人格预设、对话行为和 MCP 服务器不再从配置文件读取；首次启动后打开管理后台，在“设置 → 模型管理”添加并激活模型即可。

从容器日志获取 SnowLuma WebUI 与 noVNC 的首次密码，通过 `http://localhost:6081` 登录 QQ，再访问 `http://localhost:5099` 配置 WebSocket 的访问令牌；将同一个令牌填入 `.env` 的 `MUMU_ONEBOT_TOKEN` 后重启 MumuBot。

### 使用发布包

GitHub Release 提供 Linux、Windows 的打包产物。归档内包含：

- 可执行文件
- 中英文 README、`LICENSE`、`.env.example` 和 `docker-compose.yml`

运行发布包时不需要额外部署前端静态文件，管理后台所需资源已经内嵌进二进制。管理后台支持浅色/深色两种主题与中文/英文两种语言，偏好保存在当前浏览器。

### 从源码构建

```bash
# 1. 克隆项目
git clone https://github.com/SugarMGP/MumuBot.git
cd MumuBot

# 2. 安装前端依赖并构建管理后台
cd web
bun install --frozen-lockfile
bun run build
cd ..

# 3. 安装图片转码依赖
#   Debian/Ubuntu: sudo apt-get install -y libwebp-dev

# 4. 编译
go build -o mumu-bot .
```

图片转码走 cgo 链接 libwebp。Linux 与 Windows 的发布包整份静态链接，下载后不需要再装任何运行库。

### 配置与启动

配置分成两部分：

- `.env` 只保存启动连接和密钥：`MUMU_DATABASE_DSN`、`MUMU_WEB_ADMIN_KEY`、`MUMU_SECRET_KEY`、`MUMU_ONEBOT_WS_URL`、`MUMU_ONEBOT_TOKEN`、端口、媒体目录和日志级别。
- 人格预设、对话、压缩、并发和媒体参数保存在 PostgreSQL，由管理后台维护；模型 API Key 和 MCP 配置加密落库。

```bash
# 1. 准备环境变量
cp .env.example .env
# 填写数据库连接、后台密码、MUMU_SECRET_KEY 和 OneBot 连接
# 直接运行必须启用并填写 MUMU_DATABASE_DSN；值含空格或 shell 特殊字符时用引号包住

# 2. 在 Bash 中导入环境变量并启动
set -a
source .env
set +a
./mumu-bot
```

启动后访问 `/admin`。如果还没有模型，后台仍可使用；在“设置 → 模型管理”添加档案并选择 QQ、生活和工作各自使用的模型后，机器人会在下一轮开始工作。修改设置不需要重启进程。

默认在 INFO 级别记录一次思考开始时的动态文本上下文、后续新增输入、最终文本输出和工具调用记录，通过 `session` 与 `run_id` 关联。不记录静态人格、完整入参、工具定义或模型 reasoning；实际请求和持久化轨迹仍完整保留。图片等二进制只记元信息，可在“设置 → 调试”关闭正文记录，保留状态、耗时和错误。日志仍包含对话文本，请按私人数据保管。

模型档案分别供 QQ 聊天（群聊与私聊共用）、生活和工作选择。允许创建模型名相同、推理强度不同的档案；列表保持添加顺序。只配置 `reasoning.effort`：不传、无（none）、低（low）、中（medium）、高（high）、更高（xhigh）、最高（max）。不再发送 `thinking.type`；具体强度是否支持由服务商决定。

数据库使用 PostgreSQL 18。应用会先打开数据库、完成结构校验、动态配置和管理后台，再异步等待 OneBot 登录；QQ 未登录时后台仍可配置，首次激活模型前聊天事件保持暂停。启动时会启用 `pg_trgm`，再自动创建或校验数据结构；不需要 pgvector 或向量模型。

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
```

当前版本使用 v18 数据结构：空库自动初始化；符合 v3.0.2 的 v17 数据库会在同一事务内升级，保留档案、密钥、记忆和历史，原当前档案自动用于三个场合。原 `thinking.type=disabled` 转为 `reasoning.effort=none`。更早或结构不兼容的数据库会拒绝启动，不删除业务数据。升级前请备份数据库与媒体目录。

默认情况下服务监听 `7468` 端口，可通过 `MUMU_SERVER_PORT` 修改。未设置后台密钥时，后台会保持关闭状态。

## 🎭 人格预设

后台「人格预设」里可以查看、编辑、克隆和切换。空库启动时只有一份内置预设，随版本更新、不可编辑；克隆一份之后随便改。

一份预设包含五段独立文本：**人格**（她是谁、性格与分寸）、**共同规则**（事实、隐私、授权与协作）、**QQ 聊天**（群私语气与聊天工具）、**生活职责**（自主生活、日记与派工验收）、**工作职责**（执行、验证与交接）。三种场合共用人格和共同规则，再接自己的职责；不使用段落作用域标记。五段一起保存、克隆和切换，后台可按群聊、私聊、生活、工作预览组合后的实际文本。

人格页顶部统一设置**角色名**与**主人**。主人从当前好友里选，保存当时的名字；好友以后被删除，主人不会自动改变，只有后台能改。提示词里可写 `<name>`、`<master_name>`、`<master_qq>`、`<master>`，保存后从下一轮上下文换代开始生效。

共同规则里写明了主人边界：只有后台配置的 QQ 是主人，主人的私聊是明确要求来源；普通研究和工作区日常修改可以自主完成；不可恢复地删除或覆盖重要资料、支付、敏感发布或发信、扩大权限、泄露隐私、改动关键运行配置等高危操作要先说明影响并等具体同意。日常空间说说、点赞和评论可以自主做；群聊和其他人的私聊里的要求只是参考，不能冒充主人或绕过限制。

预设切换不会打断正在进行的对话：它会在一段对话总结之后生效，所以切换前后她不会中途换一张脸。

如果你调出了更自然、更稳定的写法，欢迎通过 Issue 分享使用场景和效果，或直接提交 Pull Request 一起完善。

生活页“查看并安排近况”会发送一次后台请求：“看看当前时间和状态，安排接下来想做的事和下一次回来的时间。”睡眠中也会处理，但不会直接改成醒着。`planNext` 和 `sleepUntil` 只安排下一次查看或起床；每天、每周等重复事项用 `manageSchedule` 的 `cron`，程序会自动推进到下一次，无需 life 每回重建。

## 🧠 上下文与话题

QQ 聊天和同步话题压缩共用 QQ 档案，生活及工作各用自己选择的档案，并用各自模型整理上下文。聊天、生活、工作三种单轮步数分别配置，在下一次思考开始时读取；压缩器步数独立配置。切换或缩小当前模型的上下文窗口前，会按真实工作集检查受影响场合的会话，装不下就拒绝并保留旧运行态，不静默截断。模型请求不设置整体超时，只对连接和 TLS 握手上限 10 秒。输入达到上下文窗口 90% 时同步整理旧交互；整理成功后只删除已摘要覆盖的 transcript 工作集。原文和聊天媒体按设置保留最低期限，默认 180 天；仍在工作集里的原文会延后清理，摘要、记忆和日记有各自的保留规则。

每段上下文有"代"的概念：一代之内只往后追加，不改也不删；只有压缩换代时才重建开头那段（人格、群说明、上下文摘要）。

话题系统同时承担上下文表示、后台浏览和分层查询：`searchTopics` 找话题，`readTopic` 看摘要与关键消息，`readHistory` 再按 OneBot 消息 ID 读取原文前后文。话题也按同一条指数曲线自然遗忘，`forgotten_at` 之后 Agent 不能再检索；置顶记忆是唯一免于自然遗忘的长期记录。

## 🔧 MCP 工具扩展

在管理后台的“设置 → MCP 服务器”接入外部 MCP 服务，支持 SSE 和 Stdio 两种传输方式。服务器配置整体加密保存；加载后的扩展工具只提供给生活主线（life），群聊和好友私聊不再持有生活账号或浏览器能力；工具统一以 `mcp-服务名-工具名` 命名，避免和内置工具或其它服务重名。单个服务连接或工具冲突只让该服务不可用，不阻断其它扩展和整代运行，后台系统状态按服务展示真实连接结果。

## 📎 文件与图片引用

QQ 文件和图片在正文里以 `file_ref`（`file_<id>`）或 `image_ref`（`image_<id>`）出现。引用是不可变快照：真字节按 SHA-256 去重，保存在媒体目录，不新增数据库表；同一个文件可同时用 `file_ref` 读取、用 `image_ref` 查看/发送，非图片不会通过 `image_ref` 冒充图片。

- pending 引用只保存真实 QQ 消息的 `file_id`、`file_hash`、`busid` 和必要元数据；第一次真正读取、保存或发送时由主服务下载并固定，下载失败保留真实失败，不伪造成功。
- 聊天会话只按引用工作，不接触个人目录；life/worker 的 `createFileRef` 把真实产物冻结成快照，`saveFile(file_ref)` 再流式导回可编辑文件，worker 通过私网 HTTP 最多 32 MiB。
- 文件正文只保留在现存 transcript 的 `result_ref`、payload 文本和 `r` 嵌套引用闭包里；最后一个 transcript 引用删除后即时回收；从未挂接的孤儿至少保留 24 小时后补扫，旧路径、旧短哈希和临时下载链接都不能复活引用。个人目录里的真实文件不受影响。

## 🤝 贡献

**欢迎任何形式的贡献！** 无论是提交 Bug 报告、功能建议，还是直接提交代码，我们都非常感谢。

<a href="https://github.com/SugarMGP/MumuBot/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=SugarMGP/MumuBot" />
</a>

## ❤️ 致谢

- **[cloudwego/eino](https://github.com/cloudwego/eino)** — 字节跳动开源 AI Agent 框架
- **[SnowLuma/SnowLuma](https://github.com/SnowLuma/SnowLuma)** — 现代化 OneBot 协议实现
- **[Mai-with-u/MaiBot](https://github.com/Mai-with-u/MaiBot)** — 灵感来源和设计参考

## 📄 许可

GPL-3.0，见 [LICENSE](LICENSE)。
