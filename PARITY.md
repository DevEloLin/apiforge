# Parity vs the TypeScript apiforge

Tracks feature coverage of the Go rewrite against `../apiforge` (Node/TS/Hono).
Goal: no functional loss ("功能不能缺失"), minus the intentionally dropped frontend.

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

## Providers — full parity
codex (chat/responses/images, OAuth+key) · claude (chat translate + native messages/count_tokens, OAuth+key) ·
gemini-cli (EXPERIMENTAL, opt-in) · copilot (token exchange) · qwen-cli (OAuth, dynamic base URL) ·
cursor (EXPERIMENTAL, protobuf) · openaicompat vendors (20) · custom relays. All present.

## Features — parity + additions
- Account pool: round-robin/failover + cooldown (429/auth-fail) ✅
- Manual account control via `/admin` (pin/enable/disable) ✅
- CLI-login + API-key mixed pool ✅
- Outbound fingerprint spoofing (genuine CLI UA/headers, no inbound header forwarding) ✅
- SSRF guard on base URLs · fail-closed startup · body limit · rate limit · secret/path redaction · keyFile path guard ✅
- Constant-time admin-token compare ✅
- **NEW (from sub2api):** per-account concurrency cap + sticky sessions (`x-apiforge-session`)

## Intentional differences
- **No browser dashboard** (user requirement — lean, headless).
- **Cursor token via `CURSOR_ACCESS_TOKEN(S)`** instead of reading `state.vscdb` (no SQLite engine
  dependency; a headless host has no Cursor DB anyway — keeps the scratch image ~7MB).
- gemini-cli / cursor are opt-in/EXPERIMENTAL (as in TS).

## Verification status
- Live E2E passed: **codex** (chat non-stream/stream, image gen), **copilot** (48 models, chat), **admin API**.
- Format/unit-verified, live pending fresh login: **claude** (refresh request format confirmed via Anthropic's
  semantic rate-limit response; 27 provider unit tests total across translation/protobuf layers).
- Code + unit only (no local login): **qwen**, **gemini**, **cursor**.
