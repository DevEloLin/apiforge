[English](./ARCHITECTURE.md) | **中文**

# apiforge 架构

面向贡献者的设计概览。终端用户文档请参阅[使用指南](./USAGE.zh-CN.md)与[运维指南](./OPERATIONS.zh-CN.md)。

## 设计目标
- **精简：** 仅依赖纯标准库 + `golang.org/x/sync`；单个静态二进制文件；通过流式处理
  （`io.Copy` / `io.Pipe`）保持内存平坦，代理响应不做整体缓冲。
- **忠实：** 与 TS 原版保持功能对等（参见[功能对等](./PARITY.zh-CN.md)）。
- **默认安全：** 失败即关闭（fail-closed）、防 SSRF、脱敏机密、最小权限。

## 包布局

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

## Provider 契约

每个上游都实现 `types.Provider`：

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

OpenAI Chat Completions 是通用语言。额外的接口面是**可选启用的能力接口**，
在路由中通过类型断言检查：

- `ResponsesProvider` → `/v1/responses` (codex)
- `ImagesProvider` → `/v1/images/*` (codex)
- `AnthropicProvider` → `/v1/messages`, `/v1/messages/count_tokens` (claude)
- `Pooled` → exposes `pool.Admin` for the `/admin` account API

Provider 方法返回上游的 `*http.Response`（真实响应，或对翻译型 provider 而言通过 `io.Pipe`
合成的响应）。服务器用 `io.Copy` 将其流式回传，从而保持内存平坦。

## 请求流程

```
client → server (auth · rate-limit · body-limit middleware)
       → extract model → registry.FindByModel → capability check
       → provider.ChatCompletion(rctx, body)
            → relay.WithAccountRetry (pool candidates · acquire · queue)
                 → provider fn: build upstream request, translate if needed
       → server writeUpstream (copy status + headers + io.Copy body, flush for SSE)
```

## 账户池（`internal/pool`）

泛型 `Pool[C]`（C = 凭据类型）。每账户维护 `state{disabledUntil, failures,
manualDisabled, inflight}`。关键行为：

- **排序**（`Candidates`）：健康账户优先，偏好有空闲并发槽位的账户，轮询轮转，
  然后将 sticky / pinned / preferred 账户移到前面；若所有账户都在冷却，则返回最快恢复的那个，
  以便请求仍能尝试。
- **并发上限：** `Acquire` 预留一个槽位（若已达上限则失败）；`Release` 释放槽位并在 `freed`
  channel 上**广播**，唤醒排队中的请求。
- **冷却：** `MarkRateLimited`（429，默认 60 s）/ `MarkAuthFailed`（401/403，5 min）。
- **Sticky 会话：** `Bind(session, id)` 将会话键 → 账户映射，持续 `StickyTTL`。
- **Admin：** 通过类型擦除的 `pool.Admin` 接口执行 `SetPreferred` / `SetEnabled` / `Status`。

## Token 生命周期（`internal/token`）

`Manager` 封装一个 `Source`（各 provider 的凭据逻辑）：

```go
type Source interface {
    Read(ctx) error            // load from disk
    Token() string             // current access token
    Fresh() bool               // still valid (with skew)?
    Refresh(ctx) (string, error) // HTTP refresh, persist rotation
}
```

`AccessToken` = 一次性加载，之后若 `Fresh()` 则直接返回；否则执行一次**single-flight** 刷新，
该刷新会**先从磁盘重新读取**（CLI 可能已经轮换过 token）再进行刷新。
每个 provider 的 `creds` 将认证状态保存在 `atomic.Pointer` 中，因此 `Token()`/`Fresh()` 与并发的
`Refresh()` 之间无数据竞争。轮换后的 token 会被原子地写回（临时文件 + rename，0600），
使 CLI 与网关保持同步。

## Relay 与账户重试（`internal/relay`）

`WithAccountRetry[C]` 驱动账户池：

1. 遍历 `Candidates`；对每个执行 `Acquire`（若已达上限则跳过——并**计为可排队**）。
2. 运行 provider fn。命中 **2xx** 时：`MarkOk` + `Bind`，并用 `releaseCloser` 包装响应体，
   使并发槽位持续占用直到响应体关闭（流式传输计入上限）。
3. 命中 **429 / 401 / 403 / 5xx** 时：冷却该账户并尝试下一个。
4. 不可重试的客户端错误（400/404/422）：原样返回上游响应体。
5. 若所有健康账户都**繁忙**（而非失败），则**排队**：在 `SlotFreed()`（每轮重新获取）上等待，
   最长 `QUEUE_WAIT_MS`，然后重试；若客户端断开连接则返回 `ctx.Err()`。
6. 全部耗尽 → 合成 `503`。

**响应辅助函数**（供翻译型 provider 共用）：`JSONResponse`（聚合 → JSON 响应体）、
`StreamingResponse`（一个 `io.Pipe`；由 goroutine 读取上游流、写入翻译后的 SSE 帧并关闭上游）、
`SynthStatus`（用于引导分类器的小型错误响应——例如在 token 刷新失败时合成一个 401）。

## 翻译层

翻译型 provider 在 OpenAI ⇄ 厂商原生协议之间进行双向转换，包括**流式**：

- **codex** — OpenAI Chat ⇄ Codex Responses SSE；用 image_generation 工具生图。
- **claude** — OpenAI Chat ⇄ Anthropic Messages；原生 `/v1/messages` 透传；在 OAuth 模式下注入
  Claude Code 身份 system 块并剥离客户端指纹字段。
- **gemini** — OpenAI Chat ⇄ Gemini `generateContent`（Code Assist 信封）。
- **cursor** — 手写 protobuf + Connect-RPC 帧封装（gzip）+ Jyh 校验和加密。
- **grokweb** — grok.com NDJSON 流（`result.token` 增量）+ 伪造的 `x-statsig-id`。
- **openaicompat** — 不做翻译；转发到任意 OpenAI 兼容端点（厂商 + 自定义）。

Wire 辅助工具：`util/sse`（SSE 帧迭代器 + NDJSON 行迭代器 + 帧写入器）、
`util/idgen`（chatcmpl / resp / call id + UUID）。每个 provider 保留自己的请求结构体——
包与包之间不共享 wire 类型。

## 安全层
- **Fail-closed** 启动（`main.go`）：在非环回（loopback）绑定上拒绝无认证运行。
- **SSRF**（`util/ssrf`）：拒绝 loopback / private / link-local 的 base URL。
- **脱敏**（`util/sanitize`）：在日志 / `/health` 中掩盖机密与绝对路径。
- **出站卫生：** 绝不转发入站请求头；仅发送真实的 CLI 指纹请求头。
- **keyFile 守卫：** 自定义 `keyFile` 必须位于允许的根目录之下。
- **常量时间**的 admin-token 比较（`crypto/subtle`）。

## 新增一个 provider
1. 在 `internal/provider/<name>/` 下新建包。
2. 实现 `types.Provider`（及任意能力接口），底层由 `pool.Pool[C]` 支撑；对于 OAuth 源，
   还需 `token.Manager`。
3. 对翻译型 provider，返回 `relay.StreamingResponse` / `relay.JSONResponse`，并用
   `relay.WithAccountRetry` 驱动账户池。
4. 在 `internal/provider/register.go` 中注册（按 env / 凭据是否存在进行门控）。
5. 为翻译层添加单元测试（参见现有的 `*_test.go`）。
