<p align="center">
  <h1 align="center">MumuBot</h1>
  <p align="center">A digital life: she remembers, has moods, keeps her own time, and gets things done</p>
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

MumuBot is not "an LLM bolted onto QQ".

She has a continuous self: one set of memories, the things you have talked about, and a concrete opinion of each person. Right now she lives in a chat app: someone attached QQ to her so she can speak, look at pictures, send stickers, and remember people and events. Later she will get other hands: running errands, visiting other places, sitting on your desktop.

Which entrance you meet her through only changes where you see her. Who she is and what she remembers stay the same.

| | A typical chatbot | MumuBot |
|---|---|---|
| Who is speaking | A character generated per request | A continuous self that remembers where it stopped |
| Sense of time | Wakes only when called | Has her own rhythm, speaks up and holds back |
| Emotion | Interjections and emoji | Has causes, decays, passes |
| Ability | Mostly talks | Looks things up, remembers, acts, keeps growing |
| Entrances | One | Several, still the same person |

## ✨ Features

Core capabilities of this project:

- 🧠 **ReAct Agent** — Autonomously decides whether to respond, look up information, or stay silent through an observe-think-act loop
- 💬 **Persona Presets** — A persona card plus a simulation prompt, switchable and clonable from the admin panel
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
| Bun 1.4.4+ | Build frontend assets (only needed for source builds) |
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

No separate frontend deployment is needed — admin dashboard assets are embedded in the binary. The dashboard supports light/dark themes and Chinese/English, with preferences stored per browser.

### Building from Source

```bash
# 1. Clone the project
git clone https://github.com/SugarMGP/MumuBot.git
cd MumuBot

# 2. Install frontend dependencies and build the admin dashboard
cd web
bun install --frozen-lockfile
bun run build
cd ..

# 3. Build
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

## 🎭 Persona Presets

Open Persona Presets in the admin panel to read, edit, clone, and switch. A fresh database ships a single read-only built-in preset that follows releases; clone it and edit the copy.

A preset is two documents: a **persona card** (who she is, how she talks) and a **simulation prompt** (protocol, timing, safety). Both mark lines with `[群]` and `[私]` to separate group from private content; unmarked lines apply everywhere. The admin renders them with a tint instead of showing the markers.

Switching a preset does not cut into a running conversation: it takes effect after the next conversation summary, so she never changes face mid-chat.

If you find wording that works better, share the use case via [Issues](https://github.com/SugarMGP/MumuBot/issues) or open a Pull Request.

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
