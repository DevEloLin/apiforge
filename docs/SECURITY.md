# Security Policy / 安全策略

## English

### Reporting a vulnerability
Please report security issues **privately**, not via public issues:

- Open a **GitHub Security Advisory** (repo → *Security* → *Report a vulnerability*), or
- Email **dev.elolin@gmail.com** with details and reproduction steps.

We aim to acknowledge within a few days. There is no bug-bounty program (this is a
personal-research project).

### Scope
This project is a self-hosted gateway that **handles your own credentials**. The most relevant
security concerns are:
- credential files / tokens on disk (kept `0600`, atomic writes, redacted in logs);
- fail-closed startup, SSRF guard, keyFile path-traversal guard, constant-time admin token.

Out of scope: the upstream vendors' own security, and the inherent account-ban risk of reusing
subscription logins (see the README notice).

### Supported versions
Only the latest `main` is supported.

---

## 中文

### 漏洞报告
请**私下**报告安全问题，不要发公开 issue：

- 提交 **GitHub Security Advisory**（仓库 → *Security* → *Report a vulnerability*），或
- 邮件 **dev.elolin@gmail.com**，附细节与复现步骤。

我们会尽量在数天内回应。本项目无漏洞赏金（仅个人研究项目）。

### 范围
本项目是**处理你自己凭据**的自托管网关，最相关的安全点：
- 磁盘上的凭据文件 / token（`0600`、原子写、日志脱敏）；
- fail-closed 启动、SSRF 防护、keyFile 路径穿越防护、管理 token 常数时间比较。

不在范围：上游厂商自身安全，以及复用订阅登录固有的封号风险（见 README 提醒）。

### 支持版本
仅支持最新的 `main`。
