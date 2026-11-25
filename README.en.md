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

## 🌱 She lives in QQ

MumuBot is an agent that lives in QQ groups and friend DMs. It decides when to speak and when to stay quiet, remembers people and conversations, carries feelings that fade naturally, and runs its own life outside the chat window.

> Which entrance you meet her through only changes where you see her. Who she is and what she remembers stay the same.

## ✨ What she can do

Core capabilities of this project:

- 🗣️ **Chat like a group member** — Decides whether to speak or stay silent; short replies, stickers, mentions, replies, recalls, and pokes
- 🧠 **Remembers** — Long-term memory and topic summaries keep people, groups, and events findable; pinned memories stay
- 🎭 **Feels** — Feelings have a known fact and personal meaning, and fade with time and interaction; mood and energy shape expression
- 🌙 **Has her own life** — The life session plans schedules, sleep, and diary entries, and can show up when no one is talking
- 🧰 **Gets things done** — Meme search; the life session looks up the web; files and images; long tasks go to workers that run commands, install tools, and drive browsers
- 🌐 **Is social** — Reads friend feeds and full Qzone timelines, likes, comments, publishes, and updates its own nickname, signature, and avatar
- 🖼️ **Sees** — Understands images, merged forwards, and stickers; voice and video understanding are not connected yet
- 🔌 **Extends** — MCP connects external tools, so abilities can keep growing
- 🖥️ **Stays manageable** — The dashboard shows memory, topics, members, conversations, stickers, and logs
- 🔐 **Has boundaries** — Only the QQ configured in the dashboard is the master; deleting important data, payments, and sensitive posts ask for approval first

## 🚀 Quick Start

### Prepare

- A QQ account and an OneBot 11 implementation (Compose includes SnowLuma)
- PostgreSQL 18 (Compose includes it)
- An LLM API with tool calling (OpenAI Responses, Chat Completions, or Gemini compatible)
- Docker (for Compose)

### Docker Compose (recommended)

```bash
cp .env.example .env
# Fill in the database DSN, admin key, MUMU_SECRET_KEY, and OneBot connection
docker compose up -d
```

Compose starts the main service, PostgreSQL 18, SnowLuma, and a worker. Workers share `./workspace`, so tools, caches, and project files are reused; set the worker count (0–5) in the dashboard.

### First Run

1. Read the SnowLuma WebUI and noVNC passwords from the container logs, sign in to QQ at `http://localhost:6081`, configure the WebSocket token at `http://localhost:5099`, put the same token into `MUMU_ONEBOT_TOKEN` in `.env`, and restart MumuBot
2. Open `http://localhost:7468/admin`, add a model under Settings → Model profiles, and select the models for QQ, life, and work
3. Adjust her persona under Persona Presets; connect external tools under Settings → MCP servers when needed

### Release Packages

GitHub Releases provide Linux and Windows packages: executable, Chinese and English READMEs, `LICENSE`, `.env.example`, and `docker-compose.yml`. Dashboard assets are embedded, ready to use.

### Build from Source

Source builds need Go 1.26.11+ and Bun 1.4.4+:

```bash
git clone https://github.com/SugarMGP/MumuBot.git
cd MumuBot

cd web
bun install --frozen-lockfile
bun run build
cd ..

# Debian/Ubuntu image transcoding dependency
sudo apt-get install -y libwebp-dev

go build -o mumu-bot .
```

Image transcoding uses cgo and libwebp; release packages are statically linked.

## ⚙️ Configuration

`.env` holds bootstrap connections and secrets, with a full example in `.env.example`; persona, chat, models, and MCP live in the dashboard, where API keys and MCP configs are encrypted at rest.

> [!IMPORTANT]
> Keep `MUMU_SECRET_KEY` with every database backup. After changing the master key, existing ciphertext still needs the original key.

The database initializes on first start, and supported older versions upgrade automatically; back up the database and media before upgrading.

## 🤝 Contributing

**Contributions of any kind are welcome!** Bug reports, feature suggestions, or code submissions — we truly appreciate all of them.

<a href="https://github.com/SugarMGP/MumuBot/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=SugarMGP/MumuBot" />
</a>

## ❤️ Acknowledgements

- **[trpc-group/trpc-agent-go](https://github.com/trpc-group/trpc-agent-go)** — Tencent's open-source AI Agent framework
- **[SnowLuma/SnowLuma](https://github.com/SnowLuma/SnowLuma)** — Modern OneBot protocol implementation
- **[Mai-with-u/MaiBot](https://github.com/Mai-with-u/MaiBot)** — Inspiration and design reference

## 📄 License

GPL-3.0, see [LICENSE](LICENSE).