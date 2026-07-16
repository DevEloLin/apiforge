**English** | [中文](./USAGE.zh-CN.md)

# apiforge Usage Guide (Client API)

> For deployment/configuration/Docker, see [OPERATIONS.md](./OPERATIONS.md).

## Table of Contents
1. [Authentication](#1-authentication)
2. [Endpoint Overview](#2-endpoint-overview)
3. [Model Naming and Routing](#3-model-naming-and-routing)
4. [Chat Completions](#4-chat-completions)
5. [Streaming Responses](#5-streaming-responses)
6. [Responses API (Codex)](#6-responses-api-codex)
7. [Anthropic Messages (Claude)](#7-anthropic-messages-claude)
8. [Image Generation / Image-to-Image (Codex)](#8-image-generation--image-to-image-codex)
9. [Quick Reference by Source](#9-quick-reference-by-source)
10. [Multi-Account Control (Headers + Admin API)](#10-multi-account-control-headers--admin-api)
11. [Integrating with Popular Clients / SDKs](#11-integrating-with-popular-clients--sdks)
12. [Error Format](#12-error-format)

---

## 1. Authentication

Except for `GET /health`, all `/v1/*` requests must carry one of the keys from `API_KEYS`:

```
Authorization: Bearer sk-my-secret
```

The Anthropic-style `x-api-key: sk-my-secret` is also accepted. `/admin/*` uses `ADMIN_TOKEN` (likewise
`Authorization: Bearer <ADMIN_TOKEN>`).

Base address (default): `http://127.0.0.1:8899`.

---

## 2. Endpoint Overview

| Method | Path | Description | Served by |
|---|---|---|---|
| GET | `/health` | Ready providers / models (**no auth required**) | All |
| GET | `/v1/models` | Aggregated models from all ready sources | All |
| POST | `/v1/chat/completions` | OpenAI Chat (general entry point) | Routed by model |
| POST | `/v1/responses` | OpenAI Responses API | codex |
| POST | `/v1/messages` | Anthropic native Messages | claude |
| POST | `/v1/messages/count_tokens` | Anthropic token counting | claude |
| POST | `/v1/images/generations` | Text-to-image (JSON) | codex |
| POST | `/v1/images/edits` | Image-to-image (multipart) | codex |
| GET | `/admin/providers` | List of ready providers | Admin |
| GET | `/admin/accounts` | Account health snapshot | Admin |
| POST | `/admin/accounts/preferred` | Set a preferred account | Admin |
| POST | `/admin/accounts/enabled` | Enable/disable an account | Admin |

First, check which models are available:

```bash
curl -s http://127.0.0.1:8899/health | jq '.providers[] | {id, models: (.models|length)}'
curl -s -H "Authorization: Bearer sk-my-secret" http://127.0.0.1:8899/v1/models | jq '.data[].id'
```

---

## 3. Model Naming and Routing

The gateway routes based on the `model` field in the request to the **ready** provider that owns that model (earlier-registered providers take precedence).

| Prefix / Rule | Routes to | Example |
|---|---|---|
| `gpt-*` / `o3*` / `o4*` / `codex*` / `gpt-image-*` | codex | `gpt-5.4-mini`, `gpt-image-2` |
| `claude*` | claude | `claude-sonnet-5` |
| `copilot/<model>` | copilot | `copilot/gpt-4o` |
| `grok-web/<model>` | grok-web 🧪 | `grok-web/grok-4.2` |
| `cursor/<model>` | cursor 🧪 | `cursor/claude-4.5-sonnet` |
| `gemini*` | gemini-cli 🧪 or gemini(key) | `gemini-2.5-pro` |
| Exact vendor model names | Corresponding vendor | `deepseek-chat`, `grok-4` (xAI key) |

> The `copilot/`, `grok-web/`, and `cursor/` **prefixes are required** to distinguish them from other sources; the gateway strips the prefix before forwarding.

---

## 4. Chat Completions

```bash
curl http://127.0.0.1:8899/v1/chat/completions \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.4-mini",
    "messages": [
      {"role": "system", "content": "你是简洁的助手。"},
      {"role": "user", "content": "用一句话解释量子纠缠。"}
    ],
    "temperature": 0.7
  }'
```

Supported fields (mapped best-effort per source): `messages`, `temperature`, `top_p`, `max_tokens` /
`max_completion_tokens`, `stop`, `tools` / `tool_choice`, `stream`, `reasoning_effort` (codex).
Multimodal: `content` can be an array containing `{"type":"text"}` and `{"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}`.

Returns a standard OpenAI `chat.completion` object (including `usage` and `tool_calls`).

---

## 5. Streaming Responses

Add `"stream": true` to receive `text/event-stream` (SSE), a `chat.completion.chunk` per frame, ending with
`data: [DONE]`:

```bash
curl -N http://127.0.0.1:8899/v1/chat/completions \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-5","messages":[{"role":"user","content":"数到5"}],"stream":true}'
```

> A streaming request **holds the account's concurrency slot until the stream ends** (normal behavior, to prevent over-issuance caused by releasing the slot too early).

---

## 6. Responses API (Codex)

```bash
curl http://127.0.0.1:8899/v1/responses \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.5","input":"写一句诗","stream":false}'
```
When `stream:true`, Codex's Responses SSE is passed through verbatim; when `false`, it is aggregated into a single `response` object.

---

## 7. Anthropic Messages (Claude)

Native Anthropic protocol (passed through to the upstream; in OAuth mode it automatically injects the Claude Code identity and cleans up the fingerprint):

```bash
curl http://127.0.0.1:8899/v1/messages \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-5","max_tokens":200,
       "messages":[{"role":"user","content":"你好"}]}'

# count_tokens
curl http://127.0.0.1:8899/v1/messages/count_tokens \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hi"}]}'
```

> You can also call Claude via the OpenAI protocol (`/v1/chat/completions` with `model` set to `claude-*`); the gateway performs
> bidirectional OpenAI↔Anthropic translation.

---

## 8. Image Generation / Image-to-Image (Codex)

Text-to-image (JSON):

```bash
curl http://127.0.0.1:8899/v1/images/generations \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"gpt-image-2","prompt":"一辆红色自行车,工作室灯光","n":1,"size":"1024x1024"}'
```
Returns `data[].b64_json` (base64 PNG). `n` is capped at 4; multiple images are generated concurrently, each with its own account retry.

Image-to-image / edit (multipart):

```bash
curl http://127.0.0.1:8899/v1/images/edits \
  -H "Authorization: Bearer sk-my-secret" \
  -F model=gpt-image-2 \
  -F prompt="把背景换成夜空" \
  -F image[]=@input.png \
  -F mask=@mask.png            # optional
```
When `model` is omitted, it defaults to `gpt-image-1`.

---

## 9. Quick Reference by Source

| Source | Endpoints | Example model | Notes |
|---|---|---|---|
| codex (ChatGPT subscription) | chat / responses / images | `gpt-5.4-mini`, `gpt-image-2` | Text + image generation + image-to-image |
| claude (Claude Code subscription) | chat / messages / count_tokens | `claude-sonnet-5` | Dual protocol |
| copilot | chat | `copilot/gpt-4o` | See `/v1/models` for the full list (with prefix) |
| qwen-cli | chat | `coder-model`, `qwen...` | OpenAI compatible |
| grok-web 🧪 | chat | `grok-web/grok-4.2` | Reuses grok.com subscription |
| cursor 🧪 | chat | `cursor/claude-4.5-sonnet` | Reverse-engineered protobuf |
| gemini-cli 🧪 | chat | `gemini-2.5-pro` | Requires enabling + your own client |
| Vendor key | chat | `deepseek-chat`, `grok-4` (xAI) | Configure `<VENDOR>_API_KEYS` |

---

## 10. Multi-Account Control (Headers + Admin API)

**Per-request** level:
- `x-apiforge-account: codex#2` — force this request to use the specified account.
- `x-apiforge-session: <session-id>` — session stickiness: requests with the same id are routed to the same account (requires
  `STICKY_TTL_SECONDS>0`), which helps with upstream cache hits and rate-limit distribution.

**Admin API** (requires `ADMIN_TOKEN`):

```bash
ADMIN=(-H "Authorization: Bearer admin-secret")
curl -s "${ADMIN[@]}" http://127.0.0.1:8899/admin/providers | jq
curl -s "${ADMIN[@]}" http://127.0.0.1:8899/admin/accounts  | jq

# Set the preferred codex account (pass an empty string for account to clear it)
curl -s "${ADMIN[@]}" -H "Content-Type: application/json" \
  -d '{"provider":"codex","account":"codex#1"}' \
  http://127.0.0.1:8899/admin/accounts/preferred

# Temporarily disable an account (e.g. when its daily quota is exhausted)
curl -s "${ADMIN[@]}" -H "Content-Type: application/json" \
  -d '{"provider":"codex","account":"codex#2","enabled":false}' \
  http://127.0.0.1:8899/admin/accounts/enabled
```
Account ids take the form `<provider>#<n>` (CLI accounts) or `<provider>-key#<n>` (API key accounts),
and are visible in `/admin/accounts`.

---

## 11. Integrating with Popular Clients / SDKs

Point the base URL at `http://127.0.0.1:8899/v1` and use a value from `API_KEYS` as the api key.

**OpenAI Python SDK:**
```python
from openai import OpenAI
client = OpenAI(base_url="http://127.0.0.1:8899/v1", api_key="sk-my-secret")
r = client.chat.completions.create(
    model="gpt-5.4-mini",
    messages=[{"role": "user", "content": "hi"}],
)
print(r.choices[0].message.content)
```

**Anthropic Python SDK:**
```python
import anthropic
c = anthropic.Anthropic(base_url="http://127.0.0.1:8899", api_key="sk-my-secret")
m = c.messages.create(model="claude-sonnet-5", max_tokens=200,
                      messages=[{"role":"user","content":"hi"}])
```

**Cherry Studio / NextChat / other clients:** Create a new OpenAI-compatible channel, set the API endpoint to
`http://127.0.0.1:8899` (or `.../v1`), set the key to `API_KEYS`, and enter models manually or pull them from `/v1/models`.

**new-api / one-api:** See [OPERATIONS.md §10](./OPERATIONS.md).

---

## 12. Error Format

- OpenAI endpoints: `{"error":{"message":"...","type":"..."}}`
- Anthropic endpoint (`/v1/messages`): `{"type":"error","error":{"type":"...","message":"..."}}`

Common status codes:
- `401` invalid key; `404` no provider serves the model; `400` bad request / capability not supported;
- `429` client rate limit (`RATE_LIMIT_RPM`);
- `503` all upstream accounts busy (queue timeout) or all in cooldown/unavailable;
- `502` upstream request failed.

For more troubleshooting, see [OPERATIONS.md §13](./OPERATIONS.md).
