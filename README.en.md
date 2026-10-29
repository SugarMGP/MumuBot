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
- 📝 **Long-term Memory** — The main agent maintains facts, preferences, constraints, goals, terms, expressions, and aliases with revisions, core-memory selection, and keyword/fuzzy search
- 👤 **Member Profiles** — Aggregates member knowledge and participation; intimacy remains a separate global signal
- 🎭 **Specific Emotions** — Twelve emotion kinds plus energy and sociability, with lazy time-based decay
- 🖼️ **Sticker System** — The agent inspects and collects stickers on demand; originals are kept
- 🔎 **Active Retrieval** — The agent searches long-term memory, topics, and raw history itself; cross-conversation results keep source labels and are not injected every turn
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
# Fill in database, admin, and model settings in .env
docker compose up -d
```

Compose pulls the `latest` image directly from GHCR. On first start, the container creates any missing `config.yaml`, `persona_group.prompt`, `persona_private.prompt`, and `mcp.json` files in the configuration directory without overwriting existing files.

Restart the service after editing the generated configuration files. Get the first-start SnowLuma WebUI and noVNC passwords from container logs, sign in to QQ through `http://localhost:6081`, then open `http://localhost:5099` to configure the WebSocket access token. Put the same token in `MUMU_ONEBOT_TOKEN` in `.env` and restart MumuBot.

### Using Release Packages

GitHub Releases provide pre-built packages for Linux, Windows, and macOS. Each archive contains:

- Executable binary
- `config/config.yaml` example configuration
- `config/persona_group.prompt` group chat persona template
- `config/persona_private.prompt` private chat persona template
- `config/mcp.json` example configuration
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

If you're using a GitHub Release archive, edit the bundled `config/config.yaml`, `config/persona_group.prompt`, `config/persona_private.prompt`, and `config/mcp.json` directly — skip the copy step below.

```bash
# 1. Copy example configs and edit as needed
cp config/config.example.yaml config/config.yaml
cp config/mcp.example.json config/mcp.json

# 2. Edit config (fill in model, database, OneBot details, etc.)
# Sensitive values can also be set via environment variables:
#   MUMU_MODEL_HIGH_BASE_URL / API_KEY / MODEL - The single main model endpoint
#   MUMU_ONEBOT_TOKEN                   - OneBot access token
#   MUMU_DATABASE_DSN                   - PostgreSQL connection string

# The static persona prompt template is at config/persona_group.prompt; the private chat prompt template is at config/persona_private.prompt
# persona.name, persona.alias_names, and persona.interests are still set in config/config.yaml
# The bot QQ is detected from OneBot; interests only shape persona context and do not change trigger probability

# 3. Launch
./mumu-bot
```

Storage uses PostgreSQL 18 with `pg_trgm`; pgvector and embedding models are no longer required.

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
```

To upgrade from an older release, first use the old program to bring the database to v14, then start this version to run the v15 and v16 migrations. The v15 migration preserves old evidence, relations, learning watermarks, and private summaries as `legacy_*` history tables; v16 removes the obsolete working-note table. Back up the database and media directory before upgrading.

The service listens on port `7468` by default (configurable).

Visit `/admin` to access the admin dashboard. The dashboard stays disabled until a secret key is configured.

## 🧠 Context and Topics

The main chat and synchronous topic compaction share one model. At 90% of the context window, the current turn may wait up to 300 seconds for a complete compaction. The target is 40%; results above 55% are rejected. Only transcript working-set rows covered by the new summaries are deleted. Original messages, media, and sent messages are retained permanently.

Topics serve as compressed context, an admin browsing unit, and a layered history index: `searchTopics`, `readTopic`, then `readHistory` around an `h` message ID.

## 🧠 Persona Template

`config/persona_group.prompt` and `config/persona_private.prompt` are the static persona templates for group chats and private chats. They share the same structure but stay independent: the group template keeps group-specific interaction rules, while the private template drops them and adds one-on-one rules. Tune each file separately and iterate based on real conversations.

If you find a prompt that works better, feel free to share your use case and results via [Issues](https://github.com/SugarMGP/MumuBot/issues), or submit a Pull Request to improve the template together.

## 🔧 MCP Tool Extension

Edit `config/mcp.json` to connect external MCP servers, supporting both SSE and Stdio transport. Loaded tools are available to both group chats and friend DMs and are named `mcp-<server>-<tool>` to avoid collisions with built-in tools or other servers:

```json
{
    "servers": [
        {
            "name": "example-mcp-server-sse",
            "enabled": false,
            "type": "sse",
            "url": "http://localhost:3333/sse",
            "tool_name_list": [],
            "custom_headers": {
                "Authorization": "Bearer YOUR_TOKEN_HERE"
            }
        },
        {
            "name": "example-mcp-server-stdio",
            "enabled": false,
            "type": "stdio",
            "command": "",
            "args": [],
            "env": []
        }
    ]
}
```

## 🤝 Contributing

**Contributions of any kind are welcome!** Bug reports, feature suggestions, or code submissions — we truly appreciate all of them.

<a href="https://github.com/SugarMGP/MumuBot/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=SugarMGP/MumuBot" />
</a>

## ❤️ Acknowledgements

- **[cloudwego/eino](https://github.com/cloudwego/eino)** — Open-source AI Agent framework by ByteDance
- **[SnowLuma/SnowLuma](https://github.com/SnowLuma/SnowLuma)** — Modern OneBot protocol implementation
- **[Mai-with-u/MaiBot](https://github.com/Mai-with-u/MaiBot)** — Inspiration and design reference
