# Qual 本机 Operator：免查 UUID 的旧 Credential 安全收尾

**实现范围**：Go `qual-admin retire-superseded`，参见 [ADR-0024](../decisions/0024-lean-governance-and-credential-retirement.md)。此命令只负责**当前新 PAT 已安全切换后**的旧 Credential 识别及撤销；它不负责签发 Token、Agent 启动或 Project Permission 授权，不是公网接口，也不是 PI MCP 工具。

## 为什么现在不需要旧 Token / 手工 ID

- `qual.credentials` 只存储 PAT 的 SHA-256，不可找回旧明文。
- Operator 命令读取本机已经生效的 **当前** 0600 PAT 文件，以哈希匹配未撤销的当前 Credential，并用 Workspace+Project+Principal、创建时间和 Credential Scopes 选择**更早、仍有效、Scope 不大于当前**的候选。
- 默认只打印计划（UUID/签发时间/Scopes，绝不打印 Token），且不会修改数据库。只有 Operator 本人附加 `-apply` 后才会将所有候选在**一个**数据库事务中设置 `revoked_at`。重复执行安全显示 0 条。
- 如果 Scope 更宽、时间不能确定、候选多于10条、一个 Principal 有多个项目成员关系、凭证/私有文件验证失败，命令**拒绝自动处理**，不做部分撤销。
- 这依然是数据库级离线 Operator 命令：**不会伪造 Human AuthN/Audit actor**，务必保存真实操作人/时间/计划和结果摘要。Human 与 PI 如果共用 macOS UID，文件权限不是对 PI 的 OS 隔离。

## 当前 QUAL 的两条操作命令（只在 Human 可信终端执行）

前提：新 Agent PAT 已在 `~/.config/qual/qual-pi-agent.token` 生效、PI `/reload` 且 `audit:read`/项目权限已真实通过；Operator 的终端通过可信方式已经设置 **`DATABASE_URL`**（不要通过聊天或 PI 注入）。若 CLI 二进制仍是旧版本，先在 Human 终端本机编译（只需一次）：

```bash
cd /Users/mini/Coding/Qual
(cd apps/server && GOTOOLCHAIN=go1.27.3 go build -o ../../bin/qual-admin ./cmd/qual-admin)
```

**1. 预览**（不修改 DB）：

```bash
./bin/qual-admin retire-superseded -workspace qual-self -project QUAL
```

输出会显示要保留的当前 Credential ID，以及拟撤销的旧 Credential UUID、创建时间和 Scopes。确认识别出的候选都属于原 PI Agent 且现在可以失效后，再由 Operator 明确执行：

```bash
./bin/qual-admin retire-superseded -workspace qual-self -project QUAL -apply
```

两条命令都不要求提供或显示旧 Token、当前 Token、Principal UUID、旧 Credential ID；默认使用 `$HOME/.config/qual/qual-pi-agent.token`。其他 Agent 可以显式传 `-token-file /absolute/private/path`，要求父目录0700、普通文件owner0600，均不可是符号链接。

**不要经 PI 代运行上述 Operator 命令；不要将数据库管理员环境给 Agent 宿主。** CLI 可编译/单测/在专用隔离 DB 验证，但实际默认 Qual Credential 撤销必须由真实 Human Operator 管理。此命令不会自动确认用户已经完成操作。

## 结果如何验收

- 通过新的 `scripts/qual-pi.sh cli access me -project QUAL`、MCP `project_access_self` 和 `make dogfood-verify` 确认**新** PAT 仍有效。可见 `audit:read` 与 `work:review`（如已有 Allow），且 `access:manage` 未自动增加。
- `-apply` 回执和数据库 `revoked_at` 可以证明旧 Credential 已撤销。**如果 Human 没有安全保留旧明文 PAT，就不可能针对该旧 Token 真实发 HTTP 请求验证401**；只记录已撤销元数据，不声称做过旧401。隔离 PostgreSQL 的合成旧 Token 测试可证明失效机制。
- 本命令不更新 WorkItem Done、不覆盖独立 Reviewer `needs_work`，不触及 S3、测试 Attempt、项目授权配置或 Operator Metrics。
- 生产 OIDC/OAuth、按人审计 Operator 身份、真实异地恢复仍要独立处理；不为清空待办跳过这些风险门禁。

## 实际工程验证

- `go test -race ./cmd/qual-admin ./internal/identity/domain`、Go 1.27.3 和 Go Vet。
- `TestOperatorRetireSupersededRequiresProtectedCurrentTokenAndExplicitApply` 使用一次性 `qual5_rotate_*` 测试数据库以及**合成 PAT**，真实执行：预览不改库、明确 `-apply` 后旧 PAT GET401/新 PAT GET200、重复 apply 零候选、错误项目与非0600 文件拒绝、Scope 缩小拒绝且旧Token保留。临时数据库已被清除。**没有在真实默认 QUAL 库执行此命令。**
- GitHub Actions 的 migrations Job 已新增独立 scratch PostgreSQL 步骤，运行旧 `TestPhase7PATAuthoritativeRotationWithIsolatedDatabase` 与新退休正向测试，结束后强制 DROP scratch DB；本机已实际通过同类隔离集成和该 Workflow 步骤的 Shell 语法检查。**尚未执行远程 GitHub Actions**，不能称远端 CI Passed。
