# Contributing / 贡献指南

**English** below · [中文见下](#中文)

## English

Thanks for your interest! First, please read the **[README notice](./README.md#-important-notice--read-first)**
and the **[LICENSE](./LICENSE)** — this is a **personal-research** project, and any contribution
must respect the non-removable attribution terms (LICENSE §2).

### Dev setup
```bash
go build ./...     # build
go vet ./...       # static checks
go test ./...      # unit tests
gofmt -l .         # must print nothing (run `gofmt -w .` to fix)
```
Requires Go 1.26+ (see `go.mod`). The project uses **only** the standard library plus
`golang.org/x/sync` — please do not add heavyweight dependencies without discussion.

### Guidelines
- Match the existing style: small focused files, immutable data, early returns, clear names.
- Keep the binary lean (it targets a 1 GB Raspberry Pi / ~7 MB scratch image).
- New provider? See **[ARCHITECTURE.md § Adding a provider](./docs/ARCHITECTURE.md#adding-a-provider)**
  and add unit tests for the translation layer.
- Commit messages: Conventional Commits (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`).
- Do **not** commit real credentials, tokens, or vendor client secrets.
- Open an issue first for anything non-trivial so we can align on scope.

### Pull requests
1. Fork, branch from `main`.
2. Ensure `go build`, `go vet`, `go test`, and `gofmt` are clean.
3. Keep attribution/NOTICE intact.
4. Describe what changed and how you tested it.

---

## 中文

感谢关注！开始前请先阅读 **[README 重要提醒](./README.zh-CN.md#-重要提醒务必先读)** 与
**[LICENSE](./LICENSE)** —— 本项目**仅供个人研究**，任何贡献都须遵守“来源署名不可移除”条款
（LICENSE §2）。

### 开发环境
```bash
go build ./...     # 构建
go vet ./...       # 静态检查
go test ./...      # 单元测试
gofmt -l .         # 应无输出（用 `gofmt -w .` 修复）
```
需要 Go 1.26+（见 `go.mod`）。项目**只**用标准库 + `golang.org/x/sync`，请勿在未讨论的情况下
引入重依赖。

### 约定
- 与现有风格一致：小而聚焦的文件、不可变数据、早返回、清晰命名。
- 保持二进制精简（目标是 1GB 树莓派 / 约 7MB scratch 镜像）。
- 新增 provider？见 **[ARCHITECTURE.md 新增 provider](./docs/ARCHITECTURE.zh-CN.md#新增-provider)**，
  并为翻译层补单元测试。
- 提交信息用 Conventional Commits（`feat:`/`fix:`/`docs:`/`refactor:`/`test:`/`chore:`）。
- **切勿**提交真实凭据、token 或厂商 client secret。
- 非小改动请先开 issue 对齐范围。

### Pull Request
1. Fork，从 `main` 切分支。
2. 确保 `go build`、`go vet`、`go test`、`gofmt` 全部干净。
3. 保留署名 / NOTICE。
4. 说明改了什么、怎么测的。
