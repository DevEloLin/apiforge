[English](./PARITY.md) | **中文**

# 与 TypeScript 版 apiforge 的对照（Parity）

追踪 Go 重写版相对原始 `apiforge`（Node/TS/Hono）的功能覆盖。
目标：功能不缺失（仅去掉有意移除的前端）。

## 路由 —— 全平
| 路由 | TS | Go |
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
| `GET /`（浏览器控制台） | ✅ | ❌ **按要求移除**（无前端） |

## Provider —— 全平 + 新增
- `codex` —— chat / responses / images（文生图 + 图生图），OAuth + API key。
- `claude` —— chat（OpenAI↔Anthropic 翻译）+ 原生 messages / count_tokens，OAuth + API key。
- `copilot` —— GitHub→Copilot token 交换，实时模型发现。
- `qwen-cli` —— OAuth，按账户动态 base URL。
- `gemini-cli` —— 🧪 实验，需显式开启。
- `cursor` —— 🧪 实验，逆向 protobuf。
- **`grok-web`** —— 🧪 实验，grok.com 订阅复用（**新增，TS 版没有**）。
- openaicompat 厂商（20+，含 **xAI Grok API key**）· 自定义中转站。

## 特性 —— 对齐 + 增强
- 账户池：轮询 / 故障转移 + 冷却（429 / 鉴权失败）。✅
- 通过 `/admin` 手动控制账户（指定 / 启用 / 禁用）。✅
- CLI 登录 + API key 混池。✅
- 出站指纹拟真（真实 CLI UA / 头；不转发入站头）。✅
- baseURL 的 SSRF 防护 · fail-closed 启动 · body 上限 · 限流 · 密钥/路径脱敏 · keyFile 路径防护。✅
- 管理 token 常数时间比较。✅
- **新增（借鉴 sub2api）：** 每账户并发帽 + 会话粘滞（`x-apiforge-session`）。
- **新增：** 请求**排队** —— 账户全部打满并发帽时，请求等待空位（`QUEUE_WAIT_MS`）而非立即失败。

## 有意的差异
- **无浏览器控制台**（按要求 —— 精简、无头）。
- **Cursor token 走 `CURSOR_ACCESS_TOKEN(S)`**，不读取 `state.vscdb`（不引入 SQLite 引擎依赖；
  无头主机本就没有 Cursor DB —— 保持 scratch 镜像约 7MB）。
- **不内置 Gemini OAuth client** —— 通过 `GEMINI_OAUTH_CLIENT_ID` / `_SECRET` 环境变量提供。
- `gemini-cli` / `cursor` / `grok-web` 为可选 / 实验性。

## 验证状态
- **真机 E2E 通过：** `codex`（非流/流式 chat、文生图）、`copilot`（48 模型、chat）、admin API。
- **格式/单测已验证，实时待重登复测：** `claude`（刷新请求格式已由 Anthropic 语义化限流响应证明正确）。
- **仅代码 + 单测（本机无登录）：** `qwen`、`gemini`、`cursor`、`grok-web`。
- 约 52 个单测覆盖翻译 / protobuf / 排队层。
