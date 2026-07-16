**English** | [中文](./README.zh-CN.md)

<h1 align="center">apiforge</h1>

<p align="center">
  <b>One OpenAI- and Anthropic-compatible API gateway for all your local AI CLI &amp; subscription logins.</b><br/>
  Single static Go binary · no frontend · no database · ~7&nbsp;MB image · runs on a 1&nbsp;GB Raspberry Pi.
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white">
  <img alt="Docker image" src="https://img.shields.io/badge/image-~7MB%20(scratch)-2496ED?logo=docker&logoColor=white">
  <img alt="Arch" src="https://img.shields.io/badge/arch-amd64%20%7C%20arm64-lightgrey">
  <img alt="License" src="https://img.shields.io/badge/license-Personal%20Research%20%2B%20Attribution-orange">
  <img alt="Status" src="https://img.shields.io/badge/use-personal%20research%20only-red">
</p>

<p align="center">
  📖 <a href="./docs/USAGE.md">Usage</a> ·
  🛠️ <a href="./docs/OPERATIONS.md">Operations</a> ·
  🧩 <a href="./docs/ARCHITECTURE.md">Architecture</a> ·
  📚 <a href="./docs/">All docs</a> ·
  🇨🇳 <a href="./README.zh-CN.md">中文文档</a>
</p>

---

**apiforge** reads the login credentials of the AI CLIs you already use — Codex/ChatGPT,
Claude Code, GitHub Copilot, Gemini CLI, Qwen Code, Cursor, Grok — auto-refreshes their
OAuth tokens over HTTP, and re-exposes all of them behind **one standard OpenAI/Anthropic
API**. Any OpenAI- or Anthropic-compatible client can then talk to a single endpoint. It
also relays plain API keys for 20+ vendors and your own custom relays.

## ⚠️ Important notice — read first

Please read the following carefully before using this project:

- 🚨 **Terms-of-service risk.** Using this project may violate the Terms of Service of
  Anthropic, OpenAI, Google, GitHub, xAI, Cursor, Alibaba and other upstream providers.
  Read each provider's user agreement first; **all resulting risk is borne by the user.**
- ⚖️ **Lawful use.** Use this project only in compliance with the laws and regulations of
  your country/region. Any illegal or non-compliant use is strictly prohibited.
- 📖 **Disclaimer.** This project is for technical learning and research ONLY. The author is
  not liable for any account bans, service interruptions, data loss, or any other direct or
  indirect damages arising from its use.
- 🚫 **No commercial authorization.** This project has never authorized any individual or
  organization to run any form of commercial operation based on it. Any commercial activity
  conducted in the name of, or based on, this project is unrelated to this project and its
  developer; all disputes, losses, and legal liability are borne solely by the actor.

Additional notes: this project **ships and distributes no** vendor accounts, keys, or
subscriptions — you supply your own (your already-logged-in local CLIs). Reverse-engineered
parts (Cursor / Grok web protocols, etc.) are based on public third-party research, marked
**EXPERIMENTAL**, and may break whenever a vendor changes its API.

