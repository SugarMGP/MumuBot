<p align="center">
  <h1 align="center">沐沐 MumuBot</h1>
  <p align="center">一个会聊天、会记事、会按话题回忆的赛博 QQ 群友</p>
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

## 🤔 什么是沐沐

沐沐是一个运行在 QQ 群和好友私聊里的智能体。跟常见的问答机器人不同，沐沐会自己判断什么时候该说话、什么时候闭嘴，会记住聊过的事，会把长对话整理成可检索的话题，也会随着聊天慢慢建立对每个群友的了解。

简单说：它试着像一个真实的人一样待在群里，而不是一个随叫随到的工具。

## ✨ 特性

本项目具有以下核心特性：

- 🧠 **ReAct 智能体** — 通过观察、思考、行动循环，自主判断是否接话、查询信息或保持沉默
- 💬 **拟人对话** — 可自定义人格、语言风格和兴趣话题，说话更接近真实群友
- 🧵 **话题压缩与分层回忆** — 接近上下文窗口时由同一主模型完整整理成会话概览与话题摘要；之后可先查话题、再看关键消息，最后读取原文前后文
- 🧩 **丰富工具集** — 内置工具覆盖发言、沉默、记忆检索、网页搜索、戳一戳和表情包等场景
- 📝 **长期记忆** — 主 Agent 按群、用户、自己或具体事物维护 profile/experience，支持置顶、版本修订和关键词检索
- 👤 **群友画像** — 聚合成员昵称、群内称呼与关于 TA 的长期记忆，不把发言量或好感度做成隐藏分数
- 🎭 **具体情感** — 喜悦、好奇、亲近、感动、思念、吃醋、担忧、安心、委屈、失落、不满、愧疚共 12 类，配合精力与交流意愿做惰性衰减
- 🖼️ **表情包系统** — 智能体按需查看并收藏群内表情，原图留存不失真
- 🔎 **主动检索** — 需要回忆时自己查长期记忆、话题和历史原文；跨会话结果标明所属会话，不自动塞进每轮上下文
- ⏰ **时段策略** — 可配置不同时间段的发言活跃度，同时支持防话痨动态限流
- 🔌 **MCP 扩展** — 支持通过 MCP 协议接入外部工具，无限扩展能力
- 🖥️ **管理后台** — 统一管理群聊与好友、拉黑状态和群聊额外提示，按会话查看已记住的内容与群聊话题
- 💬 **好友私聊** — 支持好友一对一聊天，使用独立提示词、会话队列和与群聊统一的话题压缩模型
- 📊 **健康检查接口** — 提供 `/health` 健康检查，方便接入部署和运维流程

## 🚀 快速开始

### 环境要求

| 依赖 | 说明 |
|------|------|
| Go 1.26.5+ | 编译运行 |
| Bun 1.3.14+ | 构建前端资源（仅从源码构建时需要） |
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

Compose 会直接拉取 GHCR 上的 `latest` 镜像。模型、人格、对话行为和 MCP 服务器不再从配置文件读取；首次启动后打开管理后台，在“设置 → 模型管理”添加并激活模型即可。

从容器日志获取 SnowLuma WebUI 与 noVNC 的首次密码，通过 `http://localhost:6081` 登录 QQ，再访问 `http://localhost:5099` 配置 WebSocket 的访问令牌；将同一个令牌填入 `.env` 的 `MUMU_ONEBOT_TOKEN` 后重启 MumuBot。

### 使用发布包

GitHub Release 提供 Linux、Windows、macOS 的打包产物。归档内包含：

- 可执行文件
- `README.md` 和 `LICENSE`

运行发布包时不需要额外部署前端静态文件，管理后台所需资源已经内嵌进二进制。

### 从源码构建

```bash
# 1. 克隆项目
git clone https://github.com/SugarMGP/MumuBot.git
cd MumuBot

# 2. 安装前端依赖、生成后台图标并构建后台资源
bun install --frozen-lockfile
bun run gen:icons
bun run build

# 3. 生成 templ 视图代码
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate ./internal/web/views

# 4. 安装图片转码依赖
#   Debian/Ubuntu: sudo apt-get install -y libwebp-dev
#   macOS:         brew install webp pkgconf

# 5. 编译
go build -o mumu-bot .
```

图片转码走 cgo 链接 libwebp。Linux 与 Windows 的发布包整份静态链接，下载后不需要再装任何运行库；
macOS 的系统链接器不支持全静态，产物动态依赖 libwebp，运行前先 `brew install webp`。

### 配置与启动

配置分成两部分：

- `.env` 只保存启动连接和密钥：`MUMU_DATABASE_DSN`、`MUMU_WEB_ADMIN_KEY`、`MUMU_SECRET_KEY`、`MUMU_ONEBOT_WS_URL`、`MUMU_ONEBOT_TOKEN`、端口、媒体目录和日志级别。
- 人格、提示词、对话、压缩、并发和媒体参数保存在 PostgreSQL，由管理后台维护；模型 API Key 和 MCP 配置加密落库。

```bash
# 1. 准备环境变量
cp .env.example .env
# 填写数据库连接、后台密码、MUMU_SECRET_KEY 和 OneBot 连接

# 2. 启动
./mumu-bot
```

启动后访问 `/admin`。如果还没有模型，后台仍可使用；在“设置 → 模型管理”添加模型并激活后，机器人会在下一轮开始工作。修改设置不需要重启进程。

模型管理的两个推理参数只在两个下拉框中选择：`thinking.type`（不传/enabled/disabled）和 `reasoning.effort`（不传/low/medium/high/xhigh/max）。

数据库使用 PostgreSQL 18。应用启动时会启用 `pg_trgm`，再自动创建或校验数据结构；不再需要 pgvector 或向量模型。

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
```

当前版本使用 v17 语义层，只支持空数据库初始化；旧 schema 会明确拒绝启动，不会自动迁移或删除数据。升级前请备份数据库与媒体目录，并按要求重新初始化数据库。

默认情况下服务监听 `7468` 端口，可通过 `MUMU_SERVER_PORT` 修改。

访问服务的 `/admin` 路径进入管理后台。未设置后台密钥时，后台会保持关闭状态。

## 🧠 上下文与话题

主聊天和同步话题压缩共用当前模型。主 Agent 每轮默认最多 50 步，可调整；压缩器步数独立配置。模型请求不设置整体超时，只对连接和 TLS 握手上限 10 秒。输入达到上下文窗口 90% 时同步整理旧交互，目标 40%，不超过 55% 才提交。整理成功后只删除已摘要覆盖的 transcript 工作集，消息原文、媒体和已发送消息永久保留。

话题系统同时承担上下文表示、后台浏览和分层查询：`searchTopics` 找话题，`readTopic` 看摘要与关键消息，`readHistory` 再按 OneBot 消息 ID 读取原文前后文。

## 🧠 人格模板

群聊与私聊人格模板在管理后台的“设置 → 人格”分别维护。两份模板结构一致但完全独立：群聊模板保留群友式的互动规则，私聊模板去掉群聊专用条目、换成一对一的对话规则。代码内保留一份默认模板，数据库没有覆盖值时使用默认值。

如果你测出了更自然、更稳定、也更好用的 prompt，欢迎通过 Issue 分享使用场景和效果，或直接提交 Pull Request 一起完善这份模板。

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
