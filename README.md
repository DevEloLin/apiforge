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
- [ ] P2 openaiCompat + custom (API-key relays)
- [x] **P3 codex** — CLI OAuth self-refresh + OpenAI API-key accounts; chat (translated OpenAI↔Codex Responses SSE, stream + aggregate), native /responses, images (gen + edits/img2img); account pool retry + concurrency + sticky
- [x] **P4 claude + gemini** — claude: OAuth self-refresh + API-key; OpenAI↔Anthropic translation (stream+aggregate) + native /messages & count_tokens passthrough + Claude-Code identity injection. gemini-cli (EXPERIMENTAL, `GEMINI_OAUTH_ENABLED`): Google OAuth + Code Assist + OpenAI↔Gemini translation
- [ ] P5 qwen + copilot
- [ ] P6 cursor (protobuf + checksum + state.vscdb); fallback-proxy to TS version if blocked
- [ ] P7 parity diff vs TS · Dockerfile (scratch) · deploy to Pi + measure RAM

## Run
```
API_KEYS=sk-... PORT=8899 HOST=127.0.0.1 go run ./cmd/apiforge
```
Env: `API_KEYS`, `ADMIN_TOKEN`, `HOST`, `PORT`, `RATE_LIMIT_RPM`,
`MAX_ACCOUNT_CONCURRENCY`, `STICKY_TTL_SECONDS`, `<PROVIDER>_AUTH(S)`,
`ALLOW_UNAUTHENTICATED`.