> Open-source terms and the **non-removable attribution** requirement are in
> [License &amp; attribution](#license--attribution) and [LICENSE](./LICENSE).

## Table of contents
- [Why apiforge](#why-apiforge)
- [Supported subscriptions &amp; sources](#supported-subscriptions--sources)
- [Quick start](#quick-start)
- [Configuration](#configuration)
- [Usage](#usage)
- [Concurrency &amp; queueing](#concurrency--queueing)
- [Admin API](#admin-api)
- [Security](#security)
- [Deploy](#deploy)
- [FAQ](#faq)
- [License &amp; attribution](#license--attribution)
- [Acknowledgements](#acknowledgements)

## Why apiforge

You are logged into subscription tools (Codex / Claude Code / Copilot / Gemini CLI …) on your
machine. apiforge reuses those local logins and **unifies them behind one standard API**:

```
  your client / new-api / scripts
        │  OpenAI / Anthropic protocol (sk-...)
        ▼
   ┌─────────────┐   reuse local logins + auto-refresh tokens
   │  apiforge   │──────────────────────────────►  vendor backends
   └─────────────┘   account pool · auto-switch · concurrency cap · queueing
```

- **Unified surface:** `/v1/chat/completions`, `/v1/responses`, `/v1/messages`,
  `/v1/messages/count_tokens`, `/v1/images/generations`, `/v1/images/edits`, `/v1/models`.
- **Multi-account pool:** round-robin + cooldown-on-failure switching + manual pinning.
- **No CLI subprocesses:** it only reads credential files and refreshes tokens over HTTP —
  tiny memory footprint.

## Supported subscriptions & sources

### A. Subscription / CLI login reuse (core feature)

| Source | provider id | How it's reused | Capabilities | Status |
|---|---|---|---|---|
| **ChatGPT / Codex subscription** | `codex` | Codex CLI OAuth (`~/.codex/auth.json`), auto-refresh | chat · responses · image gen · image edit | ✅ tested |
| **Claude (Claude Code subscription)** | `claude` | Claude Code OAuth (`~/.claude/.credentials.json`), auto-refresh | chat (OpenAI↔Anthropic) · native `/v1/messages` · count_tokens | ✅ implemented |
| **GitHub Copilot subscription** | `copilot` | GitHub OAuth token → Copilot token exchange | chat (auto-discovers all available models) | ✅ tested |
| **Grok (grok.com subscription)** | `grok-web` | reuse the `sso` session cookie | chat (stream / non-stream) | 🧪 experimental |
| **Cursor subscription** | `cursor` | session token (Connect-RPC / protobuf) | chat | 🧪 experimental |
| **Gemini (Google account)** | `gemini-cli` | Gemini CLI OAuth (Code Assist) | chat | 🧪 experimental (off by default, `GEMINI_OAUTH_ENABLED=1`) |
| **Qwen Code** | `qwen-cli` | Qwen Code CLI OAuth, auto-refresh | chat | ✅ implemented |

### B. Official / compatible API-key relays (enabled when you supply a key)

- **OpenAI** (`OPENAI_API_KEYS`), **Anthropic** (`ANTHROPIC_API_KEYS`).
- **20+ OpenAI-compatible vendors** (set the matching `*_API_KEYS`): DeepSeek, Kimi (Moonshot),
  Zhipu GLM, Qwen, ERNIE (Baidu), SenseTime, Skywork, 360, MiniMax, Doubao, Hunyuan, Spark,
  StepFun, Yi (01.AI), Baichuan, SiliconFlow, Gemini (key), AWS Bedrock, OpenRouter,
  **xAI Grok (`XAI_API_KEYS`, official key)**, Agnes.
- **Custom relays:** `CUSTOM_PROVIDERS` (inline JSON) / `CUSTOM_PROVIDERS_FILE`, and you can
  reuse a third-party CLI's token file via `keyFile`.

> `grok-web` (subscription reuse) and `grok` (official API key) can coexist — the former
> reuses your grok.com subscription (models prefixed `grok-web/`), the latter uses an x.ai key.

## Quick start

### Option 1 — from source (Go 1.26+)

```bash
git clone https://github.com/DevEloLin/apiforge.git
cd apiforge
API_KEYS=sk-my-secret HOST=127.0.0.1 PORT=8899 go run ./cmd/apiforge
```

### Option 2 — Docker (scratch image, ~7 MB)

```bash
docker build -t apiforge .
docker run --rm -p 127.0.0.1:8899:8899 \
  -e API_KEYS=sk-my-secret \
  -e CODEX_AUTHS=/creds/codex/auth.json \
  -v "$HOME/.codex:/creds/codex" \
  apiforge
```

> The container defaults to `HOST=0.0.0.0`; publish it only to `127.0.0.1` and put new-api /
> Cloudflare Tunnel in front for multi-user access and auth. Full Docker guide (credential
> mounting, read/write, compose, arm64 builds): [OPERATIONS.md](./docs/OPERATIONS.md).

Verify:

```bash
curl -s http://127.0.0.1:8899/health | jq                              # ready providers
curl -s -H "Authorization: Bearer sk-my-secret" \
     http://127.0.0.1:8899/v1/models | jq '.data[].id'                 # aggregated models
```

## Configuration

Everything is configured via **environment variables** (the same image can be reconfigured at
`docker run` time). Most-used ones:

| Var | Default | Meaning |
|---|---|---|
| `API_KEYS` | empty | client access keys (comma-separated); empty + non-loopback bind → **refuses to start** |
| `HOST` / `PORT` | `127.0.0.1` / `8899` | listen address |
| `ADMIN_TOKEN` | empty | guards `/admin/*`; empty disables admin |
| `MAX_ACCOUNT_CONCURRENCY` | `3` | per-account concurrency cap (`0` = unlimited) |
| `QUEUE_WAIT_MS` | `60000` | max time a request queues for a free slot when all accounts are busy |
| `STICKY_TTL_SECONDS` | `0` | session affinity (`x-apiforge-session`); `0` = off |
| `RATE_LIMIT_RPM` | `0` | per-key requests/min; `0` = off |
| `<PROVIDER>_AUTHS` / `_AUTH` | auto-detect | explicit credential file path(s) |
| `OPENAI_API_KEYS` / `ANTHROPIC_API_KEYS` / `<VENDOR>_API_KEYS` | empty | API keys (mixed into pools) |
| `GROK_COOKIES` 🧪 / `CURSOR_ACCESS_TOKEN(S)` 🧪 | empty | subscription session credentials |
| `GEMINI_OAUTH_ENABLED` 🧪 / `GEMINI_OAUTH_CLIENT_ID` / `_SECRET` | off / empty | enable gemini-cli (public client not vendored here) |

Full reference and `.env` sample: [OPERATIONS.md § Configuration](./docs/OPERATIONS.md) and
[.env.example](./.env.example).

## Usage

Routing is by the request's `model` field. Note the required prefixes `copilot/`, `grok-web/`,
`cursor/`. Full examples (streaming, tools, images, SDKs): [USAGE.md](./docs/USAGE.md).

```bash
# Chat (OpenAI protocol) — routed to codex by model name
curl http://127.0.0.1:8899/v1/chat/completions \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.4-mini","messages":[{"role":"user","content":"hi"}]}'

# Claude, native Anthropic protocol
curl http://127.0.0.1:8899/v1/messages \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-5","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}'

# Grok subscription (note the grok-web/ prefix)
curl http://127.0.0.1:8899/v1/chat/completions \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"grok-web/grok-4.2","messages":[{"role":"user","content":"hi"}]}'
```

Point any OpenAI SDK at `http://127.0.0.1:8899/v1` with your `API_KEYS` value as the key.

## Concurrency & queueing

- A **per-account cap** (`MAX_ACCOUNT_CONCURRENCY`, default 3) protects subscription accounts
  from bursts and lowers ban risk.
- When all healthy accounts of a source are at their cap, new requests **queue** for a freed
  slot (up to `QUEUE_WAIT_MS`) instead of failing immediately.
- **Auto-switch:** 429 → 60 s cooldown, auth failure → 5 min cooldown, switching to other
  accounts meanwhile. A streamed response holds its slot until the stream closes.

Example: 2 accounts × cap 3 = 6 in flight; a burst of 20 users runs 6 at a time and the rest
queue — trading queue latency for zero failures.

## Admin API

With `ADMIN_TOKEN` set (`Authorization: Bearer <ADMIN_TOKEN>`):

| Method | Path | Purpose |
|---|---|---|
| GET | `/admin/providers` | ready providers and their models |
| GET | `/admin/accounts` | per-account health (in-flight / cooldown / disabled) |
| POST | `/admin/accounts/preferred` | pin a preferred account (empty `account` clears) |
| POST | `/admin/accounts/enabled` | enable/disable an account |

Per-request headers: `x-apiforge-account: codex#2` (pin) and `x-apiforge-session: <id>`
(sticky). See [USAGE.md](./docs/USAGE.md).

## Security

- **Fail-closed:** refuses to start with no `API_KEYS` on a non-loopback bind (unless
  `ALLOW_UNAUTHENTICATED=1`).
- **SSRF guard** on all custom base URLs (rejects loopback / private / link-local).
- **Secret & path redaction** in logs and `/health`.
- **Outbound spoofing:** never forwards inbound headers; sends only each CLI's genuine
  fingerprint headers.
- **keyFile path-traversal guard**; **constant-time admin-token comparison**.

## Deploy

- Cross-compile a static binary:
  `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o apiforge ./cmd/apiforge` (~6.8 MB).
- Or `docker build` (scratch image, `GOMEMLIMIT=64MiB`).
- Recommended: bind loopback, front with new-api (multi-user/billing) + Cloudflare Tunnel.

Full guide — Docker packaging (3 ways), Docker configuration, docker-compose, systemd,
Raspberry Pi, reverse proxy, health checks, troubleshooting: [OPERATIONS.md](./docs/OPERATIONS.md).

## FAQ

**What is apiforge?** A self-hosted AI API gateway / reverse proxy written in Go that unifies
your local AI CLI & subscription logins (ChatGPT/Codex, Claude, Copilot, Gemini, Qwen, Cursor,
Grok) behind one OpenAI/Anthropic-compatible API — single binary, no frontend, no database,
runs on a Raspberry Pi.

**How is it different from one-api / new-api / sub2api?** one-api / new-api focus on
multi-tenant distribution & billing of API keys; sub2api is a heavier Go+Vue+PostgreSQL+Redis
platform. apiforge is leaner: single binary, no database, focused on **reusing local
subscription / CLI logins** as a standard API — great for personal use or behind new-api.

**Does it support streaming?** Yes — SSE streaming and non-stream aggregation on all chat endpoints.

**Can I get banned? How do I reduce the risk?** Yes, there is risk — reusing subscription logins
may violate provider ToS (see the notice above). Reduce it with per-account concurrency caps,
few accounts, and by not exposing it as a public high-traffic service; it cannot be eliminated.

**Can I use it commercially / resell it?** No. Personal research only, and **no commercial
operation is ever authorized** (see [License & attribution](#license--attribution)).

**Does it run on a Raspberry Pi / low-memory box?** Yes — ~6.8 MB static binary, ~7 MB scratch
image, `GOMEMLIMIT` default 64 MiB.

**Do I need to keep the CLIs running?** No — apiforge only reads the credential files and
refreshes OAuth tokens over HTTP; it never spawns CLI subprocesses.

## Documentation

- 📖 [Usage Guide](./docs/USAGE.md) — client API, endpoints, examples, SDKs
- 🛠️ [Operations Manual](./docs/OPERATIONS.md) — install, configure, Docker, deploy, troubleshoot
- 🧩 [Architecture](./docs/ARCHITECTURE.md) — design overview for contributors
- 🔁 [Parity](./docs/PARITY.md) — feature parity vs the TypeScript original
- 🤝 [Contributing](./docs/CONTRIBUTING.md) · 🔒 [Security](./docs/SECURITY.md) · 📝 [Changelog](./docs/CHANGELOG.md)

## License & attribution

This project uses the **apiforge Personal Research & Attribution License** — see
[LICENSE](./LICENSE). Core conditions:

1. **Personal, non-commercial research / learning / evaluation only**; no selling, reselling, or
   paid-service offering.
2. **Attribution must be retained and must NOT be removed, replaced, or altered:** any use,
   copy, fork, modification, or distribution (source or binary) must keep [LICENSE](./LICENSE)
   and [NOTICE](./NOTICE) intact and clearly credit the original source repository and author.
   **Impersonation, attribution removal, or origin misrepresentation is prohibited.**
3. You bear all risk arising from violating any AI vendor's Terms of Service (see the notice above).
4. The software is provided "as is", without warranty of any kind.

## Acknowledgements

Reverse-engineered protocols reference public community research (e.g. grok2api, codex-imagen,
and similar projects) for **interoperability research** purposes. All trademarks and services
belong to their respective owners; this project is **not affiliated with** any of these vendors.

Original source repository: **https://github.com/DevEloLin/apiforge** (please keep this
attribution when redistributing or forking).
