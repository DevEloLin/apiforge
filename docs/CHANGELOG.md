# Changelog

All notable changes are documented here. Format based on
[Keep a Changelog](https://keepachangelog.com/); this project uses rolling `main` releases.
本变更记录采用 Keep a Changelog 风格；项目以滚动 `main` 发布。

## [Unreleased]

### Added
- Initial public release of the Go rewrite of apiforge / apiforge 的 Go 重写版首次公开发布.
- **Providers / 来源:** `codex` (chat · responses · images gen+edit), `claude` (chat translate +
  native messages/count_tokens), `copilot`, `qwen-cli`, `gemini-cli` 🧪, `cursor` 🧪,
  `grok-web` 🧪 (grok.com subscription reuse), 20+ OpenAI-compatible vendors (incl. xAI Grok
  API key) + custom relays.
- **Account pool / 账户池:** round-robin + failover + cooldown, manual pin/enable/disable via
  `/admin`, CLI-login + API-key mixed pool.
- **From sub2api / 借鉴 sub2api:** per-account concurrency cap + sticky sessions.
- **Request queueing / 请求排队:** wait for a free account slot (`QUEUE_WAIT_MS`) instead of
  failing when all accounts are at cap.
- **Docker:** multi-stage → `scratch` image (~7 MB), static arm64 binary (~6.8 MB),
  `GOMEMLIMIT=64MiB`, non-root.
- **Docs / 文档:** bilingual README + USAGE + OPERATIONS + PARITY + ARCHITECTURE.

### Security
- Fail-closed startup, SSRF guard, secret/path redaction, keyFile path-traversal guard,
  constant-time admin-token comparison.
- Gemini OAuth client is **not vendored**; supply via `GEMINI_OAUTH_CLIENT_ID` / `_SECRET`.

### Hardening (multi-perspective code audit)
- **Crash-safety:** streaming goroutine `recover()`; account-retry frees the concurrency slot on
  every path incl. panic; cursor protobuf length-prefix guards (no panic / no giant allocation).
- **Resilience:** token-refresh failure no longer blacks out the whole pool for 5 min; upstream
  200-then-failure / truncated / non-JSON bodies surface an error instead of a fake empty 200;
  codex "no image" is non-retriable; copilot token-cache fallback + big-int preservation.
- **Correctness:** claude `content:null` tool-call turns; SSE/NDJSON reader has no line-length cap;
  cursor concatenates batched deltas; `/v1/models` de-duplicated; bad request bodies → 400 not 502.
- **Security:** SSRF resolves hostnames; constant-time client-key check; upstream identity headers
  (Set-Cookie / org / cf-ray) stripped; keyFile guard fail-closed; secret redaction in logs.

### Notes
- Reverse-engineered providers (`cursor`, `grok-web`) and `gemini-cli` are EXPERIMENTAL / opt-in.
- See [PARITY.md](./PARITY.md) for verification status.
