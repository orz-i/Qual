# Qual Web（Phase 9 单 Agent 多职责与 Phase 8 统一权限，本机受控版本）

TypeScript + React + React Router Data Mode + Vite + Tailwind CSS + shadcn/UI + TanStack Query + 生成的 OpenAPI TypeScript SDK。

```bash
pnpm install --frozen-lockfile
# 从仓库根目录启动：Go 需配置 DATABASE_URL、QUAL_WEB_ORIGIN 与私有 QUAL_S3_*。
pnpm --filter web dev --host 127.0.0.1 --port 5173
pnpm lint && pnpm typecheck && pnpm build
```

本机 Human 通过离线 `qual-admin browser-code` 生成的一次性码登录，使用 HttpOnly SameSite Strict Session，写请求需 Session 绑定 CSRF Token + 同源 Origin。浏览器**不保存 Agent PAT 或长期 S3 密钥**。

## 真实业务路由

- `/` 工作台、`/login`、`/projects` 项目选择。
- `/projects/:projectID` 工作项看板、`/projects/:projectID/items/:itemID` 任务详情及私有附件/外部引用。
- `/projects/:projectID/tests/cases`、`/projects/:projectID/tests/cases/:caseID` 测试规范和版本。
- `/projects/:projectID/tests/runs`、`/projects/:projectID/tests/runs/:runID` 外部运行、Attempt、JUnit、证据。
- `/projects/:projectID/archive` 管理员创建 ZIP/JSONL/Manifest 并下载。
- `/projects/:projectID/webhooks` 管理员查看订阅、首次密钥、禁用和 Delivery 状态。
- `/projects/:projectID/access` 当前 Human/Agent 有效权限、项目成员与权限覆盖（Allow/Deny/Inherit）；授权提交需 reason、CAS 版本、Human Session CSRF 或 scoped Agent Token。只有具有 `access:manage` 的凭证能列成员、修改他人权限。Human Workspace Admin 可委托 Access Manage，Agent 不能自授或转授管理权。
- `/projects/:projectID/activity` 活动审计查询。
- 工作项详情新增 **职责与证据履历**：同 Principal executor/tester/reviewer/auditor 的只追加记录、来源/版本/时间、服务端 `self_review` 标签；新增角色记录表单沿用当前 Human Session + CSRF 和 CAS，绝不提供伪造 Principal 功能。

附件由同源 REST 分阶段上传并在 S3 回读验证 SHA-256，最大 10 MiB；导出限制 ZIP 32 MiB、每表 1000 行、最多 100 个 Ready 附件，超限拒绝而非静默截断。

Webhook 只投递数据库 Outbox 里已有的工程事实，不触发 CI、Agent 或测试。需要服务端预设随机 `QUAL_WEBHOOK_ENCRYPTION_KEY` 与 HTTPS hostname allowlist，并由管理员**显式**启动 `qual-maintenance deliver-loop`。签名密钥仅注册时展示一次，禁用后不再调度新事件（在途请求仍可能完成）。

**Phase 6**：Go 服务新增 `/readyz` PG/schema10/S3 依赖就绪、独立 Operator-only `/internal/metrics` 和安全日志。浏览器不会接触运维 Token、PG/S3 备份密钥或恢复入口；部署级备份只通过 Human 运维 `qual-ops` 在随机 scratch PG/S3 环境演练。React 的 Phase 5 任务/测试/证据/归档 UI 保持现有功能，没有新增运维管理员 Web 页面。

Phase 8 新增基于 Agent 与 Human 共用应用层策略的权限管理页面；**旧 Agent PAT 不会自动获得审核、审计、归档或 Webhook 权限**。若需无人值守，用户应明确为每个 Agent 签发恰当 Scope 并在项目设置 Allow；Qual 不执行 Agent。后续高危操作可以另加双人审核/设备身份。

**仅本机受控模式**：没有生产 OIDC、公开远程 MCP OAuth、项目归档恢复 Import、病毒扫描或对象锁，不能公网部署；真实团队试点也尚未验收。详见 [Phase 6 本机验收](../../docs/engineering/phase6-verification.md)、[Phase 6 Runbook](../../docs/runbooks/phase6-operations.md) 与 [ADR-0013](../../docs/decisions/0013-phase6-reliability-backups.md)。
