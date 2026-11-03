# ADR-0013：Phase 6 本机运行可靠性、备份/恢复演练与试点门禁

- **状态**：Accepted for controlled local implementation（2026-10-31）
- **产品边界**：Qual 只存储、检索、审计及通知工程事实，不执行 Agent/CI/测试/模型。
- **部署边界**：本阶段只验证 loopback 的开发/试点候选，生产 OIDC/TLS/HA/第三方安全评估不在本阶段自动完成。

## Readiness 与观测

- `GET /healthz` 为 HTTP 进程 liveness，不因 PostgreSQL/S3 故障改变为 503。
- `GET /readyz` 为依赖 readiness，需 PostgreSQL Ping、数据库迁移版本和可选 S3 HeadBucket（配置了 S3 才检查）；超时返回 503 和非敏感 `{"status":"unavailable"}`，不返回 DSN、Bucket Key 或依赖栈。
- 结构化请求日志使用请求 UUID、路由**模板**、HTTP 方法、状态、延迟；不记录参数值/原始 Path/Query、Body、Authorization 或 Cookie。
- 仅 `QUAL_METRICS_TOKEN` 明确配置高熵令牌时开放 `GET /internal/metrics`，使用独立 Bearer 密钥验证，不能拿普通 Agent PAT 或 Session 读取；Prometheus 文本指标只使用固定 route template、方法和 status class，控制 cardinality，包含请求数和时延直方图。不上报主体、工作项 ID、文件名。
- 业务请求统一设置 `X-Content-Type-Options`、`Referrer-Policy`、`X-Frame-Options`、`Cache-Control`；panic 恢复后返回统一 500 而非泄漏栈。

## 备份与恢复

- 使用 PostgreSQL 18 `pg_export_snapshot()`/ `pg_dump --snapshot` 共享同一 REPEATABLE READ 一致性视图，以 pg_dump Custom format 将**整个开发 DB**备份；同时从快照枚举 ready/uploaded Artifact 和 Export 的 S3 object Key，通过 S3 私有 SDK 读出字节，按数据库声明的 SHA-256 验证。
- 本地备份目录必须是新建的 0700 私有目录，dump/object/Manifest 均为 0600；Manifest 包含 schema version、数据快照时间、pg_dump SHA、对象逐个 hash/字节数、表指纹（不含令牌明文）。
- 只提供 **显式操作员命令** `qual-ops backup`、`qual-ops verify`、`qual-ops restore-drill`。演练创建带随机名称的全新 `qual_drill_*` PostgreSQL 数据库与临时 S3 桶，禁止操作已有 DB/默认 Qual 桶；恢复文件并核对表级内容指纹和每个附件 hash。演练结束自动清理临时 DB/桶。整个行为均不得触发任务/测试/Webhook Worker 或 Agent。
- 为避免 PG/S3 没有跨系统原子快照，备份要求**维护窗口/写入暂停**：业务方暂停 API 写入、上传与 Webhook Worker，备份工具再使用同一 PG 快照并校验对象；边界仍可能遇到直接外部 S3 变更，此时校验失败且备份拒绝。不可宣称热备全局原子或零数据丢失。
- `pg_restore` 会执行备份内 SQL；只在**可信由本机 Qual 生成**的备份上、自动生成的空白 scratch DB 演练，不对用户提供可指定目标 DB 的恢复命令。严禁将备份上传到公开位置；DB Dump 含凭证哈希和可逆加密的 Webhook 密文，即使没有明文也应视为敏感数据。
- 此项是**部署级灾备演练**，不等同于 Phase 5 项目 ZIP 的版本化跨安装恢复导入；后者仍未交付。

## 性能与安全

- 在独立测试 DB 下以固定并发、有限样本进行项目列表/工作项读取的吞吐、p50/p95/p99 基线采集，记录硬件/工具条件；不以一台本地 Mac 的数字承诺生产 SLA。
- 保留 Go race、vet、OpenAPI、前端构建、数据库迁移 Up/Down/Up、真实 S3/Webhook/CLI/MCP 集成；增加无鉴权 metrics/数据泄漏测试、取消/超时与恶意输入回归。
- 试点需 Human/Agent 双主体、反馈记录、权限演练、离线备份恢复、运维事故演练、升级与回滚测试。**没有真实外部团队反馈不可将试点标记完成**。

## 参考

- PostgreSQL 18 `pg_dump --snapshot`、`pg_export_snapshot`、`pg_restore --exit-on-error` 官方文档。
- Prometheus 文本 exposition/histogram 规范；指标必须保持低基数。
- [ADR-0012](0012-phase5-artifacts-exports-webhooks.md)：S3/归档/Webhook 数据与密钥安全边界。
