**English** | [中文](./OPERATIONS.zh-CN.md)

# apiforge Operations Manual (Install · Configure · Docker · Deploy · Ops)

> Aimed at deployment and operations. For how clients call the gateway, see [USAGE.md](./USAGE.md).
> Before using, please read the disclaimer in the project root [README](../README.md) and the [LICENSE](../LICENSE).

## Table of Contents
1. [Environment Requirements](#1-environment-requirements)
2. [Three Ways to Run](#2-three-ways-to-run)
3. [Preparing Login Credentials for Each Source](#3-preparing-login-credentials-for-each-source)
4. [Full Configuration Reference](#4-full-configuration-reference)
5. [Building the Docker Image](#5-building-the-docker-image)
6. [Running and Configuring Docker](#6-running-and-configuring-docker)
7. [docker-compose](#7-docker-compose)
8. [Bare Binary + systemd](#8-bare-binary--systemd)
9. [Raspberry Pi Deployment](#9-raspberry-pi-deployment)
10. [Fronting with new-api / Cloudflare Tunnel](#10-fronting-with-new-api--cloudflare-tunnel)
11. [Health Checks and Monitoring](#11-health-checks-and-monitoring)
12. [Upgrade and Rollback](#12-upgrade-and-rollback)
13. [Troubleshooting](#13-troubleshooting)

---

## 1. Environment Requirements

- **Build from source / cross-compile**: Go 1.26+.
- **Docker**: Docker 20+ (use buildx for arm64 targets).
- **Credentials**: the relevant AI CLI is already logged in on the machine (or you manually prepare credential files / cookies / tokens). See §3.
- Network: outbound access to each vendor's API domains (api.openai.com, api.anthropic.com, chatgpt.com,
  githubcopilot.com, grok.com, etc.).

---

## 2. Three Ways to Run

| Method | Best for | Memory | Notes |
|---|---|---|---|
| Source `go run` | Development and debugging | Medium | Fastest to verify |
| Bare binary + systemd | Production / Raspberry Pi | **Lowest** | No container overhead, see §8 |
| Docker (scratch image) | When you want container isolation | Low (image ~7MB) | See §5–§7 |

Simplest startup (from source):

```bash
git clone https://github.com/DevEloLin/apiforge.git && cd apiforge
API_KEYS=sk-my-secret HOST=127.0.0.1 PORT=8899 go run ./cmd/apiforge
```

When you see `apiforge listening ... ready=[...]`, it started successfully; the `ready` list contains the sources that were detected and initialized successfully.

---

## 3. Preparing Login Credentials for Each Source

apiforge **does not log in for you**; it only reads the credential files already logged in on your machine, and refreshes tokens automatically over HTTP when needed.

| Source | Credential location (auto-detected) | Manual override environment variable | Notes |
|---|---|---|---|
| codex | `~/.codex/auth.json` | `CODEX_AUTHS` (comma-separated for multiple accounts) | Produced by `codex login` |
| claude | `~/.claude/.credentials.json` | `CLAUDE_AUTHS` | Produced by `claude` login |
| copilot | `~/.config/github-copilot/` (directory) | `COPILOT_GITHUB_TOKENS` | Discovered from apps.json/hosts.json |
| qwen | `~/.qwen/oauth_creds.json` | `QWEN_AUTHS` | Produced by `qwen` login |
| gemini 🧪 | `~/.gemini/oauth_creds.json` | `GEMINI_AUTHS` + requires `GEMINI_OAUTH_ENABLED=1` and your own `GEMINI_OAUTH_CLIENT_ID/SECRET` | Experimental |
| grok-web 🧪 | none (use environment variables) | `GROK_COOKIES` (sso or the full cookie string) | Copy sso from the browser |
| cursor 🧪 | none (headless machines have no state.vscdb) | `CURSOR_ACCESS_TOKEN(S)` | Export from desktop Cursor |

**Multiple accounts**: `CODEX_AUTHS=/path/a/auth.json,/path/b/auth.json`; or `GROK_COOKIES=cookie1,cookie2`.
Each credential becomes one account in the account pool, with automatic round-robin + failover.

**Direct API-key access** (mixed into the same provider pool as CLI accounts): `OPENAI_API_KEYS`, `ANTHROPIC_API_KEYS`,
and each vendor's `<VENDOR>_API_KEYS` (see §4).

Obtaining Grok / Cursor tokens:

```bash
# Grok: log in to grok.com in the browser → DevTools → Application → Cookies → copy the sso value
export GROK_COOKIES='sso=eyJ...'            # 若被 Cloudflare 403，用完整串：
export GROK_COOKIES='sso=eyJ...; cf_clearance=xxxx'

# Cursor：从桌面版 Cursor 的 state.vscdb 导出会话 token
sqlite3 "$HOME/Library/Application Support/Cursor/User/globalStorage/state.vscdb" \
  "select value from ItemTable where key='cursorAuth/accessToken'"
export CURSOR_ACCESS_TOKEN='eyJ...'
```

---

## 4. Full Configuration Reference

All configuration comes from **environment variables** (the same image can be reconfigured at `docker run` time, with no rebuild).

### Core
| Variable | Default | Description |
|---|---|---|
| `API_KEYS` | empty | Client access keys, comma-separated. Empty plus a non-loopback bind → **refuse to start** |
| `HOST` | `127.0.0.1` | Listen address. Inside a container use `0.0.0.0` |
| `PORT` | `8899` | Listen port |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `ADMIN_TOKEN` | empty | Admin API token; empty disables `/admin/*` |
| `ALLOW_UNAUTHENTICATED` | `false` | Allow starting with no keys + non-loopback (dangerous, debug only) |

### Pool / Concurrency / Rate Limiting
| Variable | Default | Description |
|---|---|---|
| `MAX_ACCOUNT_CONCURRENCY` | `3` | Per-account concurrency cap; `0`=unlimited |
| `QUEUE_WAIT_MS` | `60000` | Max milliseconds to queue for a free slot when all accounts are busy |
| `STICKY_TTL_SECONDS` | `0` | Session stickiness TTL (`x-apiforge-session`); `0`=off |
| `RATE_LIMIT_RPM` | `0` | Per-key requests-per-minute cap; `0`=off |
| `MAX_BODY_BYTES` | `10485760` | Request body cap; `0`=unlimited |
| `UPSTREAM_TIMEOUT_MS` | `600000` | Upstream timeout (reserved) |
| `GOMEMLIMIT` | `64MiB` in the image | Go heap soft limit (recommended on low-memory machines) |

### Per-Source Toggles / Credentials
| Variable | Default | Description |
|---|---|---|
| `<P>_ENABLED` | `true` | Disable a source, e.g. `CURSOR_ENABLED=false` (P=CODEX/CLAUDE/COPILOT/CURSOR/QWEN/GEMINI) |
| `<P>_AUTHS` / `<P>_AUTH` | auto-detect | Credential file path (comma-separated for multiple) |
| `CODEX_MODELS` / `CLAUDE_MODELS` / `GEMINI_CLI_MODELS` | built-in | Override the advertised model list |
| `CODEX_CLIENT_VERSION` | `0.142.5` | Codex backend version header; bump this when a model is rejected |
| `CODEX_USER_AGENT` / `CLAUDE_USER_AGENT` / `GEMINI_USER_AGENT` | built-in | Override the outbound UA |
| `OPENAI_BASE_URL` / `ANTHROPIC_BASE_URL` | official | Override the base for codex(key)/claude |
| `OPENAI_API_KEYS` / `ANTHROPIC_API_KEYS` | empty | Official keys, pooled with CLI accounts |
| `GROK_COOKIES` 🧪 | empty | grok.com subscription cookies (comma-separated for multiple accounts) |
| `CURSOR_ACCESS_TOKEN(S)` 🧪 | empty | Cursor session token |
| `GEMINI_OAUTH_ENABLED` 🧪 | `false` | Enable gemini-cli |
| `GEMINI_OAUTH_CLIENT_ID` / `_SECRET` 🧪 | empty | gemini-cli public OAuth client (not bundled in this repo; bring your own) |
| `COPILOT_GITHUB_TOKENS` | empty | Extra GitHub tokens (comma-separated) |

### Vendor API Keys (enabled by supplying a key; 20+ vendors)
`DEEPSEEK_API_KEYS`, `MOONSHOT_API_KEYS`, `ZHIPU_API_KEYS`, `QWEN_API_KEYS`, `BAIDU_API_KEYS`,
`SENSETIME_API_KEYS`, `SKYWORK_API_KEYS`, `AI360_API_KEYS`, `MINIMAX_API_KEYS`, `DOUBAO_API_KEYS`,
`HUNYUAN_API_KEYS`, `SPARK_API_KEYS`, `STEPFUN_API_KEYS`, `YI_API_KEYS`, `BAICHUAN_API_KEYS`,
`SILICONFLOW_API_KEYS`, `GEMINI_API_KEYS`, `AWS_BEDROCK_API_KEYS` (+`AWS_BEDROCK_BASE_URL`),
`AGNES_API_KEYS`, `OPENROUTER_API_KEYS`, `XAI_API_KEYS` (official Grok, +`XAI_BASE_URL`, `GROK_MODELS`).
Each vendor also supports `<VENDOR>_MODELS` to override its model list.

### Custom Relays
| Variable | Description |
|---|---|
| `CUSTOM_PROVIDERS` | Inline JSON array, see below |
| `CUSTOM_PROVIDERS_FILE` | Path to a JSON file |
| `CREDS_ROOT` | Root directory allowed for keyFile (defaults to HOME) |
| `ALLOW_ANY_KEYFILE` | `1` disables the keyFile path-traversal check |

```json
[{"id":"myrelay","baseUrl":"https://api.example.com","models":["gpt-4o"],
  "apiKeys":["sk-xxx"],"ownedBy":"me","authHeader":"authorization",
  "headers":{"x-foo":"bar"}}]
```
`apiKeys` can also use `keysEnv` (read from an environment variable) or `keyFile` (read from a file, reusing a third-party CLI token).

For a full example see [.env.example](../.env.example).

---

## 5. Building the Docker Image

The repo ships a multi-stage [Dockerfile](../Dockerfile): `golang:1.26-alpine` builds → `scratch` runs,
`CGO_ENABLED=0` static binary + CA certificates, with a final image of **≈7MB** running as a non-root uid.

### 5.1 Same-Architecture Build on the Local Machine
```bash
cd apiforge
docker build -t apiforge:latest .
docker images apiforge     # 查看大小
```

### 5.2 Cross-Building arm64 (for the Raspberry Pi)
```bash
# 需 buildx（Docker Desktop 自带）
docker buildx build --platform linux/arm64 -t apiforge:arm64 --load .
```

### 5.3 Machines Without Docker: Cross-Compile the Binary → Bake It Into an Image on the Target Machine
On a Mac / without Docker, first produce the binary, then build a minimal image on a target machine that has Docker:
```bash
# 在 Mac 上交叉编译（约 6.8MB）
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -ldflags="-s -w" -o apiforge-arm64 ./cmd/apiforge

# 传到目标机后，用只 COPY 二进制的极简 Dockerfile（不在目标机编译 Go，省内存）：
cat > Dockerfile.prebuilt <<'EOF'
FROM scratch
COPY --from=alpine:latest /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY apiforge-arm64 /apiforge
ENV GOMEMLIMIT=64MiB HOST=0.0.0.0 PORT=8899
EXPOSE 8899
USER 65532:65532
ENTRYPOINT ["/apiforge"]
EOF
docker build -f Dockerfile.prebuilt -t apiforge:arm64 .
```
> Tip: `--from=alpine` is only there to grab the CA certificates. You can also place `ca-certificates.crt` in the build directory beforehand and COPY it directly.

---

## 6. Running and Configuring Docker

Inside the image the defaults are `HOST=0.0.0.0`, `PORT=8899`, `GOMEMLIMIT=64MiB`, and non-root uid `65532`.
**Always publish the port to `127.0.0.1` only**, and let the reverse proxy in §10 handle authentication / multi-user access.

### 6.1 Key Point: How Credentials Get Into the Container
The `scratch` image **has no `/root` home directory and no `/etc/passwd`**, so auto-detecting things like `~/.codex`
**does not work inside the container**. Two correct approaches (pick either):

**A. Explicit paths (recommended)** — mount the credential directory and point `*_AUTHS` at the mount point:
```bash
docker run -d --name apiforge \
  -p 127.0.0.1:8899:8899 \
  -e API_KEYS=sk-my-secret \
  -e CODEX_AUTHS=/creds/codex/auth.json \
  -e CLAUDE_AUTHS=/creds/claude/.credentials.json \
  -v "$HOME/.codex:/creds/codex" \
  -v "$HOME/.claude:/creds/claude" \
  apiforge:latest
```

**B. Set HOME** — so auto-detection works:
```bash
docker run -d --name apiforge \
  -p 127.0.0.1:8899:8899 \
  -e API_KEYS=sk-my-secret -e HOME=/creds \
  -v "$HOME/.codex:/creds/.codex" \
  -v "$HOME/.claude:/creds/.claude" \
  apiforge:latest
```

> **Read-write permission**: after an OAuth refresh, apiforge **atomically writes the new token back** to the credential file to stay
> in sync with the CLI. If you use a `:ro` read-only mount, the refresh still takes effect in memory but cannot be persisted (a warn
> appears in the log, and once the token expires you must supply it again). To persist, use a **writable** mount (drop `:ro`), and make
> sure the container uid `65532` can write to that directory
> (or add `--user 0:0` to run as root — trading least-privilege for convenience).

### 6.2 Environment-Variable-Only Sources (No Mount Needed)
grok-web / cursor / each vendor's keys use environment variables only, with no volume needed:
```bash
docker run -d --name apiforge -p 127.0.0.1:8899:8899 \
  -e API_KEYS=sk-my-secret \
  -e DEEPSEEK_API_KEYS=sk-xxx \
  -e XAI_API_KEYS=xai-xxx \
  -e GROK_COOKIES='sso=eyJ...' \
  apiforge:latest
```

### 6.3 Using an env File
```bash
cp .env.example my.env && vim my.env      # 填好后（注意去掉注释里的示例）
docker run -d --name apiforge -p 127.0.0.1:8899:8899 --env-file my.env \
  -v "$HOME/.codex:/creds/codex" -e CODEX_AUTHS=/creds/codex/auth.json \
  apiforge:latest
```

### 6.4 Tuning
```bash
-e MAX_ACCOUNT_CONCURRENCY=3 -e QUEUE_WAIT_MS=60000 \
-e GOMEMLIMIT=48MiB --memory=128m           # 低内存机限制容器内存
```

---

## 7. docker-compose

```yaml
# docker-compose.yml
services:
  apiforge:
    build: .                       # 或 image: apiforge:latest
    container_name: apiforge
    restart: unless-stopped
    ports:
      - "127.0.0.1:8899:8899"      # 只对本机；对外走反代
    environment:
      API_KEYS: "sk-my-secret"
      ADMIN_TOKEN: "admin-secret"
      MAX_ACCOUNT_CONCURRENCY: "3"
      QUEUE_WAIT_MS: "60000"
      GOMEMLIMIT: "64MiB"
      CODEX_AUTHS: "/creds/codex/auth.json"
      CLAUDE_AUTHS: "/creds/claude/.credentials.json"
      # DEEPSEEK_API_KEYS: "sk-xxx"
      # GROK_COOKIES: "sso=eyJ..."
    volumes:
      - "${HOME}/.codex:/creds/codex"          # 可写以持久化刷新后的 token
      - "${HOME}/.claude:/creds/claude"
    mem_limit: 128m
```
```bash
docker compose up -d && docker compose logs -f
```

---

## 8. Bare Binary + systemd (Lowest Memory, Recommended for Raspberry Pi)

```bash
# 目标机上放好二进制 /opt/apiforge/apiforge 与 /opt/apiforge/apiforge.env
sudo tee /etc/systemd/system/apiforge.service >/dev/null <<'EOF'
[Unit]
Description=apiforge gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=devops
EnvironmentFile=/opt/apiforge/apiforge.env
ExecStart=/opt/apiforge/apiforge
Restart=on-failure
RestartSec=3
# 内存/安全加固
MemoryMax=128M
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/home/devops/.codex /home/devops/.claude
ProtectHome=read-only

[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable --now apiforge
journalctl -u apiforge -f
```
The contents of `apiforge.env` are the environment variables from §4 (one `KEY=VALUE` per line). `ReadWritePaths` must include the
credential directory, otherwise refreshed tokens cannot be written back.

---

## 9. Raspberry Pi Deployment

1. Cross-compile (§5.3) or `docker build` on the Pi (a 1GB Pi is tight for building Go, so prefer cross-compiling and transferring the binary).
2. Prefer **bare binary + systemd** (§8) for the lowest memory; or a minimal image (§5.3 + §6).
3. Credentials: `scp` the `auth.json` / `.credentials.json` you logged in on your desktop over to the Pi and point `*_AUTHS` at them;
   for grok/cursor pass the token via environment variables.
4. Recommended `GOMEMLIMIT=48–64MiB`; set `MAX_ACCOUNT_CONCURRENCY` based on the number of accounts.
5. Always use a compliant 5V/3A power supply (undervoltage causes throttling and occasional timeouts).

---

## 10. Fronting with new-api / Cloudflare Tunnel

apiforge only "turns subscriptions into a standard API"; leave **multi-user / billing / public entry point** to the fronting layer:

```
公网用户 → Cloudflare Tunnel → new-api(多用户+计费) → apiforge(127.0.0.1:8899) → 各厂商
```

- apiforge listens on `127.0.0.1` only, with a strong `API_KEYS` (used by new-api).
- In new-api, configure apiforge as an OpenAI channel with base `http://127.0.0.1:8899/v1`,
  and the key set to apiforge's `API_KEYS`.
- Point the Cloudflare Tunnel at new-api, not directly at apiforge.

> ⚠️ Serving publicly significantly amplifies the risk of account bans (see the disclaimer). Recommended for personal / small-scale research only.

---

## 11. Health Checks and Monitoring

- `GET /health` (**no authentication required**) returns the ready / disabled providers and models, for liveness checks.
- `GET /admin/accounts` (requires `ADMIN_TOKEN`) shows each account's in-flight count / cooldown / disabled state.
- **The scratch image has no shell**, so an in-container `HEALTHCHECK curl` cannot be used. Instead:
  - Probe from the host: `curl -fsS http://127.0.0.1:8899/health`;
  - Or have the orchestration layer probe TCP port 8899;
  - Or switch to the `gcr.io/distroless/static` base image and use an external probe.
- Logs are JSON (slog), with tokens / absolute paths already redacted, so they can be fed straight into log collection.

---

## 12. Upgrade and Rollback

- **Binary**: replace `/opt/apiforge/apiforge` → `systemctl restart apiforge`. Keep the previous binary for rollback.
- **Docker**: `docker build` a new tag → `docker compose up -d` (rolling); rollback = switch back to the old tag.
- Configuration lives entirely in environment variables, so upgrades never touch credentials; graceful shutdown waits for in-flight requests (SIGTERM, 10s).

---

## 13. Troubleshooting

| Symptom | Diagnosis |
|---|---|
| Exits immediately with `refusing to start` | `API_KEYS` not set while bound to a non-loopback address. Set `API_KEYS` or `HOST=127.0.0.1` |
| A source shows `disabled` in `/health` | Check `reason`: usually missing/expired credentials or a failed refresh; log in to that CLI again or check the `*_AUTHS` path |
| CLI credentials not detected in Docker | scratch has no HOME; you must use an explicit `*_AUTHS` path or set `HOME` (§6.1) |
| Token not written back after refresh | The credential mount is `:ro` or the uid has no write permission; switch to a writable mount / `ReadWritePaths` |
| grok-web returns 403 / a Cloudflare challenge | The Go TLS fingerprint is blocked; add `cf_clearance` to `GROK_COOKIES` (§3) |
| Occasional 503 "busy" | All accounts are busy and `QUEUE_WAIT_MS` was exceeded; raise the concurrency cap / add accounts / increase `QUEUE_WAIT_MS` |
| 503 "unavailable" | All accounts are in cooldown (429/auth failure); check cooldown times via `/admin/accounts` |
| Codex rejects a new model with "please upgrade" | Raise `CODEX_CLIENT_VERSION` to match your local Codex CLI version |
| Model 404 no provider | That model has no ready provider; check the available list via `/v1/models`, and mind the `copilot/` and `grok-web/` prefixes |
| Memory runs high | Set `GOMEMLIMIT`; a streaming request holding an account slot until the stream ends is normal |
