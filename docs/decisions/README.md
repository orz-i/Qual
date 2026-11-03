# 架构决策记录（ADR）

ADR 记录背景、决定、权衡、实施范围与后果。Accepted 表示已有明确依据的决策，不代表该 ADR 中所有后续功能都已交付。

| 编号 | 标题 | 状态 |
| --- | --- | --- |
| [0001](0001-product-boundary.md) | 仅管理与存档、不编排 Agent | Accepted |
| [0002](0002-modular-monolith.md) | Go DDD 模块化单体 + Monorepo | Accepted（工程骨架） |
| [0003](0003-workitem-and-testing.md) | WorkItem 类型与独立测试生命周期 | Accepted（Phase 4） |
| [0004](0004-api-first-and-mcp.md) | API-first、CLI 与本机 stdio MCP 共用应用服务 | Accepted（远程 MCP OAuth 待实现） |
| [0005](0005-audit-and-outbox.md) | Audit + Transactional Outbox | Accepted（Phase 5 显式 Webhook Worker 已实现） |
| [0006](0006-identity-and-authorization.md) | 完整 Human/MCP 身份与最小权限 | Proposed（生产/浏览器/远程 MCP 细节） |
| [0007](0007-evidence-and-archive.md) | 不可变测试记录与可校验归档 | Accepted（Phase 4/5，恢复导入待实现） |
| [0008](0008-dev-infrastructure.md) | PostgreSQL 18 + Goose + SeaweedFS 开发环境 | Accepted |
| [0009](0009-phase2-auth.md) | Phase 2 受控环境 Bearer PAT、离线引导与项目授权 | Accepted |
| [0010](0010-phase3-local-session-mcp.md) | Phase 3 本机 Human 一次性码/Session 与 stdio MCP/REST Adapter | Accepted（本机受控，不代表生产 OAuth） |
| [0011](0011-testing-facts-and-junit.md) | Phase 4 不可变 TestCase、追加结果、关联缺陷与受限 JUnit 导入 | Accepted（Phase 4） |
| [0012](0012-phase5-artifacts-exports-webhooks.md) | Phase 5 私有 S3、项目快照 ZIP、Webhook 签名与重试 | Accepted（本机受控，生产审查待完成） |
| [0013](0013-phase6-reliability-backups.md) | Phase 6 运行可靠性、签名 PG/S3 备份、隔离恢复与试点门禁 | Accepted（本机硬化完成；真实试点待执行） |
| [0014](0014-phase7-pi-dogfood.md) | Phase 7 使用 Qual 管理 Qual，自举 PI 独立凭证与本机注册边界 | Accepted（真实 PI 已注册；审核策略被 ADR-0015 扩展） |
| [0015](0015-human-agent-project-permissions.md) | Phase 8 Human/Agent 项目级 Allow/Deny/Inherit、委托审核/审计、权限审计 | Accepted（本机真实 PG/S3/Web/CLI/MCP 验证） |
| [0016](0016-single-principal-role-evidence.md) | Phase 9 单个真实 Principal 多职责记录、同人复核标记与不可变归档 | Accepted（本机 PG/S3/MCP/CLI、真实 QUAL 自举记录） |
| [0017](0017-phase10-light-brand-experience.md) | Phase 10 C 轻量现代视觉和人机协作信息架构 | Accepted（视觉方向；完整交互验收待办） |
| [0018](0018-project-scoped-principal-summary.md) | Phase 10 项目级受限身份摘要与权限边界 | Accepted（整改设计） |
| [0019](0019-test-run-quality-and-defect-trace.md) | Phase 10 TestRun 最新结果摘要和失败 Attempt→Bug 追溯 | Accepted（QUAL-15 实施中） |
| [0020](0020-review-queue-work-visibility.md) | Phase 10 项目 Review Queue 真实角色记录、范围隔离与审计权限边界 | Accepted（QUAL-16 实施中） |
| [0021](0021-review-lane-decision-precedence.md) | 审核结果优先于 WorkItem 状态的审核队列分流，独立通过与自审区分 | Accepted（审计 P0 修订） |
| [0022](0022-explicit-done-gate-owner-acceptance.md) | 明确 independent_review / owner_acceptance、Owner 风险确认及原子 Done Gate | Proposed（QUAL-21 阶段 C 源码、隔离 Go13/Go12、客户端及审计归档已实现，Human 独立审核与默认 Goose13 受控发布未完成） |
| [0023](0023-audited-resubmission-review-queue.md) | 审计证实整改后重新提交时在审核队列进入 Awaiting，不覆盖旧 needs_work | Proposed（隔离 Go/React 回归已验证，运行中服务重载和 Human 验收待办） |
| [0024](0024-lean-governance-and-credential-retirement.md) | 减少人工验收和凭证收尾成本；本机 Operator 预览/撤销旧 Credential | Accepted（分阶段实施，绝不绕开身份和审计） |
| [0025](0025-qual-superagent-collaboration-boundary.md) | Qual × Super-agent：持久事实控制平面与外部执行平面分离 | Accepted（仅架构与分期设计，QUAL-27～40 尚未实施） |

替代已实施的决策时以 Superseded 链接保留历史；正式远端 OAuth 授权与 Human OIDC 在后续阶段单独评审。
