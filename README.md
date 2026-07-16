# apiforge (Go)

Lean, single-binary OpenAI/Anthropic-compatible gateway that reuses **local CLI
subscription logins** (Codex/Claude/Gemini/Copilot/Cursor/Qwen) as upstreams.
No frontend, no database. Go rewrite of the TypeScript `apiforge` (kept at
`../apiforge` as the reference for parity).

## Why Go
Single static arm64 binary → `scratch` image ~10–15MB, ~15–30MB RAM, `io.Copy`
streaming (flat memory). Fits a 1GB Raspberry Pi comfortably.

## Design
Env-configured. Per provider: a rotating **account pool** with cooldown on
429/auth-fail, **per-account concurrency cap** + **sticky sessions** (borrowed
from sub2api — protects subscription accounts, lowers ban risk). Token refresh
is self-managed (read file → single-flight OAuth refresh → write back).

```
cmd/apiforge/main.go           fail-closed startup + graceful shutdown
internal/
  config/    env → Config (+ credential path auto-detect, pool tuning)
  types/     Provider contract + capabilities (responses|images|anthropic)
  token/     TokenManager: load-once + single-flight refresh
  pool/      AccountPool: round-robin/failover + cooldown + concurrency + sticky + pin
  registry/  register / initAll (failures isolated) / route by model
  server/    net/http: /v1 (openai+anthropic+images), /admin, /health + middleware
  util/      httpx · jwtx · filestore · ssrf · sanitize
  provider/  (Phase 2+) codex claude gemini copilot cursor qwen openaicompat custom
```

## Phases
- [x] **P1 skeleton** — config, types, token base, pool, registry, server+middleware, /health, /v1/models, auth/ratelimit/bodylimit, fail-closed
- [x] **P2 openaiCompat + custom** — vendor table (20) + user relays, account-key pool, live/static models, relay + account retry (429/401/5xx switch), concurrency cap + sticky
- [x] **P3 codex** — CLI OAuth self-refresh + OpenAI API-key accounts; chat (translated OpenAI↔Codex Responses SSE, stream + aggregate), native /responses, images (gen + edits/img2img); account pool retry + concurrency + sticky
- [x] **P4 claude + gemini** — claude: OAuth self-refresh + API-key; OpenAI↔Anthropic translation (stream+aggregate) + native /messages & count_tokens passthrough + Claude-Code identity injection. gemini-cli (EXPERIMENTAL, `GEMINI_OAUTH_ENABLED`): Google OAuth + Code Assist + OpenAI↔Gemini translation
- [x] **P5 qwen + copilot** — qwen-cli: OAuth self-refresh + per-account base URL from resource_url. copilot: GitHub token→Copilot token exchange (single-flight), live /models with copilot/ prefix, editor fingerprint headers
- [x] **P6 cursor** (EXPERIMENTAL) — reverse-engineered AiService: hand-rolled protobuf + Connect-RPC framing (gzip) + Jyh checksum cipher; OpenAI chat (stream + aggregate). Token via `CURSOR_ACCESS_TOKEN(S)` — no SQLite engine dependency (a headless host has no `state.vscdb`; extract the token once from a desktop Cursor: `sqlite3 state.vscdb "select value from ItemTable where key='cursorAuth/accessToken'"`)
- [x] **P7a parity + Dockerfile** — full route/provider/feature parity (see PARITY.md); admin account API wired; constant-time admin auth; scratch Dockerfile, static arm64 binary 6.8MB
- [ ] P7b deploy to Pi + measure RAM (needs Pi + copied logins)

## Run
```
API_KEYS=sk-... PORT=8899 HOST=127.0.0.1 go run ./cmd/apiforge
```
Env: `API_KEYS`, `ADMIN_TOKEN`, `HOST`, `PORT`, `RATE_LIMIT_RPM`,
`MAX_ACCOUNT_CONCURRENCY`, `STICKY_TTL_SECONDS`, `<PROVIDER>_AUTH(S)`,
`ALLOW_UNAUTHENTICATED`.
