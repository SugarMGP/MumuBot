<p align="center">
  <h1 align="center">MumuBot</h1>
  <p align="center">A cyber QQ group friend that chats, remembers, and blends into your community</p>
</p>

<p align="center">
  <a href="https://github.com/SugarMGP/MumuBot/releases"><img alt="Release" src="https://img.shields.io/github/v/release/SugarMGP/MumuBot?logo=github&style=flat-square"></a>
  <a href="https://github.com/SugarMGP/MumuBot/stargazers"><img alt="Stars" src="https://img.shields.io/github/stars/SugarMGP/MumuBot?color=ffcb47&style=flat-square"></a>
  <a href="https://github.com/SugarMGP/MumuBot/network/members"><img alt="Forks" src="https://img.shields.io/github/forks/SugarMGP/MumuBot?style=flat-square"></a>
  <a href="https://github.com/SugarMGP/MumuBot/blob/main/LICENSE"><img alt="License" src="https://img.shields.io/badge/license-MIT-blue?style=flat-square"></a>
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

MumuBot is an AI group member that lives in your QQ group chat. Unlike typical Q&A bots, MumuBot decides on its own when to speak up and when to stay quiet. It remembers what's been discussed, picks up on group slang and inside jokes, and gradually builds an understanding of each member through everyday conversations.

In short: it tries to behave like an actual person in the group, not an on-demand tool.

## ✨ Features

Core capabilities of this project:

- 🧠 **ReAct Agent** — Autonomously decides whether to respond, look up information, or stay silent through an observe-think-act loop
- 💬 **Human-like Chat** — Customizable personality, language style, and interests; speaks more like a real group member
- 🧵 **Topic Working Memory** — Continuously tracks current topics, summaries, participants, and open threads; can recall archived topics
- 🧩 **Rich Toolset** — 20+ built-in tools: speak, stay quiet, poke, react with emoji, send stickers, check group announcements, browse the web, and more
- 📝 **Long-term Memory** — Stores and retrieves facts, experiences, preferences, constraints, and goals via MySQL + Milvus
- 👤 **Member Profiles** — Records each person's speaking style, interests, activity level, and closeness
- 🎭 **Emotion System** — Three-dimensional mood (valence, energy, sociability) shifts naturally during conversation, affecting tone and activity
- 👀 **Multimodal Understanding** — Vision model recognizes image and video content
- 🖼️ **Sticker System** — Automatically collects stickers from the group; the agent decides when to use them
- 📖 **Continuous Learning** — Learns group chat atmosphere and popular memes, materializing into reviewable community culture data
- ⏰ **Time-based Scheduling** — Configurable activity levels for different time periods, with anti-spam rate limiting
- 🔌 **MCP Extension** — Connect external tools via MCP protocol (SSE / Stdio) for unlimited capability expansion
- 🖥️ **Admin Dashboard** — Overview, learning review, topic/memory/sticker management, and member profile browsing
- 📊 **Monitoring** — Health check and status endpoints for deployment and operations integration

## 🚀 Quick Start

### Prerequisites

| Dependency | Purpose |
|------|------|
| Go 1.26 | Build and run |
| Node.js 22+ | Build frontend assets (only needed for source builds) |
| MySQL | Store memories, message logs, member profiles |
| Milvus | Vector database for long-term memory, style cards, and archived topic retrieval |
| NapCat / go-cqhttp | OneBot 11 protocol implementation |
| LLM API | OpenAI-compatible; must support tool calling and structured output |

### Using Release Packages

GitHub Releases provide pre-built packages for Linux, Windows, and macOS. Each archive contains:

- Executable binary
- `config/config.yaml` example configuration
- `config/persona.prompt` persona prompt template
- `config/mcp.json` example configuration
- `README.md` and `LICENSE`

No separate frontend deployment is needed — admin dashboard assets are embedded in the binary. To customize the persona prompt, edit `config/persona.prompt` directly from the archive.

### Building from Source

```bash
# 1. Clone the project
git clone https://github.com/SugarMGP/MumuBot.git
cd MumuBot

# 2. Install frontend dependencies and build dashboard assets
npm ci
npm run build

# 3. Generate templ view code
go run github.com/a-h/templ/cmd/templ@latest generate ./internal/web/views

# 4. Build
go build -o mumu-bot .
```

### Configuration and Launch

If you're using a GitHub Release archive, edit the bundled `config/config.yaml`, `config/persona.prompt`, and `config/mcp.json` directly — skip the copy step below.

```bash
# 1. Copy example configs and edit as needed
cp config/config.example.yaml config/config.yaml
cp config/mcp.example.json config/mcp.json

# 2. Edit config (fill in model, database, OneBot details, etc.)
# Sensitive values can also be set via environment variables:
#   MUMU_MODEL_HIGH_API_KEY             - High-tier model API key
#   MUMU_MODEL_MID_API_KEY              - Mid-tier model API key
#   MUMU_MODEL_LOW_API_KEY              - Low-tier model API key
#   MUMU_EMBEDDING_API_KEY              - Embedding model API key
#   MUMU_VISION_API_KEY                 - Vision model API key
#   MUMU_ONEBOT_TOKEN                   - OneBot access token
#   MUMU_MYSQL_PASSWORD                 - MySQL password

# The static persona prompt template is at config/persona.prompt
# persona.name, persona.qq, persona.alias_names, persona.interests are still set in config/config.yaml

# 3. Launch
./mumu-bot
```

The service listens on port `8080` by default (configurable).

Visit `/admin` to access the admin dashboard. The dashboard stays disabled until a secret key is configured.

## 🧠 Persona Template

`config/persona.prompt` is the default persona prompt template that controls MumuBot's personality, speaking rhythm, and reply style. You can adjust it to fit your group's vibe and iterate based on real conversation results.

If you find a prompt that works better, feel free to share your use case and results via [Issues](https://github.com/SugarMGP/MumuBot/issues), or submit a Pull Request to improve the template together.

## 🔧 MCP Tool Extension

Edit `config/mcp.json` to connect external MCP servers, supporting both SSE and Stdio transport:

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

Contributions of any kind are welcome — bug reports, feature suggestions, or code submissions.

<a href="https://github.com/SugarMGP/MumuBot/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=SugarMGP/MumuBot" />
</a>

## ❤️ Acknowledgements

- **[cloudwego/eino](https://github.com/cloudwego/eino)** — Open-source AI Agent framework by ByteDance
- **[NapNeko/NapCatQQ](https://github.com/NapNeko/NapCatQQ)** — Modern OneBot protocol implementation
- **[Mai-with-u/MaiBot](https://github.com/Mai-with-u/MaiBot)** — Inspiration and design reference
