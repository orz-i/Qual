# Qual OpenAPI 3.1 源契约

`openapi.yaml` 是**当前代码已实现的 REST HTTP 接口**来源，Phase 1–9 和 QUAL-21 Stage C 均遵循它；**默认运行服务仍为 Goose12 旧阶段二进制**，不能仅凭 OpenAPI 断言新审核接口已在线：

- Health、Project/Task/Bug、Activity/Audit、身份与 HttpOnly Session。
- TestCase/不可变 Version、Run/RunItem、追加 Attempt、JUnit XML 与缺陷覆盖。
- 私有 S3 Artifact 元数据/分阶段上传/校验下载、ExternalReference、可验证 JSONL ZIP/Manifest、Webhook/HMAC Subscription/Delivery。
- Phase 6 新增 `/readyz` 依赖就绪（PG/当前 schema11/可选 S3）和按高熵独立运维 Token 有条件开放的 `/internal/metrics`（低基数 Prometheus 文本），与 `/healthz` 进程存活职责分开。
- Phase 9 新增 `GET|POST .../work-items/{itemID}/role-entries`：真实 Principal 的不同职责、外部证据、服务端 self_review 标记，写入同事务 Audit/Activity/Outbox，历史只追加并纳入 ZIP。
- QUAL-21 Stage C 候选新增 `/review-policy` 项目策略、`/work-items/{itemID}/review-policy` 工作项有效策略、`/acceptances` 真签收历史和显式 `POST /complete` 双模式 Done Gate；Goose12 下旧 Done 是历史未分类，Goose13 会拒绝旧 `/transitions done`，未经 Human 授权默认数据库不会迁移。
- Phase 8 新增 `/api/v1/projects/{projectID}/access/me`、`/access`、`PUT /access/{principalID}/permissions/{permission}`，按 Credential Scope + Project Membership + Allow/Deny/Inherit 执行。普通 Human/Agent 可被明确授权审核或读审计；Integration Reporter 仍受限。

```bash
pnpm contracts:lint
pnpm contracts:generate
pnpm contracts:check
pnpm check:sdk
```

生成目标 `packages/sdk-ts/src/schema.d.ts`，**禁止手改生成物**。Web 使用 HttpOnly Session+CSRF，CLI/MCP 使用独立 scoped PAT。Phase 5 新增 `artifact:read/write`、`archive:export`、`webhook:manage`；Phase 8 新增 `access:manage`。Human/Agent 可按 Project Allow + Credential Scope 被明确委托导出、审核、审计、Webhook 操作，但不自动提升旧 Token 的权限。Integration Reporter 不可获得高权 Scope；委托 `access:manage` 只允许 Human Workspace Admin，受托 Agent 不得自增/转授。

受控本机 `cmd/qual-mcp` 复用 REST 并使用官方 Go MCP stdio SDK，**候选 Stage C 共 34 Tools**（原28项：9 work、10 test、4 evidence、3 access、2 role history；新增项目审核策略2、工作项策略2、签收历史1、显式完成1）。目前实际已注册 PI 仍为旧28项，Human 完成有权的 Go/CLI/MCP/Web 发布后需要客户端重新加载，不可因生成代码就声称运行中已经34项。大字节上传使用 REST/CLI，不通过工具参数传输 Base64 巨量内容。**没有公开的远程 HTTP `/mcp` OAuth Endpoint**。

Phase 6 的 `qual-ops` 在**本地运维 CLI** 里提供签名 PG/S3 备份与**随机新库/桶恢复演练**，不对 Agent/REST/Web 开放。未来内容（项目 ZIP 跨安装导入、生产 OIDC/远程 MCP OAuth、对象扫描/HA）不在此源契约中；详见 [Phase 6 验收](../../docs/engineering/phase6-verification.md) 和 [运维手册](../../docs/runbooks/phase6-operations.md)。
