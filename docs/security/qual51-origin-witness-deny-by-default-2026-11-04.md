# QUAL-51 · 新鲜 Spool 来源标记与旧签名恢复纵深防护（2026-11-04）

> **状态：实现了另一项 fail-closed 安全防护；尚未验证独立来源新鲜度，不应将 QUAL-51 记为 Done。** 此分支仅修改外部 GitHub Adapter 及隔离合成联测；本机默认 Goose17、PR #1/#2 的正式审查与实际 webhook 权限均未改变。

## 漏洞模型

现有恢复隔离在经独立 Operator HMAC 验证的旧签名快照上创建 `recovery-quarantine.json` 与 `recovery-witness.json` 两份保护文件。此前仅检查它们是否同时存在且内容一致；如果发生恶意或误操作**同时删除两份**，`RecoveryStatus()` 可能误把旧恢复队列视为从未恢复过的普通队列，并可在源地址中重新承认旧 T1 后的签名事件。HMAC 证明备份完整性，不能证明备份包含 T0 后的所有已接收 GitHub Delivery。

## 本轮增强

1. **新来源身份仅允许在新建空目录时产生。** `OpenSpool(root)` 发现原目录真正不存在后，创建 0700 目录，并生成两个同样的 0600 `active-source-origin*.json`。随机的 Nonce SHA-256、规范化绝对根路径 SHA-256、创建时间和 Schema 版本被写入两份见证，经过文件和目录 fsync 后才认定可用；重新打开时必须校验两份一致。不存在、权限不安全、路径不一致、格式错误时拒绝启动。
2. **拒绝对未知旧目录自动补签。** 预先存在的队列目录，即使里面没有记录，也绝不会因新版本首次运行而自动获得有效的 ACTIVE 来源见证。否则旧已恢复队列只要删除两份 quarantine 标记就能被错误地“重新初始化”。真正需要迁移旧 Spool 时，先实施独立来源对账、专用 Operator 迁移方案，不能删除记录后按新 Spool 自动启用。
3. **签名快照根本不包含 ACTIVE 见证。** 现有 `CreateSignedBackup` 只打包 Pending/Delivered/Archived/ArchiveIndex 的受限脱敏回执；原根目录的 `active-source-origin*.json` 不进入 ZIP。合法 `RestoreSignedBackup` 在新暂存目录先生成双 quarantine 标记，OpenSpool 允许只读检查但不会通过 `requireActive`。
4. **所有写入口均复用真实 ACTIVE 检查。** `NewHandler/serve`、`Enqueue` 和 `Publisher/once` 等已存在的 `requireActive()` 现在检查来源见证，避免绕过 `OpenSpool` 直接构造 `Spool{Root: ...}` 的路径。如果删除恢复目录中的双 quarantine 标记，因为缺少 ACTIVE 见证，正常 OpenSpool、HTTP Receiver、Publisher、Enqueue 均拒绝而不是确认 202 或发送新的 Qual Event。
5. **根路径绑定的限制。** 正常 Spool 不能直接通过 OS move/rename 到另一目录继续正常服务；这会拒绝启用，需要受权迁移与完整来源对账。所有动作在 Owner-only 测试目录内进行，不修改默认业务数据。

## 真实工程和隔离验证

- Go Adapter 模块全部 `go test -race -count=1 -v ./...`、`go vet ./...`：**49 条顶层 Go Race PASS、0 Skip**。新增三个顶层测试分别验证新源双见证、删除一份/两份、将见证从另一目录复制、异常老 Spool 不能自动取得身份、旧签名恢复删除两份 quarantine 后仍拒绝入站和 worker。原始日志 SHA-256 `b2d0ab32d2f67c63425879c6590de3cb4eb0c071c43ad33ee2dda57811e9b1d7`。
- 正式名称保护脚本 `scripts/tests/ci_goose17_guarded.sh`，在新的、按名称限制的合成 Goose17/Goose12 数据库和测试 S3 中真实执行 **19/19 专项 PASS**（ExternalEvent、真正外部 Adapter 子进程的签名 Event/WorkItem/AgentRun/TestAttempt/Ready S3、Context、Archive、旧 Done/Goose12）。在 T0 备份→T1 两个真实 PostgreSQL Event/Audit→T2 恢复旧签名快照后，还测试**同时删除两份 quarantine，独立启动 inspect/serve/once 均拒绝**。原始隔离日志 SHA-256 `d3af24800fe54c6363a9f1c05b72cd4999b019a7e97d4f9e0655f26c68f43089`。
- 所有模拟 HMAC/凭据均为测试限定值，未读取真实 Operator HMAC、GitHub Webhook Secret 或生产 PI PAT，没有对正式数据库做升级、写事件或启动 Adapter 公网服务。

## 必须明确保留的生产阻断项

**本轮源见证不是独立可信的签名授权或来源账本。** 同一 macOS UID 的恶意执行者仍有能力同时删除、复制或伪造两份来源标记；本地随机 Nonce 和路径 SHA 不构成密码学认证。更老的 Adapter 二进制也可以完全忽略此协议。因此它解决了单纯“双隔离标记丢失导致无意恢复启用”的 fail-close 路径，但**不构成强可信抗恶意回滚**。

真正解除隔离必须来自独立 OS/签名权限边界的可信 GitHub Delivery 与 Qual 实际 Event/Audit 全量对账，能够证明 T0 后已有事件、高水位、晚到/乱序、重签和跨 Delivery ID 同 Body 的去重连续性。当前代码继续**没有任何同 UID 的 --approve 解锁路径**；即使 CI 和 Go Race 全绿，旧恢复队列也只能维持 Quarantine，不能生产启用。正式 GitHub App/TLS 入站、专用 Integration Credential 及独立不同授权主体的安全验收仍 Pending。

**操作提醒：** 部署此新版本以前，现有合法老 Spool 不能通过创建两个文件或直接更改路径来继续服务。Operator 必须保留原始历史，使用受审的专用迁移和真正来源对账协议；不要删除旧历史/备份来“修复”启动错误。
