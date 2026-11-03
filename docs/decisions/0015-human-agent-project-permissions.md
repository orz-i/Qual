# ADR-0015 · Human/Agent 统一细粒度授权与无人值守审查

- **状态**：Accepted for Phase 8 local implementation（2026-10-31）
- **目标**：将 Principal 的身份类型（human/agent/integration）与项目能力分离。Human 和 Agent 可以被赋予同一操作权限，尤其是 WorkItem Review、审计读取、项目归档及 Webhook 管理。以最小授权的独立 Principal 支持 A（执行）/B（审计）/C（复核）等无人值守协作。
- **不变边界**：Qual 只保存事实、鉴权、记录审计并向外部投递事件；不启动 PI、Agent、测试、CI 或编排任务。`work:review` 允许授权 Agent 审核 Qual 工作项，不代表 Qual 代 Agent 执行代码。管理操作不得绕过真实客户端/授权。

## 授权公式

必须满足全部条件：

1. 有效且未禁用的 Principal（Human/Agent/Integration），有效且未撤销/到期的 Credential。
2. 同 Workspace 的项目 Membership，限定 Project ID；未加入项目的资源依旧返回 404 而不是泄漏存在性。
3. 当前 Credential **显式包含**所需 Scope。签发 scope 本身不会赋权。
4. Project Permission：优先匹配该 Principal+Project 的 `allow`/`deny` 覆盖；否则继承既有 role 的**保守默认值**。角色是默认模板，不是身份类型的不可覆盖特权。
5. Integration Reporter 的测试结果专用角色仍限制不能显式获得审核/访问管理/归档/Webhook 权限。Human Workspace Admin 的 `access:manage` 不允许被自我禁用，从而避免最后一位管理员误锁死。
6. 变更权限立即对新请求生效，即使旧 Token 仍含该 Scope；撤销 Token 仍立即使所有操作失效。没有具备 Scope 的旧凭证，即使新增 Project Allow 也不会自行提升。

`permission:manage` 的命名最终选定为 **`access:manage`**：只有得到该权限的 Principal 可查询全项目授权/修改项目成员权限。任何有 `access:manage` 的 Human/Agent 都可管理普通 Scope，但**授予或撤销 `access:manage` 必须由 Human Workspace Admin 执行**；所有人不能更改自己的项目权限，也不能更改 Workspace Admin 的保护权限。该特殊操作适合 Human 在浏览器完成，Agent 不得通过给另一个 Agent 此权限绕过授权边界。

## 模型

- `qual.project_permission_overrides`：`(workspace_id,project_id,principal_id,permission,effect,version)`，effect 为 `inherit/allow/deny`。无行与 `inherit` 都使用默认角色权限，初次版本 0；每次变更 CAS expected_version，防并发丢更新。
- `qual.project_permission_changes`：所有变更 append-only，记录 actor、credential、request_id、target、permission、from/to、版本和时间。与 Audit/Activity/Outbox 及幂等结果在**单个 PostgreSQL 事务**完成；数据库阻止对历史记录 UPDATE/DELETE。
- REST `GET /api/v1/projects/{projectID}/access/me`：仅显示当前已认证 Principal 的 Project Role、有效 Scope 和最终能力，用于 Agent 自检（不读取他人的 Token）。
- REST `GET /api/v1/projects/{projectID}/access`：仅 access:manage 查看成员与当前覆盖值/版本，最多 100，避免泄漏其他租户信息。
- REST `PUT /api/v1/projects/{projectID}/access/{principalID}/permissions/{permission}`：需 `Idempotency-Key` + CAS `expected_version`，返回更新后的 effect/version；提供 `inherit` 恢复角色默认，不删除历史。
- Web 项目 `权限` 页面由 Human Session + CSRF/Origin 管理；Agent 可通过 scoped REST/CLI（未来 MCP 扩展）走同一 Application/PG 授权，**不直接写 DB**。
- `qual-admin issue` 允许 Human/Agent 的已知 Scope 在签发时预置，但这些超出角色默认值的 Scope **只有在 Human 管理员明确授予 Project allow 后才会生效**；避免管理员需要先变更 Token 与 Principal 的死循环。Integration Reporter 严格禁止高权 Scope。

## 典型配置（权限均为 Project 级）

- Agent A：role `project_member`，credential `work:read/work:write/work:claim/test:read/test:write/work:review`；Human Admin 给予 `work:review allow` 后可以自主管理和审核工作项，操作保留真实 Actor/Audit。
- Agent B：role `project_viewer`，credential `audit:read`（可选 `work:read`）；Human Admin 给予 `audit:read allow`，B 无 `work:write`、`work:review`，不能修改工作。
- Human Reviewer：原 Human Role 即可获得审核权限，也可以按项目显式 `deny`。
- Agent C：若 Human Admin 明确授权 `access:manage`，可调整**其他 Principal 的普通操作权限**，但不能赋予管理权或修改自身权限；所有操作落审计。

## 安全与兼容

- 迁移既有角色/Token **不自动放大权限**：历史 PI Agent 不会仅因升级获得 Review、Audit 或归档权限。
- 所有 Agent/Human 仍需合理最小权限、凭证独立、有效期/撤销、审计和幂等；自动审批适合被信任的 Agent，敏感任务可按项目 `deny` 或审批策略人工限制。当前没有高价值操作的双人批准、设备/网络身份/OS 沙箱。
- 不把同 Mac UID 下的 0600 PAT 当作独立进程隔离；Agent 主机仍能读取宿主的文件/Docker，生产隔离需另行设计。
- 现有 Phase 7 `QUAL-1/2` 仍处于 `in_review`，**本次授权重构不替 Human 自动完成它们**。后续可由明确授权的自主 Agent 审核，但本次不自动授予现有 PI 更高权限。
