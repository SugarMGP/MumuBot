<p align="center">
  <h1 align="center">MumuBot</h1>
  <p align="center">A cyber QQ group friend that chats, remembers, and recalls conversations by topic</p>
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
  <a href="README.md">中文</a> · <b>English</b>
  <!-- /language-selector -->
</p>

> [!WARNING]
> - The project is under active development. Configuration and behavior may change between versions.
> - QQ bots carry account restriction risks. Use at your own discretion.
> - AI model API calls consume tokens. Control usage as needed.

## 🤔 What is MumuBot

MumuBot is an AI companion that lives in your QQ group chats and friend DMs. Unlike typical Q&A bots, MumuBot decides on its own when to speak up and when to stay quiet. It remembers what has been discussed, compresses long conversations into searchable topics, and gradually builds an understanding of each member through everyday conversations.

In short: it tries to behave like an actual person in the group, not an on-demand tool.

## ✨ Features

Core capabilities of this project:

- 🧠 **ReAct Agent** — Autonomously decides whether to respond, look up information, or stay silent through an observe-think-act loop
- 💬 **Human-like Chat** — Customizable personality, language style, and interests; speaks more like a real group member
- 🧵 **Topic Compression and Layered Recall** — When the context nears its window, the same main model compacts history into a conversation overview and topic summaries; later recalls move from topic search to key messages to surrounding originals
- 🧩 **Rich Toolset** — Built-in tools for speaking, staying quiet, memory search, web search, poking, and stickers, with more available through MCP
- 📝 **Long-term Memory** — The main agent maintains profile and experience memories for groups, users, itself, and objects, with pinned selection, revisions, and keyword search
- 👤 **Member Profiles** — Keeps basic member names and related long-term memories; no participation or intimacy scores
- 🎭 **Specific Emotions** — Twelve emotion kinds plus energy and sociability, with lazy time-based decay
- 🖼️ **Sticker System** — The agent inspects and collects stickers on demand; originals are kept
- 🔎 **Active Retrieval** — The agent searches long-term memory, topics, and raw history itself; cross-conversation results identify their conversation and are not injected every turn
- ⏰ **Time-based Scheduling** — Configurable activity levels for different time periods, with anti-spam rate limiting
- 🔌 **MCP Extension** — Connect external tools via MCP protocol (SSE / Stdio) for unlimited capability expansion
- 🖥️ **Admin Dashboard** — Manage groups, friends, blocks, and extra group prompts; browse remembered knowledge per conversation and group topics
- 💬 **Friend DMs** — One-on-one chats with a dedicated prompt, conversation queue, and the same topic compaction model used for groups
- 📊 **Health Check** — Provides a `/health` endpoint for deployment and operations integration

## 🚀 Quick Start

### Prerequisites

| Dependency | Purpose |
|------|------|
| Go 1.26.5+ | Build and run |
| Bun 1.3.14+ | Build frontend assets (only needed for source builds) |
| PostgreSQL 18 | Stores messages, memory, topics, checkpoints, and emotions using `pg_trgm` |
| SnowLuma | OneBot 11 protocol implementation |
| LLM API | OpenAI Responses compatible (`/responses`), must support tool calling |

### Docker Compose

Compose starts MumuBot, standard PostgreSQL 18, and SnowLuma together:

```bash
cp .env.example .env
# Fill in database, admin, encryption key, and OneBot connection details
docker compose up -d
```

Compose pulls the `latest` image directly from GHCR. Models, persona, chat behavior, and MCP servers are no longer read from configuration files. After the first start, open the admin dashboard and add/activate a model under Settings → Models.

Get the first-start SnowLuma WebUI and noVNC passwords from container logs, sign in to QQ through `http://localhost:6081`, then open `http://localhost:5099` to configure the WebSocket access token. Put the same token in `MUMU_ONEBOT_TOKEN` in `.env` and restart MumuBot.

### Using Release Packages

GitHub Releases provide pre-built packages for Linux, Windows, and macOS. Each archive contains:

- Executable binary
- `README.md` and `LICENSE`

No separate frontend deployment is needed — admin dashboard assets are embedded in the binary.

### Building from Source

