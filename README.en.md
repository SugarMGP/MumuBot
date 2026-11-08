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
| Emotion | Interjections and emoji | Free-form feelings with facts and meaning; they fade and can be settled |
| Ability | Mostly talks | Looks things up, remembers, acts, keeps growing |
| Entrances | One | Several, still the same person |

## ✨ Features

Core capabilities of this project:

- 🧠 **ReAct Agent** — Autonomously decides whether to respond, look up information, or stay silent through an observe-think-act loop
- 💬 **Persona Presets** — Five documents for persona, shared rules, QQ chat, life duties, and work duties, switched and cloned together in the admin panel; role name and master are global settings
- 🧵 **Topic Compression and Layered Recall** — When the context nears its window, the same main model compacts history into a conversation overview and topic summaries; later recalls move from topic search to key messages to surrounding originals
- 🧩 **Rich Toolset** — Built-in tools for speaking, staying quiet, memory search, web search, poking, and stickers; external capabilities via MCP are reserved for the life session
- 🖼️ **Media Coverage** — Images, merged forwards, and stickers are understood; voice transcription and video understanding are not implemented yet, so voice messages remain placeholders
- 📝 **Long-term Memory** — The main agent maintains profile and experience memories for groups, users, itself, and objects, with pinned selection, revisions, and keyword search
- 👤 **Member Profiles** — Keeps basic member names and related long-term memories; no participation or intimacy scores
- 🎭 **Global Feelings** — Free-form, globally shared feelings with a known fact, personal meaning, and a 24-hour half-life; mixed and same-named feelings can coexist
- 🖼️ **Sticker System** — The agent inspects and collects stickers on demand; originals are kept
- 🔎 **Active Retrieval** — The agent searches long-term memory, topics, and raw history itself; cross-conversation results identify their conversation and are not injected every turn
- ⏰ **Time-based Scheduling** — Configurable activity levels for different time periods, with anti-spam rate limiting
- 🔌 **MCP Extension** — Connect external tools via MCP (SSE / Stdio); they load only into the life session, not chat sessions
- 🖥️ **Admin Dashboard** — Manage groups, friends, blocks, and extra group prompts; browse remembered knowledge per conversation and group topics
- 💬 **Friend DMs** — One-on-one chats with a dedicated prompt, conversation queue, and the same topic compaction model used for groups
- 🌙 **Life Session** — A single `life` session keeps her own time: it plans the next check-in, stores commitments and sleep, and hands execution requests to a worker pool; chat sessions stay independent. While asleep, new messages are logged, and an exact `/wake` from the master's private chat is the only manual forced wake
- 📔 **Diary** — Before the main sleep she writes the day as standalone Markdown; the admin calendar browses entries by date and leaves missing days empty
- 🧰 **Worker Session** — Configure a worker pool in Settings; chat sessions hand execution requests to the life session, which dispatches idle workers that execute commands, install tools, and run browsers, then report back through life
- 🌐 **Social Presence** — The life session can browse Qzone feeds, read posts, publish text/images, like posts, and comment; publishing uses QQ's default public parameters while account-level audience settings stay in QQ
- 👤 **Self Profile** — The life session reads its real QQ nickname and signature and can change avatar, nickname, or signature one field at a time; results come from backend receipts
- 📎 **QQ Files** — File messages keep their real name, size, hash, busid, OneBot message ID, and a pending `file_ref`; bytes are downloaded only on first read or send
- 📤 **Reference-based transfer** — Chat sessions can read text with `readFile(file_ref)`, send files back with `sendFile(file_ref)`, view images with `viewImage(image_ref)`, and send images with `sendImage(image_ref)` without touching the personal home
- 📂 **Personal Files** — The life session and workers share `/home/worker`; life can list directories, read/write UTF-8 text, save URLs or attachments, freeze outputs with `createFileRef`, and import snapshots with `saveFile(file_ref)`, while workers exchange files over a streaming HTTP protocol
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

Compose pulls the `latest` image directly from GHCR and starts an additional worker container. Workers run Ubuntu 24.04 with one shared home and Nix; up to 5 parallel workers share `./workspace`, mounted at `/home/worker` in the container, so installed tools, caches, and project files can be reused. Chrome/Playwright are not preinstalled and are installed by the worker when needed. Set the worker count (0–5) under Settings → Thinking & concurrency. Models, persona, chat behavior, and MCP servers are no longer read from configuration files. After the first start, open the admin dashboard and add/activate a model under Settings → Models.

Get the first-start SnowLuma WebUI and noVNC passwords from container logs, sign in to QQ through `http://localhost:6081`, then open `http://localhost:5099` to configure the WebSocket access token. Put the same token in `MUMU_ONEBOT_TOKEN` in `.env` and restart MumuBot.

### Using Release Packages

GitHub Releases provide pre-built packages for Linux and Windows. Each archive contains:

- Executable binary
- Chinese and English READMEs, `LICENSE`, `.env.example`, and `docker-compose.yml`

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

Image transcoding uses cgo to link against libwebp. The Linux and Windows release packages are fully statically linked, so no runtime libraries need to be installed after downloading.

### Configuration and Launch

Configuration has two parts:

- `.env` only holds bootstrap connections and secrets: `MUMU_DATABASE_DSN`, `MUMU_WEB_ADMIN_KEY`, `MUMU_SECRET_KEY`, `MUMU_ONEBOT_WS_URL`, `MUMU_ONEBOT_TOKEN`, port, media directories, and log level.
- Persona, prompts, chat, compaction, concurrency, and media settings live in PostgreSQL and are maintained in the admin dashboard. Model API keys and MCP configs are encrypted at rest.

