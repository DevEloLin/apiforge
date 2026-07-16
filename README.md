# apiforge

**把你本机各家 AI CLI / 订阅登录，聚合成一个统一的 OpenAI / Anthropic 兼容 API 网关。**
单文件 Go 二进制，无前端、无数据库，静态镜像约 7MB，可跑在 1GB 树莓派上。

> English TL;DR: apiforge is a lean, single-binary Go gateway that **reuses your
> local AI CLI / subscription logins** (Codex/Claude/Copilot/Gemini/Qwen/Cursor/Grok)
> and re-exposes them behind one OpenAI- and Anthropic-compatible API. It also
> relays plain API keys for 20+ vendors. **For personal research only. Reusing
> subscription logins this way may violate those vendors' Terms of Service — see
> the disclaimer below.**

---

## ⚠️ 重要提醒（务必先读）

使用本项目前，请务必仔细阅读以下内容：

- 🚨 **服务条款风险**：使用本项目可能违反 Anthropic、OpenAI、Google、GitHub、xAI、
  Cursor、阿里等上游服务商的服务条款。请在使用前仔细阅读相关服务商的用户协议，由此
  产生的一切风险由用户自行承担。
- ⚖️ **合规使用**：请在符合您所在国家或地区法律法规的前提下使用本项目，严禁将其用于
  任何违法违规用途。
- 📖 **免责声明**：本项目仅供技术学习与研究使用，作者不对因使用本项目导致的账户封禁、
  服务中断、数据丢失或其他任何直接或间接损失承担责任。
- 🚫 **无商业授权**：本项目从未授权任何个人或组织基于本项目开展任何形式的商业化运营。
  任何以本项目名义或基于本项目从事的商业行为均与本项目及其开发者无关，由此产生的一切
  纠纷、损失和法律责任由行为主体自行承担。

补充说明：

- 本项目**不含、不分发**任何厂商的账号、密钥或订阅；一切凭据由使用者自备（用你自己
  已登录的本机 CLI）。
- 逆向部分（Cursor / Grok 网页协议等）基于公开的第三方研究实现，标注为
  **实验性（EXPERIMENTAL）**，随厂商改动随时可能失效。

> 开源许可与**来源署名不可移除**的要求见文末「许可与署名」及 [LICENSE](./LICENSE)。

---

