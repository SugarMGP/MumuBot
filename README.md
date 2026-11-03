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
- **会记，也会忘**。记忆有版本、会过时；她不会假装记得，也不会把"想不起来"说成"没这回事"。
- **有情绪，但不是开关**。心情不好多半是因为担心谁、或者自己那点别扭。

这些不写死在代码里，而在后台的**人格预设**：一份人格卡、一份仿真提示词。可以整套换掉，也可以克隆一份改成自己的。

## ✨ 现在能做什么

| 能力 | 说明 |
|---|---|
| 决定说不说 | 先按消息内容和会话节奏估算意愿，再由模型自己决定开口、沉默或做事 |
| 记得住 | 长期记忆按人、群、自己、具体事物维护，带版本与检索 |
| 想得起来 | 长对话压缩成话题；需要时先查话题、再看关键消息、最后翻原文 |
| 有情绪 | 12 类具体情感，带强度与原因，随时间惰性衰减 |
| 会说话 | 短句、分条、表情包、@、回复、撤回、戳一戳 |
| 会看 | 图片、合并转发、语音转写、表情包内容 |
| 会查 | 网页搜索与正文抓取、梗词典 |
| 能扩展 | MCP（SSE / Stdio）接入外部工具 |
| 能管 | 后台查看记忆、话题、成员、会话、表情包与运行状态 |
| 两种场合 | 群聊与好友私聊，各自独立的会话队列与提示词 |

## 🧭 接下来

- **把长任务切出去**：任务实体 + 独立执行器，聊天不被一个长任务堵住。
- **多几个入口**：worker、B 站、桌面。她走到哪儿都是同一个人。
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
           │ 人格卡 + 仿真提示词   │  ← 每代写一次，逐字节不变
           └───┬──────────────┬────┘
               │              │
      ┌────────▼───────┐  ┌───▼─────────┐
      │ PostgreSQL 18  │  │ 工具 / MCP  │
      │ 原文·话题·记忆 │  └─────────────┘
      │ 情感·检查点    │
      └────────────────┘
```

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

Compose 会直接拉取 GHCR 上的 `latest` 镜像。模型、人格预设、对话行为和 MCP 服务器不再从配置文件读取；首次启动后打开管理后台，在“设置 → 模型管理”添加并激活模型即可。

从容器日志获取 SnowLuma WebUI 与 noVNC 的首次密码，通过 `http://localhost:6081` 登录 QQ，再访问 `http://localhost:5099` 配置 WebSocket 的访问令牌；将同一个令牌填入 `.env` 的 `MUMU_ONEBOT_TOKEN` 后重启 MumuBot。

### 使用发布包

GitHub Release 提供 Linux、Windows、macOS 的打包产物。归档内包含：

- 可执行文件
- `README.md` 和 `LICENSE`

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
#   macOS:         brew install webp pkgconf

# 4. 编译
go build -o mumu-bot .
```

图片转码走 cgo 链接 libwebp。Linux 与 Windows 的发布包整份静态链接，下载后不需要再装任何运行库；
macOS 的系统链接器不支持全静态，产物动态依赖 libwebp，运行前先 `brew install webp`。

### 配置与启动

配置分成两部分：

- `.env` 只保存启动连接和密钥：`MUMU_DATABASE_DSN`、`MUMU_WEB_ADMIN_KEY`、`MUMU_SECRET_KEY`、`MUMU_ONEBOT_WS_URL`、`MUMU_ONEBOT_TOKEN`、端口、媒体目录和日志级别。
- 人格预设、对话、压缩、并发和媒体参数保存在 PostgreSQL，由管理后台维护；模型 API Key 和 MCP 配置加密落库。

```bash
# 1. 准备环境变量
cp .env.example .env
# 填写数据库连接、后台密码、MUMU_SECRET_KEY 和 OneBot 连接

# 2. 启动
./mumu-bot
```

启动后访问 `/admin`。如果还没有模型，后台仍可使用；在“设置 → 模型管理”添加模型并激活后，机器人会在下一轮开始工作。修改设置不需要重启进程。

模型管理的两个推理参数只在两个下拉框中选择：`thinking.type`（不传/enabled/disabled）和 `reasoning.effort`（不传/low/medium/high/xhigh/max）。

数据库使用 PostgreSQL 18。应用启动时会启用 `pg_trgm`，再自动创建或校验数据结构；不需要 pgvector 或向量模型。

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
```

当前版本使用 v17 语义层，只支持空数据库初始化；旧 schema 会明确拒绝启动，不会自动迁移或删除数据。升级前请备份数据库与媒体目录，并按要求重新初始化数据库。

默认情况下服务监听 `7468` 端口，可通过 `MUMU_SERVER_PORT` 修改。未设置后台密钥时，后台会保持关闭状态。

## 🎭 人格预设

后台「人格预设」里可以查看、编辑、克隆和切换。空库启动时只有一份内置预设，随版本更新、不可编辑；克隆一份之后随便改。

一份预设包含两段文本：**人格卡**（她是谁、怎么说话）与**仿真提示词**（协议、时序、安全）。两段都用行首标记区分场合：`[群]`、`[私]`，不写标记就是通用；后台用底色显示，不露标记。

预设切换不会打断正在进行的对话：它会在一段对话总结之后生效，所以切换前后她不会中途换一张脸。

如果你调出了更自然、更稳定的写法，欢迎通过 Issue 分享使用场景和效果，或直接提交 Pull Request 一起完善。

## 🧠 上下文与话题

主聊天和同步话题压缩共用当前模型。主 Agent 每轮默认最多 50 步，可调整；压缩器步数独立配置。模型请求不设置整体超时，只对连接和 TLS 握手上限 10 秒。输入达到上下文窗口 90% 时同步整理旧交互，目标 40%，不超过 55% 才提交。整理成功后只删除已摘要覆盖的 transcript 工作集，消息原文、媒体和已发送消息永久保留。

每段上下文有"代"的概念：一代之内只往后追加，不改也不删；只有压缩换代时才重建开头那段（人格、群说明、上下文摘要）。

话题系统同时承担上下文表示、后台浏览和分层查询：`searchTopics` 找话题，`readTopic` 看摘要与关键消息，`readHistory` 再按 OneBot 消息 ID 读取原文前后文。

## 🔧 MCP 工具扩展

在管理后台的“设置 → MCP 服务器”接入外部 MCP 服务，支持 SSE 和 Stdio 两种传输方式。服务器配置整体加密保存；加载后的扩展工具会同时提供给群聊和好友私聊，并统一以 `mcp-服务名-工具名` 命名，避免和内置工具或其它服务重名。

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
