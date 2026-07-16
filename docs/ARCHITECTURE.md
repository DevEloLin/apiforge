**English** | [中文](./ARCHITECTURE.zh-CN.md)

# apiforge Architecture

A design overview for contributors. For end-user docs see [USAGE.md](./USAGE.md) and
[OPERATIONS.md](./OPERATIONS.md).

## Design goals
- **Lean:** pure standard library + `golang.org/x/sync` only; single static binary; flat memory
  via streaming (`io.Copy` / `io.Pipe`), no full-body buffering for proxied responses.
- **Faithful:** feature parity with the TS original (see [PARITY.md](./PARITY.md)).
- **Safe by default:** fail-closed, SSRF-guarded, secret-redacting, least-privilege.

## Package layout

```
cmd/apiforge/main.go        fail-closed startup + graceful shutdown
internal/
  config/     env → Config (credential path auto-detect, pool tuning)
  types/      Provider contract + capability interfaces + wire types
  token/      Manager: load-once + single-flight OAuth refresh
  pool/       Account pool: round-robin/failover + cooldown + concurrency cap + sticky + queueing
  registry/   register / initAll (failures isolated) / route by model
  relay/      shared HTTP relay + account-retry + queueing + response helpers
  server/     net/http surface (/v1, /admin, /health) + middleware
  util/       httpx · jwtx · filestore · ssrf · sanitize · sse · idgen
  provider/   codex · claude · gemini · copilot · qwen · cursor · grokweb · openaicompat · custom
```

## Provider contract

Every upstream implements `types.Provider`:

```go
type Provider interface {
    ID() string
    Capabilities() []Capability
    Init(ctx context.Context) error
    IsReady() bool
    ListModels() []ModelObject
    OwnsModel(model string) bool
    ChatCompletion(rctx RequestContext, body []byte) (*http.Response, error)
}
```

OpenAI Chat Completions is the lingua franca. Extra surfaces are **opt-in capability
interfaces**, checked by type assertion in the router:

- `ResponsesProvider` → `/v1/responses` (codex)
- `ImagesProvider` → `/v1/images/*` (codex)
- `AnthropicProvider` → `/v1/messages`, `/v1/messages/count_tokens` (claude)
- `Pooled` → exposes `pool.Admin` for the `/admin` account API

A provider method returns the upstream `*http.Response` (real, or synthesized via `io.Pipe` for
translating providers). The server streams it back with `io.Copy` so memory stays flat.

## Request flow

```
client → server (auth · rate-limit · body-limit middleware)
       → extract model → registry.FindByModel → capability check
       → provider.ChatCompletion(rctx, body)
            → relay.WithAccountRetry (pool candidates · acquire · queue)
                 → provider fn: build upstream request, translate if needed
       → server writeUpstream (copy status + headers + io.Copy body, flush for SSE)
```

## Account pool (`internal/pool`)

Generic `Pool[C]` (C = credential type). Per-account `state{disabledUntil, failures,
manualDisabled, inflight}`. Key behaviors:

- **Ordering** (`Candidates`): healthy first, prefer accounts with a free concurrency slot,
  round-robin rotation, then sticky / pinned / preferred moved to the front; if everyone is
  cooling down, return the one recovering soonest so the request still attempts.
- **Concurrency cap:** `Acquire` reserves a slot (fails if at cap); `Release` frees it and
  **broadcasts** on a `freed` channel so queued requests wake.
- **Cooldown:** `MarkRateLimited` (429, default 60 s) / `MarkAuthFailed` (401/403, 5 min).
- **Sticky sessions:** `Bind(session, id)` maps a session key → account for `StickyTTL`.
- **Admin:** `SetPreferred` / `SetEnabled` / `Status` via the type-erased `pool.Admin` interface.

## Token lifecycle (`internal/token`)

`Manager` wraps a `Source` (per-provider credential logic):

```go
type Source interface {
    Read(ctx) error            // load from disk
    Token() string             // current access token
    Fresh() bool               // still valid (with skew)?
    Refresh(ctx) (string, error) // HTTP refresh, persist rotation
}
```

`AccessToken` = load-once, then if `Fresh()` return; else a **single-flight** refresh that
**re-reads from disk first** (the CLI may have already rotated the token) before refreshing.
Each provider's `creds` holds auth state in an `atomic.Pointer` so `Token()`/`Fresh()` are
race-free against a concurrent `Refresh()`. Rotated tokens are written back atomically
(temp file + rename, 0600) so the CLI and gateway stay in sync.

## Relay & account retry (`internal/relay`)

`WithAccountRetry[C]` drives the pool:

1. Iterate `Candidates`; `Acquire` each (skip — and **count as queueable** — if at cap).
2. Run the provider fn. On **2xx**: `MarkOk` + `Bind` + wrap the body in a `releaseCloser` so the
   concurrency slot is held until the response body closes (streaming counts toward the cap).
3. On **429 / 401 / 403 / 5xx**: cool the account and try the next.
4. Non-retriable client errors (400/404/422): return the upstream body verbatim.
5. If every healthy account was **busy** (not failed), **queue**: wait on `SlotFreed()` (re-fetched
   each loop) up to `QUEUE_WAIT_MS`, then retry; on client disconnect return `ctx.Err()`.
6. Exhausted → synthesized `503`.

**Response helpers** (shared by translating providers): `JSONResponse` (aggregate → JSON body),
`StreamingResponse` (an `io.Pipe`; a goroutine reads the upstream stream, writes translated SSE
frames, and closes the upstream), `SynthStatus` (a small error response to steer the classifier —
e.g. a synthetic 401 on token-refresh failure).

## Translation layers

Translating providers convert OpenAI ⇄ the vendor's native protocol both ways, including
**streaming**:

- **codex** — OpenAI Chat ⇄ Codex Responses SSE; image_generation tool for images.
- **claude** — OpenAI Chat ⇄ Anthropic Messages; native `/v1/messages` passthrough; injects the
  Claude Code identity system block + strips client fingerprint fields in OAuth mode.
- **gemini** — OpenAI Chat ⇄ Gemini `generateContent` (Code Assist envelope).
- **cursor** — hand-rolled protobuf + Connect-RPC framing (gzip) + the Jyh checksum cipher.
- **grokweb** — grok.com NDJSON stream (`result.token` deltas) + spoofed `x-statsig-id`.
- **openaicompat** — no translation; relays to any OpenAI-compatible endpoint (vendors + custom).

Wire helpers: `util/sse` (SSE frame iterator + NDJSON line iterator + frame writers),
`util/idgen` (chatcmpl / resp / call ids + UUID). Each provider keeps its own request structs —
no shared wire types across packages.

## Security layers
- **Fail-closed** startup (`main.go`): refuse no-auth on a non-loopback bind.
- **SSRF** (`util/ssrf`): reject loopback / private / link-local base URLs.
- **Redaction** (`util/sanitize`): mask secrets and absolute paths in logs / `/health`.
- **Outbound hygiene:** never forward inbound headers; send only genuine CLI fingerprint headers.
- **keyFile guard:** custom `keyFile` must stay under the allowed root.
- **Constant-time** admin-token comparison (`crypto/subtle`).

## Adding a provider
1. New package under `internal/provider/<name>/`.
2. Implement `types.Provider` (+ any capability interfaces), backed by `pool.Pool[C]` and, for
   OAuth sources, a `token.Manager`.
3. For translating providers, return `relay.StreamingResponse` / `relay.JSONResponse` and drive
   the pool with `relay.WithAccountRetry`.
4. Register it in `internal/provider/register.go` (gate by env / credential presence).
5. Add unit tests for the translation layer (see existing `*_test.go`).
