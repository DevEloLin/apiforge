[English](./OPERATIONS.md) | **中文**

# apiforge 操作手册（安装 · 配置 · Docker 打包与部署 · 运维）

> 面向部署与运维。客户端调用方式见 [USAGE.md](./USAGE.zh-CN.md)。
> 使用前请阅读项目根 [README](../README.zh-CN.md) 的免责声明与 [LICENSE](../LICENSE)。

## 目录
1. [环境要求](#1-环境要求)
2. [三种运行方式](#2-三种运行方式)
3. [准备各来源登录凭据](#3-准备各来源登录凭据)
4. [完整配置项参考](#4-完整配置项参考)
5. [打包 Docker 镜像](#5-打包-docker-镜像)
6. [Docker 运行与配置](#6-docker-运行与配置)
7. [docker-compose](#7-docker-compose)
8. [裸二进制 + systemd](#8-裸二进制--systemd)
9. [树莓派部署](#9-树莓派部署)
10. [前置 new-api / Cloudflare Tunnel](#10-前置-new-api--cloudflare-tunnel)
11. [健康检查与监控](#11-健康检查与监控)
12. [升级与回滚](#12-升级与回滚)
13. [故障排查](#13-故障排查)

---

## 1. 环境要求

- **源码编译 / 交叉编译**：Go 1.26+。
- **Docker 方式**：Docker 20+（arm64 目标可用 buildx）。
- **凭据**：本机已登录相应 AI CLI（或手动准备凭据文件 / cookie / token）。见 §3。
- 网络：能出网访问各厂商 API 域名（api.openai.com、api.anthropic.com、chatgpt.com、
  githubcopilot.com、grok.com 等）。

---

## 2. 三种运行方式

| 方式 | 适用 | 内存 | 备注 |
|---|---|---|---|
| 源码 `go run` | 开发调试 | 中 | 最快验证 |
| 裸二进制 + systemd | 生产/树莓派 | **最低** | 无容器开销，见 §8 |
| Docker（scratch 镜像） | 想要容器隔离 | 低（镜像 ~7MB） | 见 §5–§7 |

最简启动（源码）：

```bash
git clone https://github.com/DevEloLin/apiforge.git && cd apiforge
API_KEYS=sk-my-secret HOST=127.0.0.1 PORT=8899 go run ./cmd/apiforge
```

看到 `apiforge listening ... ready=[...]` 即成功；`ready` 列表是探测到并初始化成功的来源。

### 直接用编译好的二进制（单文件）
单个自包含二进制，无运行时依赖。先构建（或放入 release 二进制），然后三选一方式运行：

```bash
# 1) 构建静态二进制（任意装了 Go 的机器，可交叉编译）：
deploy/build.sh linux/arm64            # → dist/apiforge-linux-arm64
#   或直接：  go build -o apiforge ./cmd/apiforge

# 2) 运行 —— 配置方式三选一：
API_KEYS=sk-my-secret ./apiforge                       # a) 环境变量（本机回环调试）
./apiforge -env-file ./apiforge.env                    # b) 配置文件
./apiforge -config-dir /etc/apiforge                   # c) 配置目录（apiforge.env + conf.d/*.env）
./apiforge                                             # d) 不带参数 → 自动发现 /etc/apiforge、~/.config/apiforge、~/.apiforge、./

# 3) 验证：
curl -s http://127.0.0.1:8899/health
```

标志：`-env-file <文件>`、`-config-dir <目录>`（也可用 `APIFORGE_ENV_FILE` / `APIFORGE_CONFIG_DIR`）。
服务器/树莓派上要开机自启与重启，请用 **systemd**（§8）跑，别手动挂后台。
配置项与文件格式见下（§4 及下一小节）。

**仓库已提供现成的部署产物：**

| 产物 | 路径 | 用途 |
|---|---|---|
| 配置文件模板 | [`deploy/apiforge.env.example`](../deploy/apiforge.env.example) | 二进制 / systemd 配置 |
| systemd unit | [`deploy/apiforge.service`](../deploy/apiforge.service) | 作为服务运行 |
| 多架构构建脚本 | [`deploy/build.sh`](../deploy/build.sh) | 生成静态二进制 |
| systemd 安装脚本 | [`deploy/install.sh`](../deploy/install.sh) | 一键安装 |
| Dockerfile | [`Dockerfile`](../Dockerfile) | 构建镜像 |
| docker-compose | [`docker-compose.yml`](../docker-compose.yml) | `docker compose up -d` |

### 配置文件与目录（二进制用）
二进制走环境变量配置，也可读**配置文件/目录**（nginx/haproxy/wireguard 风格），无需 systemd 或 Docker。

**标准配置目录** —— 自动发现（第一个存在的生效）：

```
/etc/apiforge/
  apiforge.env        # 主配置（KEY=VALUE）；chmod 0600 —— 含密钥
  conf.d/*.env        # 拖入覆盖，在主配置之后按文件名排序加载
  creds/              # 把 CLI 登录/授权文件拷到这里（chmod 0700）
```

发现顺序：`$APIFORGE_CONFIG_DIR` → `/etc/apiforge` → `~/.config/apiforge` → `~/.apiforge`
→ `./apiforge.env`（或 `./.apiforge.env`）。所以装到 `/etc/apiforge` 后，直接跑裸 `apiforge`（无参数）即可。指定位置：

```bash
apiforge                                    # 自动发现
apiforge -config-dir /etc/apiforge          # 目录（apiforge.env + conf.d/*.env）
apiforge -env-file /path/apiforge.env       # 单文件
APIFORGE_CONFIG_DIR=/etc/apiforge apiforge  # 等价环境变量
APIFORGE_ENV_FILE=/path/apiforge.env apiforge
```

**优先级：** 真实环境变量 > 后加载文件（drop-in）> 先加载文件。即 `docker -e` / systemd
`Environment=` / shell export 永远压过文件，`conf.d/` 压过 `apiforge.env`。

**凭据/授权文件：** 把拷来的 CLI 登录文件放 `creds/`（像 wireguard 把密钥放 `/etc/wireguard`），
用 `*_AUTHS` 指过去，如 `CODEX_AUTHS=/etc/apiforge/creds/codex/auth.json`。因为 `creds/` 在
`/etc/apiforge` 内（已在 unit 的 `ReadWritePaths`），OAuth token 回写默认即可用。

格式（键即 §4 变量）：
- 每行一个 `KEY=VALUE`；空行与以 `#` 开头的行忽略；
- 可选前缀 `export `；
- **行内注释**：未加引号的值里，前面有空格的 `#` 起注释（`PORT=8899   # 端口` → `8899`）；
- 值里要保留空格或字面 `#`，请**加引号**（`X="a#b c"` → `a#b c`）；
- 真实环境变量（shell / `docker -e` / systemd `Environment=`）**优先级高于**文件值。

模板见 [`deploy/apiforge.env.example`](../deploy/apiforge.env.example)。

---

## 3. 准备各来源登录凭据

apiforge **不做登录**，只读取你本机已登录的凭据文件，并在需要时用 HTTP 自动刷新 token。

| 来源 | 凭据位置（自动探测） | 手动指定环境变量 | 备注 |
|---|---|---|---|
| codex | `~/.codex/auth.json` | `CODEX_AUTHS`（逗号分隔多账户） | `codex login` 产生 |
| claude | `~/.claude/.credentials.json` | `CLAUDE_AUTHS` | `claude` 登录产生 |
| copilot | `~/.config/github-copilot/`（目录） | `COPILOT_GITHUB_TOKENS` | 从 apps.json/hosts.json 发现 |
| qwen | `~/.qwen/oauth_creds.json` | `QWEN_AUTHS` | `qwen` 登录产生 |
| gemini 🧪 | `~/.gemini/oauth_creds.json` | `GEMINI_AUTHS` + 需 `GEMINI_OAUTH_ENABLED=1` 且自备 `GEMINI_OAUTH_CLIENT_ID/SECRET` | 实验 |
| grok-web 🧪 | 无（用环境变量） | `GROK_COOKIES`（sso 或完整 cookie 串） | 浏览器复制 sso |
| cursor 🧪 | 无（无头机没有 state.vscdb） | `CURSOR_ACCESS_TOKEN(S)` | 从桌面 Cursor 导出 |

**多账户** —— 两种方式,都在配置文件里完成:
1. **逗号分隔列表**(任意来源):`CODEX_AUTHS=/a/auth.json,/b/auth.json`、
   `ANTHROPIC_API_KEYS=k1,k2`、`DEEPSEEK_API_KEYS=k1,k2`、`GROK_COOKIES=cookie1,cookie2`、
   `CURSOR_ACCESS_TOKENS=t1,t2`、`COPILOT_GITHUB_TOKENS=t1,t2`。
2. **账户目录**(文件型来源 codex/claude/qwen/gemini —— 比长逗号行清爽):把 `*_AUTHS` 指向一个
   **目录**,每个账户放一个 `*.json`:
   ```
   /etc/apiforge/creds/codex/acct1.json
   /etc/apiforge/creds/codex/acct2.json     # → codex#1, codex#2
   ```
   `CODEX_AUTHS=/etc/apiforge/creds/codex`(或直接放进去 —— `creds/<provider>/` 会被自动发现)。
   目录内非 `.json` 文件忽略。

> 每个凭据成为池中一个账户(轮询 + 故障转移 + 每账户并发帽)。
> 自动发现会汇集**所有**存在的凭据来源(如 `creds/codex/` 目录 *与* `~/.codex/auth.json` 并存);
> 要精确集合请显式设 `*_AUTHS`。原多账户逗号写法仍等价支持。
每个凭据成为账户池里的一个账户，自动轮询 + 失败切换。

**API Key 直连**（与 CLI 账户混入同一 provider 池）：`OPENAI_API_KEYS`、`ANTHROPIC_API_KEYS`、
各厂商 `<VENDOR>_API_KEYS`（见 §4）。

Grok / Cursor token 获取：

```bash
# Grok：浏览器登录 grok.com → 开发者工具 → Application → Cookies → 复制 sso 值
export GROK_COOKIES='sso=eyJ...'            # 若被 Cloudflare 403，用完整串：
export GROK_COOKIES='sso=eyJ...; cf_clearance=xxxx'

# Cursor：从桌面版 Cursor 的 state.vscdb 导出会话 token
sqlite3 "$HOME/Library/Application Support/Cursor/User/globalStorage/state.vscdb" \
  "select value from ItemTable where key='cursorAuth/accessToken'"
export CURSOR_ACCESS_TOKEN='eyJ...'
```

---

## 4. 完整配置项参考

所有配置来自**环境变量**（同一镜像可在 `docker run` 时改配置，无需重建）。

### 核心
| 变量 | 默认 | 说明 |
|---|---|---|
| `API_KEYS` | 空 | 客户端访问密钥，逗号分隔。空且非回环绑定 → **拒绝启动** |
| `HOST` | `127.0.0.1` | 监听地址。容器内需 `0.0.0.0` |
| `PORT` | `8899` | 监听端口 |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `ADMIN_TOKEN` | 空 | 管理 API 令牌；空则 `/admin/*` 关闭 |
| `ALLOW_UNAUTHENTICATED` | `false` | 允许无密钥 + 非回环启动（危险，仅调试） |

### 池 / 并发 / 限流
| 变量 | 默认 | 说明 |
|---|---|---|
| `MAX_ACCOUNT_CONCURRENCY` | `3` | 每账户并发上限；`0`=不限 |
| `QUEUE_WAIT_MS` | `60000` | 账户全忙时排队等空位的最长毫秒数 |
| `STICKY_TTL_SECONDS` | `0` | 会话粘滞 TTL（`x-apiforge-session`）；`0`=关 |
| `RATE_LIMIT_RPM` | `0` | 每密钥每分钟请求上限；`0`=关 |
| `MAX_BODY_BYTES` | `10485760` | 请求体上限；`0`=不限 |
| `UPSTREAM_TIMEOUT_MS` | `600000` | 上游超时（预留） |
| `GOMEMLIMIT` | 镜像内 `64MiB` | Go 堆软上限（低内存机建议设置） |

### 各来源开关 / 凭据
| 变量 | 默认 | 说明 |
|---|---|---|
| `<P>_ENABLED` | `true` | 关闭某来源，如 `CURSOR_ENABLED=false`（P=CODEX/CLAUDE/COPILOT/CURSOR/QWEN/GEMINI） |
| `<P>_AUTHS` / `<P>_AUTH` | 自动探测 | 凭据文件路径（多个逗号分隔） |
| `CODEX_MODELS` / `CLAUDE_MODELS` / `GEMINI_CLI_MODELS` | 内置 | 覆盖广告的模型列表 |
| `CODEX_CLIENT_VERSION` | `0.142.5` | Codex 后端版本头；模型被拒时升级此值 |
| `CODEX_USER_AGENT` / `CLAUDE_USER_AGENT` / `GEMINI_USER_AGENT` | 内置 | 出站 UA 覆盖 |
| `OPENAI_BASE_URL` / `ANTHROPIC_BASE_URL` | 官方 | 覆盖 codex(key)/claude 的 base |
| `OPENAI_API_KEYS` / `ANTHROPIC_API_KEYS` | 空 | 官方 key，与 CLI 账户混池 |
| `GROK_COOKIES` 🧪 | 空 | grok.com 订阅 cookie（多账户逗号分隔） |
| `CURSOR_ACCESS_TOKEN(S)` 🧪 | 空 | Cursor 会话 token |
| `GEMINI_OAUTH_ENABLED` 🧪 | `false` | 开启 gemini-cli |
| `GEMINI_OAUTH_CLIENT_ID` / `_SECRET` 🧪 | 空 | gemini-cli 公开 OAuth client（本仓库不内置，需自备） |
| `COPILOT_GITHUB_TOKENS` | 空 | 额外 GitHub token（逗号分隔） |

### 厂商 API Key（供 key 即启用；20+ 家）
`DEEPSEEK_API_KEYS`、`MOONSHOT_API_KEYS`、`ZHIPU_API_KEYS`、`QWEN_API_KEYS`、`BAIDU_API_KEYS`、
`SENSETIME_API_KEYS`、`SKYWORK_API_KEYS`、`AI360_API_KEYS`、`MINIMAX_API_KEYS`、`DOUBAO_API_KEYS`、
`HUNYUAN_API_KEYS`、`SPARK_API_KEYS`、`STEPFUN_API_KEYS`、`YI_API_KEYS`、`BAICHUAN_API_KEYS`、
`SILICONFLOW_API_KEYS`、`GEMINI_API_KEYS`、`AWS_BEDROCK_API_KEYS`（+`AWS_BEDROCK_BASE_URL`）、
`AGNES_API_KEYS`、`OPENROUTER_API_KEYS`、`XAI_API_KEYS`（官方 Grok，+`XAI_BASE_URL`、`GROK_MODELS`）。
每家还支持 `<VENDOR>_MODELS` 覆盖模型列表。

### 自定义中转站
| 变量 | 说明 |
|---|---|
| `CUSTOM_PROVIDERS` | 内联 JSON 数组，见下 |
| `CUSTOM_PROVIDERS_FILE` | JSON 文件路径 |
| `CREDS_ROOT` | keyFile 允许的根目录（默认 HOME） |
| `ALLOW_ANY_KEYFILE` | `1` 关闭 keyFile 路径穿越检查 |

```json
[{"id":"myrelay","baseUrl":"https://api.example.com","models":["gpt-4o"],
  "apiKeys":["sk-xxx"],"ownedBy":"me","authHeader":"authorization",
  "headers":{"x-foo":"bar"}}]
```
`apiKeys` 也可用 `keysEnv`（从某环境变量读）或 `keyFile`（读文件，复用第三方 CLI token）。

完整样例见 [.env.example](../.env.example)。

---

## 5. 打包 Docker 镜像

仓库自带多阶段 [Dockerfile](../Dockerfile)：`golang:1.26-alpine` 编译 → `scratch` 运行，
`CGO_ENABLED=0` 静态二进制 + CA 证书，最终镜像 **≈7MB**，非 root uid 运行。

### 5.1 本机同架构构建
```bash
cd apiforge
docker build -t apiforge:latest .
docker images apiforge     # 查看大小
```

### 5.2 交叉构建 arm64（给树莓派）
```bash
# 需 buildx（Docker Desktop 自带）
docker buildx build --platform linux/arm64 -t apiforge:arm64 --load .
```

### 5.3 无 Docker 的机器：交叉编译二进制 → 在目标机装进镜像
Mac/无 Docker 时先出二进制，再到有 Docker 的目标机构建极简镜像：
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
> 提示：`--from=alpine` 只为取 CA 证书。也可预先把 `ca-certificates.crt` 放到构建目录直接 COPY。

---

## 6. Docker 运行与配置

镜像内默认 `HOST=0.0.0.0`、`PORT=8899`、`GOMEMLIMIT=64MiB`、非 root uid `65532`。
**务必只把端口发布到 `127.0.0.1`**，对外由 §10 的反代做鉴权/多用户。

### 6.1 关键：凭据如何进容器
`scratch` 镜像**没有 `/root` 家目录、没有 `/etc/passwd`**，所以自动探测 `~/.codex` 之类
**在容器里不生效**。两种正确做法（任选）：

> **⚠️ uid 很关键。** 镜像以 uid `65532` 运行，而宿主机凭据文件通常是属主为你登录 uid 的 `0600`。
> bind mount 按 uid 号判权限，所以容器**读不到（也写不回）**这些文件，除非**以凭据属主的 uid 运行**：
> 加 `--user "$(id -u):$(id -g)"`（见下）。读凭据和 OAuth token 回写都需要这一步。

**A. 显式路径（推荐）** —— 挂载凭据目录并用 `*_AUTHS` 指到挂载点：
```bash
docker run -d --name apiforge \
  -p 127.0.0.1:8899:8899 \
  --user "$(id -u):$(id -g)" \
  -e API_KEYS=sk-my-secret \
  -e CODEX_AUTHS=/creds/codex/auth.json \
  -e CLAUDE_AUTHS=/creds/claude/.credentials.json \
  -v "$HOME/.codex:/creds/codex" \
  -v "$HOME/.claude:/creds/claude" \
  apiforge:latest
```

**B. 设 HOME** —— 让自动探测生效：
```bash
docker run -d --name apiforge \
  -p 127.0.0.1:8899:8899 \
  -e API_KEYS=sk-my-secret -e HOME=/creds \
  -v "$HOME/.codex:/creds/.codex" \
  -v "$HOME/.claude:/creds/.claude" \
  apiforge:latest
```

> **读写权限**：OAuth 刷新后 apiforge 会把新 token **原子写回**凭据文件，以便与 CLI 保持
> 同步。若用 `:ro` 只读挂载，刷新仍在内存生效但无法落盘（日志出现 warn，token 过期后需
> 重新提供）。想持久化就用**可写**挂载（去掉 `:ro`），并以凭据属主的 uid 运行
> （`--user "$(id -u):$(id -g)"`，同上），容器才能写该目录。

### 6.2 纯环境变量来源（无需挂载）
grok-web / cursor / 各厂商 key 只用环境变量，无需挂卷：
```bash
docker run -d --name apiforge -p 127.0.0.1:8899:8899 \
  -e API_KEYS=sk-my-secret \
  -e DEEPSEEK_API_KEYS=sk-xxx \
  -e XAI_API_KEYS=xai-xxx \
  -e GROK_COOKIES='sso=eyJ...' \
  apiforge:latest
```

### 6.3 用 env 文件
```bash
cp .env.example my.env && vim my.env      # 填好后（注意去掉注释里的示例）
docker run -d --name apiforge -p 127.0.0.1:8899:8899 --env-file my.env \
  -v "$HOME/.codex:/creds/codex" -e CODEX_AUTHS=/creds/codex/auth.json \
  apiforge:latest
```

### 6.4 调优
```bash
-e MAX_ACCOUNT_CONCURRENCY=3 -e QUEUE_WAIT_MS=60000 \
-e GOMEMLIMIT=48MiB --memory=128m           # 低内存机限制容器内存
```

---

## 7. docker-compose

仓库根目录已提供现成的 [`docker-compose.yml`](../docker-compose.yml) —— 改好其 `environment:`
（填 `API_KEYS`、凭据路径）与 `volumes:`（凭据目录**可写**挂载、不要 `:ro`，以便刷新后的 token 落盘），然后：

```bash
docker compose up -d --build && docker compose logs -f
```

---

## 8. 裸二进制 + systemd（最省内存，推荐树莓派）

配置文件、unit、脚本仓库都已提供，无需手写。

**一键安装（在目标机上）：** 装二进制、生成默认 `/etc/apiforge/apiforge.env`、装 unit,并**自动
`systemctl enable`**(开机自启 + 纳入 systemctl 管理)。默认配置已能在 `127.0.0.1` 起;填好自己的
key/凭据后启动即可。
```bash
# 1. 拿到静态二进制（在任意装了 Go 的机器上构建，再 scp 到目标机）：
deploy/build.sh linux/arm64            # → dist/apiforge-linux-arm64

# 2. 目标机上以 root 运行——装 + enable 服务（加 --now 可顺带立即启动）：
sudo deploy/install.sh dist/apiforge-linux-arm64 devops        # 第 2 个参数=服务用户
#   sudo deploy/install.sh dist/apiforge-linux-arm64 devops --now

# 3. 填好 key/凭据后启动：
sudo $EDITOR /etc/apiforge/apiforge.env   # 填 API_KEYS + *_AUTHS 路径
sudo systemctl start apiforge
journalctl -u apiforge -f
```

**手动安装（等价）：**
```bash
sudo install -m0755 apiforge /usr/local/bin/apiforge
sudo install -d /etc/apiforge && sudo install -m0600 deploy/apiforge.env.example /etc/apiforge/apiforge.env
sudo install -m0644 deploy/apiforge.service /etc/systemd/system/apiforge.service
sudo $EDITOR /etc/apiforge/apiforge.env                    # 配置
sudo $EDITOR /etc/systemd/system/apiforge.service          # 设 User= + 取消注释 ReadWritePaths
sudo systemctl daemon-reload && sudo systemctl enable --now apiforge
```

配置在 `/etc/apiforge/apiforge.env`（unit 执行裸 `apiforge`，自动发现 `/etc/apiforge`；键即 §4 变量）。
[`deploy/apiforge.service`](../deploy/apiforge.service) 里有两点要改对：

- **`User=`** 必须是你复用其 CLI 登录的账户（否则读不到凭据文件）。
- **`ReadWritePaths=`** 必须包含凭据目录（如 `/home/<用户>/.codex`），否则刷新后的 OAuth token 无法写回。

像普通服务一样管理：
```bash
sudo systemctl start   apiforge      # 启动（默认配置开箱即可在 127.0.0.1 起）
sudo systemctl status  apiforge      # 是否在跑
sudo systemctl restart apiforge      # 改完配置 / 换二进制后
sudo systemctl stop    apiforge
sudo systemctl enable  apiforge      # 开机自启（install.sh 已做）
journalctl -u apiforge -f            # 跟随日志
```

---

## 9. 树莓派部署

1. 交叉编译（§5.3）或在派上 `docker build`（1GB 派编译 Go 偏吃紧，优先交叉编译传二进制）。
2. 优先 **裸二进制 + systemd**（§8），内存最省；或极简镜像（§5.3 + §6）。
3. 凭据：把桌面机登录好的 `auth.json` / `.credentials.json` `scp` 到派上，用 `*_AUTHS` 指定；
   grok/cursor 用环境变量传 token。
4. 建议 `GOMEMLIMIT=48–64MiB`、`MAX_ACCOUNT_CONCURRENCY` 视账户数而定。
5. 供电务必用合规 5V/3A 电源（欠压会限频导致偶发超时）。

---

## 10. 前置 new-api / Cloudflare Tunnel

apiforge 只做“把订阅变标准 API”，**多用户 / 计费 / 公网入口**交给前置层：

```
公网用户 → Cloudflare Tunnel → new-api(多用户+计费) → apiforge(127.0.0.1:8899) → 各厂商
```

- apiforge 只监听 `127.0.0.1`，设强 `API_KEYS`（给 new-api 用）。
- new-api 里把 apiforge 配成一个 OpenAI 渠道，base 填 `http://127.0.0.1:8899/v1`，
  密钥填 apiforge 的 `API_KEYS`。
- Cloudflare Tunnel 指向 new-api，不直接暴露 apiforge。

> ⚠️ 对外提供服务会显著放大账号封禁风险（见免责声明）。仅建议个人/小范围研究。

---

## 11. 健康检查与监控

- `GET /health`（**无需鉴权**）返回就绪 / 禁用的 provider 及模型，用于探活。
- `GET /admin/accounts`（需 `ADMIN_TOKEN`）看各账户在途数 / 冷却 / 禁用状态。
- **scratch 镜像无 shell**，无法用容器内 `HEALTHCHECK curl`。改为：
  - 宿主机探活：`curl -fsS http://127.0.0.1:8899/health`；
  - 或编排层用 TCP 探活端口 8899；
  - 或换 `gcr.io/distroless/static` 基础镜像后用外部探针。
- 日志为 JSON（slog），已对 token / 绝对路径脱敏，可直接接日志采集。

---

## 12. 升级与回滚

- **二进制**：替换 `/opt/apiforge/apiforge` → `systemctl restart apiforge`。保留上一版二进制以便回滚。
- **Docker**：`docker build` 新 tag → `docker compose up -d`（滚动）；回滚 = 切回旧 tag。
- 配置全在环境变量，升级不动凭据；优雅停机会等在途请求（SIGTERM，10s）。

---

## 13. 故障排查

| 现象 | 排查 |
|---|---|
| 启动即退出 `refusing to start` | 未设 `API_KEYS` 且绑了非回环。设 `API_KEYS` 或 `HOST=127.0.0.1` |
| `/health` 里某来源在 `disabled` | 看 `reason`：多为凭据缺失/过期或刷新失败；重新登录该 CLI 或检查 `*_AUTHS` 路径 |
| Docker 里探测不到 CLI 凭据 | scratch 无 HOME，必须用 `*_AUTHS` 显式路径或设 `HOME`（§6.1） |
| 刷新后 token 没写回 | 凭据挂载是 `:ro` 或 uid 无写权限；改可写挂载 / `ReadWritePaths` |
| grok-web 返回 403 / Cloudflare 挑战 | Go TLS 指纹被拦；`GROK_COOKIES` 里补 `cf_clearance`（§3） |
| 请求偶发 503 “busy” | 账户全忙且超过 `QUEUE_WAIT_MS`；调大并发帽 / 加账户 / 调大 `QUEUE_WAIT_MS` |
| 请求 503 “unavailable” | 账户全部冷却（429/鉴权失败）；`/admin/accounts` 看冷却时间 |
| Codex 新模型被拒“请升级” | 提高 `CODEX_CLIENT_VERSION` 到本机 Codex CLI 版本 |
| 模型 404 no provider | 该模型没有就绪的 provider；`/v1/models` 看可用列表，注意 `copilot/`、`grok-web/` 前缀 |
| 内存偏高 | 设 `GOMEMLIMIT`；流式请求会占用账户槽位直到流结束属正常 |