## 目录
- [它解决什么问题](#它解决什么问题)
- [支持哪些订阅 / 来源](#支持哪些订阅--来源)
- [快速开始](#快速开始)
- [各来源怎么准备登录](#各来源怎么准备登录)
- [配置项（环境变量）](#配置项环境变量)
- [使用示例](#使用示例)
- [账户池 / 并发 / 排队](#账户池--并发--排队)
- [管理 API](#管理-api)
- [安全设计](#安全设计)
- [部署到树莓派](#部署到树莓派)
- [许可与署名](#许可与署名)
- [来源与致谢](#来源与致谢)

---

## 它解决什么问题

你在本机登录了 Codex / Claude Code / Copilot / Gemini CLI 等订阅工具。apiforge 读取
它们的本地登录凭据（并在需要时自动刷新 OAuth token），把这些订阅**统一暴露成一套标准
API**，于是任何支持 OpenAI / Anthropic 协议的客户端都能直接调用：

```
你的客户端 / new-api / 脚本
        │  OpenAI / Anthropic 兼容协议 (sk-...)
        ▼
   ┌─────────────┐   复用本机登录 + 自刷新 token
   │  apiforge   │──────────────────────────────►  各厂商后端
   └─────────────┘   账户池 · 自动切换 · 并发帽 · 排队
```

- **对外协议统一**：`/v1/chat/completions`、`/v1/responses`、`/v1/messages`、
  `/v1/messages/count_tokens`、`/v1/images/generations`、`/v1/images/edits`、`/v1/models`。
- **多账户池**：同一来源可放多个账户，自动轮询 + 失败冷却切换 + 手动指定。
- **不跑 CLI 进程**：只读凭据文件并用 HTTP 自刷新 token，内存占用极低。

---

## 支持哪些订阅 / 来源

### A. 订阅 / CLI 登录复用（核心能力）

| 来源 | provider id | 复用方式 | 能力 | 状态 |
|---|---|---|---|---|
| **ChatGPT / Codex 订阅** | `codex` | Codex CLI OAuth（`~/.codex/auth.json`）自刷新 | chat · responses · 文生图 · 图生图 | ✅ 实测 |
| **Claude（Claude Code 订阅）** | `claude` | Claude Code OAuth（`~/.claude/.credentials.json`）自刷新 | chat（OpenAI↔Anthropic 翻译）· 原生 `/v1/messages` · count_tokens | ✅ 已实现 |
| **GitHub Copilot 订阅** | `copilot` | GitHub OAuth token → 交换 Copilot token | chat（自动发现全部可用模型） | ✅ 实测 |
| **Grok（grok.com 订阅）** | `grok-web` | 复用 `sso` 会话 cookie | chat（流式 / 非流） | 🧪 实验 |
| **Cursor 订阅** | `cursor` | 会话 token（Connect-RPC / protobuf） | chat | 🧪 实验 |
| **Gemini（Google 账号）** | `gemini-cli` | Gemini CLI OAuth（Code Assist） | chat | 🧪 实验（默认关，`GEMINI_OAUTH_ENABLED=1` 开启） |
| **Qwen Code** | `qwen-cli` | Qwen Code CLI OAuth 自刷新 | chat | ✅ 已实现 |

### B. 官方 / 兼容 API Key 直连（供 key 即启用）

- **OpenAI**（`OPENAI_API_KEYS`，走 codex 的 key 模式）、**Anthropic**（`ANTHROPIC_API_KEYS`）。
- **20+ OpenAI 兼容厂商**（配对应 `*_API_KEYS` 即启用）：DeepSeek、Kimi(Moonshot)、
  智谱 GLM、通义千问、文心一言、商汤、昆仑天工、360、MiniMax、豆包、混元、讯飞星火、
  阶跃、零一万物、百川、SiliconFlow、Gemini(key)、AWS Bedrock、OpenRouter、
  **xAI Grok（`XAI_API_KEYS`，官方 key）**、Agnes。
- **自定义中转站**：`CUSTOM_PROVIDERS`（内联 JSON）/ `CUSTOM_PROVIDERS_FILE`，
  可复用第三方 CLI 的 token 文件（`keyFile`）。

> 说明：`grok-web`（订阅复用）与 `grok`（官方 API key）可并存——前者复用 grok.com
> 订阅，模型名带 `grok-web/` 前缀；后者用 x.ai 官方 key。

---

## 快速开始

### 方式一：源码运行（需 Go 1.26+）

```bash
git clone https://github.com/DevEloLin/apiforge.git
cd apiforge

# 最简：本机回环 + 一个访问密钥，自动探测已登录的 CLI
API_KEYS=sk-my-secret HOST=127.0.0.1 PORT=8899 go run ./cmd/apiforge
```

### 方式二：Docker（scratch 镜像，约 7MB）

```bash
docker build -t apiforge .
docker run --rm -p 127.0.0.1:8899:8899 \
  -e API_KEYS=sk-my-secret \
  -v $HOME/.codex:/root/.codex:ro \
  -v $HOME/.claude:/root/.claude:ro \
  apiforge
```

> 容器内默认 `HOST=0.0.0.0`，请只发布到 `127.0.0.1`（如上），前面再挂 new-api /
> Cloudflare Tunnel 做多用户与鉴权。

启动后验证：

```bash
curl -s http://127.0.0.1:8899/health | jq        # 就绪的 provider 列表
curl -s -H "Authorization: Bearer sk-my-secret" \
     http://127.0.0.1:8899/v1/models | jq         # 聚合模型列表
```

---

## 各来源怎么准备登录

apiforge **不做登录**，只复用你已经登录好的本机凭据。准备方式：

- **Codex**：`codex login`（或把 `auth.json` 拷到 `~/.codex/`）。多账户用
  `CODEX_AUTHS=/path/a/auth.json,/path/b/auth.json`。
- **Claude**：`claude`（Claude Code）登录一次，凭据在 `~/.claude/.credentials.json`。
- **Copilot**：在 VS Code 里登录 GitHub Copilot；apiforge 从
  `~/.config/github-copilot/{apps,hosts}.json` 发现 token，或用 `COPILOT_GITHUB_TOKENS`。
- **Qwen Code**：`qwen` 登录，凭据在 `~/.qwen/oauth_creds.json`。
- **Gemini CLI**：`gemini` 登录；设 `GEMINI_OAUTH_ENABLED=1` 启用（实验）。本仓库
  **不内置** Google OAuth client，需额外提供 `GEMINI_OAUTH_CLIENT_ID` 与
  `GEMINI_OAUTH_CLIENT_SECRET`（取自开源 gemini-cli 内置的公开 client）。
- **Grok（订阅）**：浏览器登录 grok.com，从开发者工具复制 `sso` cookie 值，设
  `GROK_COOKIES=<sso值>`。若被 Cloudflare 拦（403），改传完整 cookie 串（含
  `cf_clearance`）：`GROK_COOKIES="sso=xxx; cf_clearance=yyy"`。多账户逗号分隔。
- **Cursor（订阅）**：从桌面版 Cursor 的 `state.vscdb` 取出会话 token（无头机没有该
  文件，所以用环境变量传）：
  `sqlite3 state.vscdb "select value from ItemTable where key='cursorAuth/accessToken'"`，
  然后设 `CURSOR_ACCESS_TOKEN=<token>`。

> 凭据可以是 `user_<ULID>::<JWT>` 形式或裸 JWT，apiforge 会自动处理。

---

## 配置项（环境变量）

| 变量 | 默认 | 说明 |
|---|---|---|
| `API_KEYS` | 空 | 客户端访问密钥（逗号分隔）。为空且非回环绑定时**拒绝启动**（fail-closed） |
| `HOST` / `PORT` | `127.0.0.1` / `8899` | 监听地址 |
| `ADMIN_TOKEN` | 空 | 管理 API 令牌；为空则 `/admin/*` 全关 |
| `MAX_ACCOUNT_CONCURRENCY` | `3` | **每账户**并发上限；`0` = 不限 |
| `QUEUE_WAIT_MS` | `60000` | 账户全忙时请求排队等空位的最长时间（而非直接失败） |
| `STICKY_TTL_SECONDS` | `0` | 会话粘滞（`x-apiforge-session` 头映射同一账户）；`0` 关闭 |
| `RATE_LIMIT_RPM` | `0` | 每密钥每分钟请求上限；`0` 关闭 |
| `MAX_BODY_BYTES` | `10485760` | 请求体上限；`0` 不限 |
| `<PROVIDER>_AUTHS` / `_AUTH` | 自动探测 | 指定凭据文件路径（如 `CODEX_AUTHS`、`CLAUDE_AUTHS`） |
| `<PROVIDER>_ENABLED` | `true` | 关闭某来源（如 `CURSOR_ENABLED=false`） |
| `OPENAI_API_KEYS` / `ANTHROPIC_API_KEYS` | 空 | 官方 key（与 CLI 账户混池） |
| `<VENDOR>_API_KEYS` | 空 | 各厂商 key（`DEEPSEEK_API_KEYS`、`XAI_API_KEYS` …） |
| `GROK_COOKIES` | 空 | grok.com 订阅 cookie（多账户逗号分隔）🧪 |
| `CURSOR_ACCESS_TOKEN(S)` | 空 | Cursor 会话 token 🧪 |
| `GEMINI_OAUTH_ENABLED` | `false` | 开启 gemini-cli 🧪 |
| `GEMINI_OAUTH_CLIENT_ID` / `_SECRET` | 空 | gemini-cli 的公开 OAuth client（本仓库不内置，需自备）🧪 |
| `CUSTOM_PROVIDERS` / `_FILE` | 空 | 自定义中转站（内联 JSON / 文件） |
| `ALLOW_UNAUTHENTICATED` | `false` | 允许无密钥非回环启动（危险，仅调试） |

完整示例见 [.env.example](./.env.example)。

---

## 使用示例

Chat（自动按模型名路由到对应来源）：

```bash
curl http://127.0.0.1:8899/v1/chat/completions \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.4-mini","messages":[{"role":"user","content":"你好"}]}'
```

流式：加 `"stream": true`。Claude 原生协议：

```bash
curl http://127.0.0.1:8899/v1/messages \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-5","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}'
```

Codex 文生图：

```bash
curl http://127.0.0.1:8899/v1/images/generations \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"gpt-image-2","prompt":"a red bicycle","n":1,"size":"1024x1024"}'
```

Grok 订阅（注意 `grok-web/` 前缀）：

```bash
curl http://127.0.0.1:8899/v1/chat/completions \
  -H "Authorization: Bearer sk-my-secret" -H "Content-Type: application/json" \
  -d '{"model":"grok-web/grok-4.2","messages":[{"role":"user","content":"hi"}]}'
```

---

## 账户池 / 并发 / 排队

- **每账户并发帽**（`MAX_ACCOUNT_CONCURRENCY`，默认 3）保护订阅账户不被瞬时打爆、降低封号风险。
- **排队**：当某来源所有健康账户都打满并发帽时，新请求会**排队等待空位**（最多
  `QUEUE_WAIT_MS`），而不是立刻失败；账户释放槽位即被唤醒。
- **自动切换**：429 冷却 60s，鉴权失败冷却 5 分钟，期间自动切到其他账户。
- **流式**会一直占用槽位直到流结束（避免提前释放导致超发）。

举例：2 个账户 × 帽 3 = 同时 6 路在途；20 个用户并发时，6 个立即处理、其余排队轮流，
**以排队延迟换取零失败**（实测 10 并发 × 2 账户帽 1 → 10/10 成功）。

---

## 管理 API

设了 `ADMIN_TOKEN` 后可用（`Authorization: Bearer <ADMIN_TOKEN>`）：

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/admin/providers` | 列出就绪 provider 与模型 |
| GET | `/admin/accounts` | 各来源账户健康快照（在途数 / 冷却 / 禁用） |
| POST | `/admin/accounts/preferred` | 手动指定优先账户（`{"provider":"codex","account":"codex#1"}`；account 传空清除） |
| POST | `/admin/accounts/enabled` | 启用/禁用某账户（`{"provider":"codex","account":"codex#1","enabled":false}`） |

也可用请求头 `x-apiforge-account: codex#2` 对**单次请求**指定账户，`x-apiforge-session:
<id>` 做会话粘滞。

---

## 安全设计

- **fail-closed**：无 `API_KEYS` 且绑定非回环时拒绝启动（除非显式 `ALLOW_UNAUTHENTICATED=1`）。
- **SSRF 防护**：所有自定义 baseURL 拒绝回环 / 内网 / 链路本地地址。
- **凭据脱敏**：日志与 `/health` 诊断中屏蔽 token 与绝对路径。
- **出站拟真**：绝不转发入站头（客户端 UA/密钥），只发各 CLI 的真实指纹头。
- **keyFile 防穿越**：自定义 `keyFile` 必须在允许根目录内。
- **管理令牌常数时间比较**，防时序侧信道。

---

## 部署到树莓派

- 交叉编译静态二进制：`CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o apiforge ./cmd/apiforge`（约 6.8MB）。
- 或在派上 `docker build`（scratch 镜像，`GOMEMLIMIT=64MiB`）。
- 建议只监听回环，前置 new-api（多用户/计费）+ Cloudflare Tunnel。

---

## 常见问题 FAQ

**apiforge 是什么？**
apiforge 是一个用 Go 写的**自托管 AI API 网关 / 反向代理**，把你本机各家 AI CLI 与
订阅登录（ChatGPT/Codex、Claude、GitHub Copilot、Gemini、Qwen、Cursor、Grok）聚合成
**一套 OpenAI / Anthropic 兼容 API**，单文件二进制、无前端、无数据库，可跑在树莓派上。

**它和 one-api / new-api / sub2api 有什么区别？**
one-api / new-api 主要面向 API Key 的多租户分发与计费；sub2api 是较重的
Go+Vue+PostgreSQL+Redis 多租户平台。apiforge 更轻：单文件、无数据库、专注“**复用本机
订阅 / CLI 登录**并统一成标准 API”，适合个人自用或前置到 new-api 后面。

**支持哪些模型和订阅？**
见上文[支持哪些订阅 / 来源](#支持哪些订阅--来源)：订阅复用（Codex/Claude/Copilot/
Gemini/Qwen/Cursor/Grok）+ 20+ 家 OpenAI 兼容厂商 API Key（DeepSeek/Kimi/GLM/通义/
xAI Grok…）+ 自定义中转站。

**支持流式（stream）吗？** 支持。所有 chat 端点支持 SSE 流式与非流式聚合。

**会被封号吗？如何降低风险？**
有风险——复用订阅登录可能违反厂商服务条款（见文首声明）。可通过**每账户并发帽**
（`MAX_ACCOUNT_CONCURRENCY`）、少量账户、避免高频对外提供服务来降低风险；但无法消除。

**可以商用或转卖吗？** 不可以。本项目仅供个人研究，且**从未授权任何商业化运营**
（见[许可与署名](#许可与署名)与 [LICENSE](./LICENSE)）。

**能跑在树莓派 / 低内存机器上吗？** 可以。静态二进制约 6.8MB，scratch 镜像约 7MB，
`GOMEMLIMIT` 默认 64MiB，适合 1GB 树莓派。

**需要一直开着对应的 CLI 吗？** 不需要。apiforge 只读取登录凭据文件并用 HTTP 自动
刷新 OAuth token，不启动任何 CLI 子进程。

## 许可与署名

本项目采用 **apiforge 个人研究与署名许可（Personal Research & Attribution License）**，
详见 [LICENSE](./LICENSE)。核心条件：

1. **仅限个人、非商业的研究 / 学习 / 评估用途**；不得出售、转售或作为付费服务提供。
2. **必须保留来源署名，且不可移除、不可替换、不可篡改**：任何使用、拷贝、fork、修改、
   分发（源码或二进制）都必须原样保留 [LICENSE](./LICENSE) 与 [NOTICE](./NOTICE)，
   并清晰标注原始来源仓库地址与作者。**禁止冒名、去除署名或伪造出处。**
3. 你需自行承担因违反各 AI 厂商服务条款而产生的一切风险（见文首声明）。
4. 软件按“原样”提供，不含任何担保。

---

## 来源与致谢

- 逆向协议参考了社区公开研究（如 grok2api、codex-imagen 等第三方项目）用于**互操作性
  研究**目的。相关商标与服务归各自厂商所有；本项目与这些厂商**无任何隶属关系**。
- 原始来源仓库：**https://github.com/DevEloLin/apiforge** （转载 / fork 请保留此出处）。
