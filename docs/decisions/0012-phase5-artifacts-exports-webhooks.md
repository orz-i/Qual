# ADR-0012：Phase 5 受控附件、快照导出与 Webhook

- **状态**：Accepted（Phase 5 历史架构；Phase 8 的 Human/Agent 统一授权以 [ADR-0015](0015-human-agent-project-permissions.md) 为准，以下 Phase 5 的 Human-only 描述是原始策略）
- **范围**：本机 loopback 服务；生产 OIDC/OAuth/TLS/HA 运维依然未交付。
- **边界**：只保存/检索工程事实，投递已产生的事件，不执行测试、CI、Agent、脚本或模型。

## 附件

使用官方 AWS Go SDK v2 的 S3 API 对接 SeaweedFS（生产可替换为兼容 S3 服务）。S3 访问密钥只在 Qual 服务端环境中；客户端仅使用带项目 Scope 的 REST。

三步：`POST /artifacts` 验证目标类型和项目内 ID、上传大小、SHA256、MIME，创建 `pending`；`PUT /artifacts/{id}/content` 接收最多 10 MiB 的字节并校验大小/散列，保存到随机私有对象 Key，状态变为 `uploaded`；`POST /artifacts/{id}/complete` 由服务端重新 GET S3 校验字节数/散列，原子将元数据转为 `ready` 并审计。同一对象不能覆盖已上传字节。失败对象清理为 `failed`，另提供显式过期 pending 清理命令；不声称 S3 与 PostgreSQL 跨系统存在原子事务。GET 元数据或下载必须重新认证授权；下载通过同源代理、Content-Disposition attachment、nosniff，不暴露裸 bucket Key。

上传允许受控的 WorkItem、TestCase、TestRun、TestAttempt 目标。外部来源者只能声称上报了证据，Qual 不担保测试确已执行。下载方仍可自行验证 SHA256。Phase 4 PostgreSQL 中既有 JUnit 小报告保留原文，未来另行评审自动迁移。

## 外部引用

`ExternalReference` 是外部 provider/kind/id/HTTPS URL 的事实记录。不会在后端请求该 URL。只允许指向同项目受控目标，按唯一来源去重，同事务记录 Actor/Audit/Outbox。

## 归档

`archive_schema_version=1`；Human 管理员/维护者且有 `archive:export` 权限方可创建。PostgreSQL REPEATABLE READ/READ ONLY 一致性快照读取版本化的 JSONL（以稳定顺序排序），单类 1000 行上限；对象存储 ready 附件作为不可变字节按散列验证后放进 ZIP，附 `manifest.json`，列 SHA256、大小与行数。限制最大 32 MiB（MVP），超限直接拒绝，不做不完整的静默成功。快照时间来自数据库；导出操作的 Audit 在快照完成后另一个事务中写入，不声称该审计也出现在自身快照。归档下载必须鉴权，且再次验证 ZIP SHA256。MVP 不支持恢复导入、增量导出或签名公证。

## Webhook

只有 Human 管理员/维护者且有 `webhook:manage` Scope 可创建/禁用订阅。为每个订阅生成高熵 HMAC secret，只显示一次；数据库使用由部署级 `QUAL_WEBHOOK_ENCRYPTION_KEY` 配置的 AES-256-GCM 加密，绝不以明文写库。事件来自已有事务性 Outbox；为订阅建立唯一 Delivery，含状态、尝试次数、下次投递时间与失败摘要。独立维护进程按租约领取，调用固定事件 URL，HMAC-SHA256 签名覆盖时间戳与原始 body，并发送 event/delivery ID，消费端按 ID 幂等。失败指数退避、最大 5 次后 dead；禁止重定向，网络请求超时。重试表示**至少一次**，不是恰好一次。过期锁允许恢复。

安全首版要求单独的目的地 hostname allowlist（`QUAL_WEBHOOK_ALLOWED_HOSTS`），HTTPS 且实际拨号地址必须是可公开路由 IP，阻断 DNS rebinding/环回/私网/metadata（OWASP）；只有显式受控集成测试可使用 loopback HTTP。公共 webhook 不会通过普通 Agent PAT 管理，也不能借事件链启动 Agent。生产需额外配置网络层 egress 限制、secret 轮换/审查、告警及 HA。

## 替代与限制

- 浏览器直连 S3/S3 长期密钥：拒绝。MVP 采用 REST 同源上传代理而不是 presigned URL；大文件/分段续传后续另行设计。
- 原样 SQL Dump：拒绝，使用版本化 ZIP/JSONL/Manifest。
- 在业务事务内直接发送 webhook：拒绝，改用独立可审计的投递器。
- Webhook 任意 URL 或跟随 Redirect：拒绝。无 allowlist 则不注册订阅。
- 事件发送器 != Agent 编排器，不允许触发测试/CI 运行。