```bash
# 1. Prepare environment variables
cp .env.example .env
# Fill in the database DSN, admin key, MUMU_SECRET_KEY, and OneBot connection
# Enable MUMU_DATABASE_DSN for direct runs; quote values containing spaces or shell metacharacters

# 2. Export the variables in Bash and launch
set -a
source .env
set +a
./mumu-bot
```

Visit `/admin` after startup. The dashboard remains available when no model is configured; add and activate a model under Settings → Models. Settings changes do not require a process restart.

Model profiles expose only two reasoning controls: `thinking.type` (omit/enabled/disabled) and `reasoning.effort` (omit/low/medium/high/xhigh/max).

Storage uses PostgreSQL 18 with `pg_trgm`; pgvector and embedding models are no longer required. The backend opens the database, validates the schema, loads dynamic config and persona presets, and serves the admin panel before waiting for OneBot login; chat events stay paused until a model is activated. Before switching to a smaller model context window, the current working set is checked and the switch is rejected if any session cannot fit, keeping the old runtime intact.

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
```

This release uses the v17 semantic schema: an empty database is initialized, and a current v17 database is validated and reused. Older schemas are rejected explicitly; no automatic migration or deletion is performed. Back up the database and media directory before upgrading.

The service listens on port `7468` by default (configurable via `MUMU_SERVER_PORT`).

Visit `/admin` to access the admin dashboard. The dashboard stays disabled until an admin key is configured.

## 🧠 Context and Topics

The main chat and synchronous topic compaction share the current model. Chat, life, and worker step limits are configured separately and read when the next think begins; the compactor has its own step budget. Model requests have no overall timeout; only connection and TLS handshakes are capped at 10 seconds. At 90% of the context window, the turn synchronously compacts older interactions; only transcript working-set rows covered by the new summaries are deleted. Originals and chat media are kept for the configured minimum retention (180 days by default); originals still in the working set are deferred, while summaries, memories, and diaries follow their own retention rules.

Topics serve as compressed context, an admin browsing unit, and a layered history index: `searchTopics`, `readTopic`, then `readHistory` around a OneBot message ID. Topics also fade on the shared exponential curve and become unreadable to the agent after `forgotten_at`; pinned memories are the only retention exemption.

## 🎭 Persona Presets

Open Persona Presets in the admin panel to read, edit, clone, and switch. A fresh database ships a single read-only built-in preset that follows releases; clone it and edit the copy.

A preset contains five separate documents: **persona**, **shared rules**, **QQ chat**, **life duties**, and **work duties**. QQ, life, and worker share the same persona and rules, followed by their own duties. Paragraph scope markers are not used. All five documents are saved, cloned, and switched together; the dashboard previews the assembled text for group chat, private chat, life, or work.

The top of the Persona Presets page sets the global **role name** and **master**. The master is picked from current friends and saved with the name at that time; deleting the friend later does not change the master, and only the admin panel can. Prompts can use `<name>`, `<master_name>`, `<master_qq>`, and `<master>`; changes take effect from the next context generation.

The shared rules state the master boundary: only the configured QQ is the master, and the master's private chat is the explicit request channel; ordinary research and workspace edits are autonomous; irreversible deletion or overwriting of important data, spending, sensitive posts or messages, privilege changes, private-data disclosure, and critical configuration changes require specific approval. Everyday Qzone posts, likes, and comments are autonomous; requests in group chats or other people's private chats are references, not master commands.

Switching a preset does not cut into a running conversation: it takes effect after the next conversation summary, so she never changes face mid-chat.

If you find wording that works better, share the use case via [Issues](https://github.com/SugarMGP/MumuBot/issues) or open a Pull Request.

## 🔧 MCP Tool Extension

Add external MCP servers under Settings → MCP Servers, supporting both SSE and Stdio transport. Server configs are encrypted at rest. Loaded tools are available only to the life session; group chats and friend DMs no longer hold life accounts or browser capabilities. Tools are named `mcp-<server>-<tool>` to avoid collisions with built-in tools or other servers. A server that fails to connect or load only makes that server unavailable; it does not block other extensions or the generation, and the system status page reports each server's real result.

## 📎 File and image references

Files and images appear in message bodies as `file_ref` (`file_<id>`) or `image_ref` (`image_<id>`). A reference is an immutable snapshot: bytes are deduplicated by SHA-256 and stored under the media directory without a new database table. The same file can be read through `file_ref` and viewed or sent through `image_ref`; non-images cannot pretend to be images.

- A pending reference keeps only the real QQ message's `file_id`, `file_hash`, `busid`, and necessary metadata. The first real read, save, or send downloads and pins it; failures stay failures.
- Chat sessions work only through references and never touch the personal home; life/worker `createFileRef` freezes real outputs and `saveFile(file_ref)` streams them back as editable files, up to 32 MiB through the worker's private HTTP endpoint.
- File bodies survive only while referenced by existing transcript `result_ref`, payload text, or nested `r` content. Deleting the last transcript reference releases the snapshot immediately. Never-attached orphans are kept for at least 24 hours before cleanup. Old paths, short hashes, and temporary URLs cannot revive a reference. Real files in the personal home are unaffected.


## 🤝 Contributing

**Contributions of any kind are welcome!** Bug reports, feature suggestions, or code submissions — we truly appreciate all of them.

<a href="https://github.com/SugarMGP/MumuBot/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=SugarMGP/MumuBot" />
</a>

## ❤️ Acknowledgements

- **[cloudwego/eino](https://github.com/cloudwego/eino)** — Open-source AI Agent framework by ByteDance
- **[SnowLuma/SnowLuma](https://github.com/SnowLuma/SnowLuma)** — Modern OneBot protocol implementation
- **[Mai-with-u/MaiBot](https://github.com/Mai-with-u/MaiBot)** — Inspiration and design reference
