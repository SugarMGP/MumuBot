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

## 🌱 她住在 QQ 里

沐沐是运行在 QQ 群和好友私聊里的智能体。她会自己判断什么时候接话、什么时候安静，记得聊过的人和事，有会自然淡化的情绪，也会在聊天之外安排自己的生活。

> 从哪个入口遇见她，都只是“在哪儿见到”；她是谁、记得什么，还是同一份。

## ✨ 她能做什么

- 🗣️ **像个群友一样聊天** — 自己判断接话还是沉默；短句、表情、@、回复、撤回、戳一戳
- 🧠 **记得住** — 长期记忆和话题整理；聊过的人、群、事情都能再找回来，置顶的记忆一直留着
- 🎭 **有情绪** — 感受有原因、有意义，会随时间和相处自然淡化；心情和精力影响表达
- 🌙 **有自己的生活** — 生活会话安排日程、睡眠和日记，会在没有消息的时候主动出现
- 🧰 **能办事** — 查梗百科；生活线可查网页；收发文件图片；长任务交给 worker 执行命令、装工具、跑浏览器
- 🌐 **会社交** — QQ 空间看动态、点赞、评论、发表；改自己的昵称、签名和头像
- 🖼️ **看得见** — 图片、合并转发、表情包都能读；语音和视频理解暂未接入
- 🔌 **可扩展** — MCP 接入外部工具，能力可以继续长出来
- 🖥️ **管得住** — 后台查看记忆、话题、成员、会话、表情和日志
- 🔐 **有边界** — 只有后台配置的 QQ 是主人；删除重要资料、支付、敏感发布等会先征求同意

## 🚀 快速开始

### 需要准备

- 一个 QQ 账号和 OneBot 11 实现（用 Compose 时自带 SnowLuma）
- PostgreSQL 18（用 Compose 时自带）
- 支持工具调用的模型 API（OpenAI Responses、Chat Completions 或 Gemini 兼容）
- Docker（使用 Compose 时）

### 用 Docker Compose（推荐）

```bash
cp .env.example .env
# 填数据库连接、后台密钥、MUMU_SECRET_KEY 和 OneBot 连接
docker compose up -d
```

Compose 会启动主服务、PostgreSQL 18、SnowLuma 和 worker；worker 共用 `./workspace`，工具、缓存和项目文件直接复用，数量在后台调整（0–5）。

### 第一次使用

1. 从容器日志取 SnowLuma WebUI 与 noVNC 的密码，在 `http://localhost:6081` 登录 QQ；在 `http://localhost:5099` 配置 WebSocket 令牌，把同一个令牌填进 `.env` 的 `MUMU_ONEBOT_TOKEN`，重启 MumuBot
2. 打开 `http://localhost:7468/admin`，在“设置 → 模型档案”添加模型，并选择 QQ、生活、工作使用的模型
3. 在“人格预设”里调整她的人格；需要外部工具时到“设置 → MCP 服务器”接入

### 使用发布包

GitHub Release 提供 Linux、Windows 打包产物：可执行文件、中英文 README、`LICENSE`、`.env.example` 和 `docker-compose.yml`；管理后台资源已内嵌，开箱即用。

### 从源码构建

源码构建需要 Go 1.26.11+ 和 Bun 1.4.4+：

```bash
git clone https://github.com/SugarMGP/MumuBot.git
cd MumuBot

cd web
bun install --frozen-lockfile
bun run build
cd ..

# Debian/Ubuntu 图片转码依赖
sudo apt-get install -y libwebp-dev

go build -o mumu-bot .
```

图片转码通过 cgo 链接 libwebp；发布包已静态链接。

## ⚙️ 配置

`.env` 保存启动连接和密钥，完整示例见 `.env.example`；人格、对话、模型、MCP 等都在后台维护，API Key 与 MCP 配置加密保存。

> [!IMPORTANT]
> 备份数据库时同时保留 `MUMU_SECRET_KEY`。更换主密钥后，已有密文仍需原密钥解密。

数据库会在首次启动时初始化，受支持的旧版本会自动升级；升级前备份数据库和媒体目录。

## 🤝 贡献

**欢迎任何形式的贡献！** 无论是提交 Bug 报告、功能建议，还是直接提交代码，我们都非常感谢。

<a href="https://github.com/SugarMGP/MumuBot/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=SugarMGP/MumuBot" />
</a>

## ❤️ 致谢

- **[trpc-group/trpc-agent-go](https://github.com/trpc-group/trpc-agent-go)** — 腾讯开源 AI Agent 框架
- **[SnowLuma/SnowLuma](https://github.com/SnowLuma/SnowLuma)** — 现代化 OneBot 协议实现
- **[Mai-with-u/MaiBot](https://github.com/Mai-with-u/MaiBot)** — 灵感来源和设计参考

## 📄 许可

GPL-3.0，见 [LICENSE](LICENSE)。