```bash
# 1. Clone the project
git clone https://github.com/SugarMGP/MumuBot.git
cd MumuBot

# 2. Install frontend dependencies, generate admin icons, and build dashboard assets
bun install --frozen-lockfile
bun run gen:icons
bun run build

# 3. Generate templ view code
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate ./internal/web/views

# 4. Build
go build -o mumu-bot .
```

Image transcoding uses cgo to link against libwebp. The Linux and Windows release packages are fully statically linked, so no runtime libraries need to be installed after downloading. macOS's system linker does not support fully static linking, so the artifacts dynamically depend on libwebp; run `brew install webp` before running them.

### Configuration and Launch

Configuration has two parts:

- `.env` only holds bootstrap connections and secrets: `MUMU_DATABASE_DSN`, `MUMU_WEB_ADMIN_KEY`, `MUMU_SECRET_KEY`, `MUMU_ONEBOT_WS_URL`, `MUMU_ONEBOT_TOKEN`, port, media directories, and log level.
- Persona, prompts, chat, compaction, concurrency, and media settings live in PostgreSQL and are maintained in the admin dashboard. Model API keys and MCP configs are encrypted at rest.

```bash
# 1. Prepare environment variables
cp .env.example .env
# Fill in the database DSN, admin key, MUMU_SECRET_KEY, and OneBot connection

# 2. Launch
./mumu-bot
```

Visit `/admin` after startup. The dashboard remains available when no model is configured; add and activate a model under Settings → Models. Settings changes do not require a process restart.

Model profiles expose only two reasoning controls: `thinking.type` (omit/enabled/disabled) and `reasoning.effort` (omit/low/medium/high/xhigh/max).

Storage uses PostgreSQL 18 with `pg_trgm`; pgvector and embedding models are no longer required.

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
```

This release uses the v17 semantic schema and only initializes an empty database. Older schemas are rejected explicitly; no automatic migration or deletion is performed. Back up the database and media directory before reinitializing.

The service listens on port `7468` by default (configurable via `MUMU_SERVER_PORT`).

Visit `/admin` to access the admin dashboard. The dashboard stays disabled until an admin key is configured.

## 🧠 Context and Topics

The main chat and synchronous topic compaction share the current model. The main Agent runs at most 50 steps per turn by default, configurable; the compactor has its own step budget. Model requests have no overall timeout; only connection and TLS handshakes are capped at 10 seconds. At 90% of the context window, the turn synchronously compacts older interactions, targeting 40% and rejecting results above 55%. Only transcript working-set rows covered by the new summaries are deleted. Original messages, media, and sent messages are retained permanently.

Topics serve as compressed context, an admin browsing unit, and a layered history index: `searchTopics`, `readTopic`, then `readHistory` around a OneBot message ID.

## 🧠 Persona Template

Group and private persona templates are maintained separately under Settings → Persona. They share the same structure but stay independent: the group template keeps group-specific interaction rules, while the private template drops them and adds one-on-one rules. A code default is used when the database has no override.

If you find a prompt that works better, feel free to share your use case and results via [Issues](https://github.com/SugarMGP/MumuBot/issues), or submit a Pull Request to improve the template together.

## 🔧 MCP Tool Extension

Add external MCP servers under Settings → MCP Servers, supporting both SSE and Stdio transport. Server configs are encrypted at rest. Loaded tools are available to both group chats and friend DMs and are named `mcp-<server>-<tool>` to avoid collisions with built-in tools or other servers:


## 🤝 Contributing

**Contributions of any kind are welcome!** Bug reports, feature suggestions, or code submissions — we truly appreciate all of them.

<a href="https://github.com/SugarMGP/MumuBot/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=SugarMGP/MumuBot" />
</a>

## ❤️ Acknowledgements

- **[cloudwego/eino](https://github.com/cloudwego/eino)** — Open-source AI Agent framework by ByteDance
- **[SnowLuma/SnowLuma](https://github.com/SnowLuma/SnowLuma)** — Modern OneBot protocol implementation
- **[Mai-with-u/MaiBot](https://github.com/Mai-with-u/MaiBot)** — Inspiration and design reference
