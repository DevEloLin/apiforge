[English](./USAGE.md) | **中文**

# apiforge 使用手册（客户端调用）

> 面向调用方。部署/配置/Docker 见 [OPERATIONS.md](./OPERATIONS.zh-CN.md)。

## 目录
1. [认证](#1-认证)
2. [端点总览](#2-端点总览)
3. [模型命名与路由](#3-模型命名与路由)
4. [Chat Completions](#4-chat-completions)
5. [流式响应](#5-流式响应)
6. [Responses API（Codex）](#6-responses-apicodex)
7. [Anthropic Messages（Claude）](#7-anthropic-messagesclaude)
8. [图像生成 / 图生图（Codex）](#8-图像生成--图生图codex)
9. [按来源用法速查](#9-按来源用法速查)
10. [多账户控制（请求头 + 管理 API）](#10-多账户控制请求头--管理-api)
11. [用主流客户端 / SDK 接入](#11-用主流客户端--sdk-接入)
12. [错误格式](#12-错误格式)

---

## 1. 认证

除 `GET /health` 外，所有 `/v1/*` 都需要携带 `API_KEYS` 里的某个密钥：

```
Authorization: Bearer sk-my-secret
```

Anthropic 风格的 `x-api-key: sk-my-secret` 也接受。`/admin/*` 用 `ADMIN_TOKEN`（同样
`Authorization: Bearer <ADMIN_TOKEN>`）。

基础地址（默认）：`http://127.0.0.1:8899`。

---

## 2. 端点总览

| 方法 | 路径 | 说明 | 由谁服务 |
|---|---|---|---|
| GET | `/health` | 就绪 provider / 模型（**免鉴权**） | 全部 |
| GET | `/v1/models` | 聚合所有就绪来源的模型 | 全部 |
| POST | `/v1/chat/completions` | OpenAI Chat（通用入口） | 按模型路由 |
| POST | `/v1/responses` | OpenAI Responses API | codex |
| POST | `/v1/messages` | Anthropic 原生 Messages | claude |
| POST | `/v1/messages/count_tokens` | Anthropic 计数 | claude |
| POST | `/v1/images/generations` | 文生图（JSON） | codex |
| POST | `/v1/images/edits` | 图生图（multipart） | codex |
| GET | `/admin/providers` | 就绪 provider 列表 | 管理 |
| GET | `/admin/accounts` | 账户健康快照 | 管理 |
| POST | `/admin/accounts/preferred` | 指定优先账户 | 管理 |
| POST | `/admin/accounts/enabled` | 启停账户 | 管理 |

先看有哪些模型可用：

```bash
curl -s http://127.0.0.1:8899/health | jq '.providers[] | {id, models: (.models|length)}'
curl -s -H "Authorization: Bearer sk-my-secret" http://127.0.0.1:8899/v1/models | jq '.data[].id'
```

---

## 3. 模型命名与路由

网关按请求里的 `model` 字段路由到拥有该模型的**就绪** provider（先注册者优先）。

| 前缀 / 规则 | 路由到 | 例 |
|---|---|---|
| `gpt-*` / `o3*` / `o4*` / `codex*` / `gpt-image-*` | codex | `gpt-5.4-mini`、`gpt-image-2` |
| `claude*` | claude | `claude-sonnet-5` |
| `copilot/<model>` | copilot | `copilot/gpt-4o` |
| `grok-web/<model>` | grok-web 🧪 | `grok-web/grok-4.2` |
| `cursor/<model>` | cursor 🧪 | `cursor/claude-4.5-sonnet` |
| `gemini*` | gemini-cli 🧪 或 gemini(key) | `gemini-2.5-pro` |
| 各厂商精确模型名 | 对应 vendor | `deepseek-chat`、`grok-4`（xAI key） |

> `copilot/`、`grok-web/`、`cursor/` **前缀是必须的**，用于与其他来源区分；网关转发前会去掉前缀。

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

支持字段（按来源尽力映射）：`messages`、`temperature`、`top_p`、`max_tokens` /
`max_completion_tokens`、`stop`、`tools` / `tool_choice`、`stream`、`reasoning_effort`（codex）。
多模态：`content` 可为数组，含 `{"type":"text"}` 与 `{"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}`。

返回标准 OpenAI `chat.completion` 对象（含 `usage`、`tool_calls`）。

---

## 5. 流式响应

加 `"stream": true`，返回 `text/event-stream`（SSE），逐帧 `chat.completion.chunk`，以
`data: [DONE]` 结束：

```bash
curl -N http://127.0.0.1:8899/v1/chat/completions \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-5","messages":[{"role":"user","content":"数到5"}],"stream":true}'
```

> 流式请求会**占用账户并发槽位直到流结束**（正常行为，防止提前释放导致超发）。

---

## 6. Responses API（Codex）

```bash
curl http://127.0.0.1:8899/v1/responses \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.5","input":"写一句诗","stream":false}'
```
`stream:true` 时原样透传 Codex 的 Responses SSE；`false` 时聚合为单个 `response` 对象。

---

## 7. Anthropic Messages（Claude）

原生 Anthropic 协议（透传到上游，OAuth 模式会自动注入 Claude Code 身份并清理指纹）：

```bash
curl http://127.0.0.1:8899/v1/messages \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-5","max_tokens":200,
       "messages":[{"role":"user","content":"你好"}]}'

# 计数
curl http://127.0.0.1:8899/v1/messages/count_tokens \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hi"}]}'
```

> 也可以用 OpenAI 协议（`/v1/chat/completions`，model 填 `claude-*`）调 Claude，网关会做
> OpenAI↔Anthropic 双向翻译。

---

## 8. 图像生成 / 图生图（Codex）

文生图（JSON）：

```bash
curl http://127.0.0.1:8899/v1/images/generations \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"gpt-image-2","prompt":"一辆红色自行车,工作室灯光","n":1,"size":"1024x1024"}'
```
返回 `data[].b64_json`（base64 PNG）。`n` 最多 4，多张会并发出图并各自走账户重试。

图生图 / 编辑（multipart）：

```bash
curl http://127.0.0.1:8899/v1/images/edits \
  -H "Authorization: Bearer sk-my-secret" \
  -F model=gpt-image-2 \
  -F prompt="把背景换成夜空" \
  -F image[]=@input.png \
  -F mask=@mask.png            # 可选
```
`model` 省略时默认 `gpt-image-1`。

---

## 9. 按来源用法速查

| 来源 | 端点 | model 示例 | 备注 |
|---|---|---|---|
| codex（ChatGPT 订阅） | chat / responses / images | `gpt-5.4-mini`、`gpt-image-2` | 文本+出图+图生图 |
| claude（Claude Code 订阅） | chat / messages / count_tokens | `claude-sonnet-5` | 双协议 |
| copilot | chat | `copilot/gpt-4o` | `/v1/models` 看全部（带前缀） |
| qwen-cli | chat | `coder-model`、`qwen...` | OpenAI 兼容 |
| grok-web 🧪 | chat | `grok-web/grok-4.2` | 复用 grok.com 订阅 |
| cursor 🧪 | chat | `cursor/claude-4.5-sonnet` | 逆向 protobuf |
| gemini-cli 🧪 | chat | `gemini-2.5-pro` | 需开启 + 自备 client |
| 厂商 key | chat | `deepseek-chat`、`grok-4`(xAI) | 配 `<VENDOR>_API_KEYS` |

---

## 10. 多账户控制（请求头 + 管理 API）

**单次请求**级：
- `x-apiforge-account: codex#2` —— 强制本次用指定账户。
- `x-apiforge-session: <会话id>` —— 会话粘滞：同一 id 的请求路由到同一账户（需
  `STICKY_TTL_SECONDS>0`），利于上游缓存命中与限流分摊。

**管理 API**（需 `ADMIN_TOKEN`）：

```bash
ADMIN=(-H "Authorization: Bearer admin-secret")
curl -s "${ADMIN[@]}" http://127.0.0.1:8899/admin/providers | jq
curl -s "${ADMIN[@]}" http://127.0.0.1:8899/admin/accounts  | jq

# 指定 codex 优先账户（account 传空字符串则清除）
curl -s "${ADMIN[@]}" -H "Content-Type: application/json" \
  -d '{"provider":"codex","account":"codex#1"}' \
  http://127.0.0.1:8899/admin/accounts/preferred

# 临时禁用某账户（如它当天配额用尽）
curl -s "${ADMIN[@]}" -H "Content-Type: application/json" \
  -d '{"provider":"codex","account":"codex#2","enabled":false}' \
  http://127.0.0.1:8899/admin/accounts/enabled
```
账户 id 形如 `<provider>#<n>`（CLI 账户）或 `<provider>-key#<n>`（API key 账户），
在 `/admin/accounts` 里可见。

---

## 11. 用主流客户端 / SDK 接入

把 base URL 指向 `http://127.0.0.1:8899/v1`，api key 用 `API_KEYS` 里的值即可。

**OpenAI Python SDK：**
```python
from openai import OpenAI
client = OpenAI(base_url="http://127.0.0.1:8899/v1", api_key="sk-my-secret")
r = client.chat.completions.create(
    model="gpt-5.4-mini",
    messages=[{"role": "user", "content": "hi"}],
)
print(r.choices[0].message.content)
```

**Anthropic Python SDK：**
```python
import anthropic
c = anthropic.Anthropic(base_url="http://127.0.0.1:8899", api_key="sk-my-secret")
m = c.messages.create(model="claude-sonnet-5", max_tokens=200,
                      messages=[{"role":"user","content":"hi"}])
```

**Cherry Studio / NextChat / 各类客户端：** 新建 OpenAI 兼容渠道，接口地址填
`http://127.0.0.1:8899`（或 `.../v1`），密钥填 `API_KEYS`，模型手填或从 `/v1/models` 拉取。

**new-api / one-api：** 见 [OPERATIONS.md §10](./OPERATIONS.zh-CN.md#10-前置-new-api--cloudflare-tunnel)。

---

## 12. 错误格式

- OpenAI 端点：`{"error":{"message":"...","type":"..."}}`
- Anthropic 端点（`/v1/messages`）：`{"type":"error","error":{"type":"...","message":"..."}}`

常见状态码：
- `401` 密钥无效；`404` 无 provider 服务该模型；`400` 请求错误 / 能力不支持；
- `429` 客户端限流（`RATE_LIMIT_RPM`）；
- `503` 上游账户全忙（排队超时）或全部冷却不可用；
- `502` 上游请求失败。

更多排错见 [OPERATIONS.md §13](./OPERATIONS.zh-CN.md#13-故障排查)。
