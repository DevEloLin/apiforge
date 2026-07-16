**English** | [中文](./PARITY.zh-CN.md)

# Parity vs the TypeScript apiforge

Tracks feature coverage of the Go rewrite against the original `apiforge` (Node/TS/Hono).
Goal: no functional loss, minus the intentionally dropped frontend.

## Routes — full parity
| Route | TS | Go |
|---|---|---|
| `GET /health` | ✅ | ✅ |
| `GET /v1/models` | ✅ | ✅ |
| `POST /v1/chat/completions` | ✅ | ✅ |
| `POST /v1/responses` | ✅ | ✅ |
| `POST /v1/messages` | ✅ | ✅ |
| `POST /v1/messages/count_tokens` | ✅ | ✅ |
| `POST /v1/images/generations` | ✅ | ✅ |
| `POST /v1/images/edits` | ✅ | ✅ |
| `GET /admin/providers` | ✅ | ✅ |
| `GET /admin/accounts` | ✅ | ✅ |
| `POST /admin/accounts/preferred` | ✅ | ✅ |
| `POST /admin/accounts/enabled` | ✅ | ✅ |
| `GET /` (browser dashboard) | ✅ | ❌ **dropped by request** (no frontend) |

## Providers — full parity + additions
- `codex` — chat / responses / images (gen + edit), OAuth + API key.
- `claude` — chat (OpenAI↔Anthropic translate) + native messages / count_tokens, OAuth + API key.
- `copilot` — GitHub→Copilot token exchange, live models.
- `qwen-cli` — OAuth, per-account base URL.
- `gemini-cli` — 🧪 experimental, opt-in.
- `cursor` — 🧪 experimental, reverse-engineered protobuf.
- **`grok-web`** — 🧪 experimental, grok.com subscription reuse (**new, not in TS**).
- openaicompat vendors (20+, incl. **xAI Grok API key**) · custom relays.

## Features — parity + additions
- Account pool: round-robin / failover + cooldown (429 / auth-fail). ✅
- Manual account control via `/admin` (pin / enable / disable). ✅
- CLI-login + API-key mixed pool. ✅
- Outbound fingerprint spoofing (genuine CLI UA / headers; no inbound header forwarding). ✅
- SSRF guard on base URLs · fail-closed startup · body limit · rate limit · secret/path redaction · keyFile path guard. ✅
- Constant-time admin-token comparison. ✅
- **NEW (inspired by sub2api):** per-account concurrency cap + sticky sessions (`x-apiforge-session`).
- **NEW:** request **queueing** — when all accounts are at their cap, requests wait for a free
  slot (`QUEUE_WAIT_MS`) instead of failing immediately.

## Intentional differences
- **No browser dashboard** (by request — lean, headless).
- **Cursor token via `CURSOR_ACCESS_TOKEN(S)`** instead of reading `state.vscdb` (no SQLite
  engine dependency; a headless host has no Cursor DB anyway — keeps the scratch image ~7 MB).
- **Gemini OAuth client not vendored** — supply `GEMINI_OAUTH_CLIENT_ID` / `_SECRET` via env.
- `gemini-cli` / `cursor` / `grok-web` are opt-in / EXPERIMENTAL.

## Verification status
- **Live E2E passed:** `codex` (chat non-stream/stream, image gen), `copilot` (48 models, chat),
  admin API.
- **Format/unit-verified, live pending fresh login:** `claude` (refresh request format confirmed
  via Anthropic's semantic rate-limit response).
- **Code + unit only (no local login):** `qwen`, `gemini`, `cursor`, `grok-web`.
- ~36 unit tests across translation / protobuf / queueing layers.
