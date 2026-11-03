# ADR-0018 · 项目成员只读身份摘要（Phase 10 整改）

- 状态：Accepted for local Qual pilot
- 关联：QUAL-13、QUAL-14；2026-11-01
- 替代方案：不在 WorkItem 中持久化冗余显示名、也不开放现有 `access:manage` 成员及权限列表给普通读取者

## 问题

WorkItem/Comment/RoleEntry 均以真实 Principal UUID 关联身份。Web 的只读项目成员只有 `work:read`，不能使用原本要求 `access:manage` 的 `GET /access` 去读取身份名称和 Human/Agent 类型。只显示“已分配”或 UUID 无法满足 QUAL-13/14 的身份核验要求。

## 决策

新增 `GET /api/v1/projects/{projectID}/principals?ids=<uuid>,<uuid>`。须先对调用者完成当前 Credential Scope + Project Membership + Project Allow/Deny 的 `work:read` 校验；请求必须包含 1–50 个合法非零 UUID，返回 `{items:[{id,display_name,kind}]}`，并设置 `Cache-Control: no-store`。查询限定该项目真实有效成员（含同 Workspace 中有效的 Workspace Admin），禁止跨 Workspace/Project 返回成员，也不返回角色、凭证、邮箱、策略或其他权限。

以服务器成员表作为实时身份事实；成员被禁用/离开项目时查不到，不根据 UUID 假定其类型或原名称。按请求的 UUID 批量读取以避免 N+1。前端只使用已知的 assignee/creator/comment/RoleEntry ID，请求失败时展示带可复制的真实完整 UUID 和“身份信息暂不可用”，绝不伪造身份。即使成员查不到，既有审计历史仍以真实 ID 可核验。

该接口供 Web/CLI 的标准授权 REST 使用，不增设 Agent 身份模拟、执行队列或用户自授权限。新增 HTTP 安全检查、PostgreSQL 项目范围检查及自动化显示测试。

## 不变约束

- 完整 `GET /access` 仍由 `access:manage` 授权且可包含权限设置。
- 业务身份从服务端解析，不覆盖历史审计数据、WorkItem 版本、TestRun/Attempt。
- 不开放到公网；仍为受控本机环境，未来生产目录隐私与可见性需单独评审。
