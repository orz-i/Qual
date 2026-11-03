# ADR-0006：Principal/凭证分离与最小权限

- **状态**：Proposed（实施前评审）
- **日期**：2026-10-30

## Context
外部 Agent 通过不同宿主访问 Qual，同一个任务可能由不同人/Agent 修改；需要区分“谁操作、谁负责、谁批准”。

## Decision
统一 `Principal(type=human|agent|integration)`；Credential 独立于 Principal，具备 scope、期限和撤销能力。授权按 `principal status ∩ credential scopes ∩ membership ∩ resource policy` 进行，跨项目默认拒绝。审计记录操作主体和凭证 ID，而不是信任请求体中的 actor。Human UI 认证机制及标准远程 MCP OAuth 的具体提供方在进入实现前决定。

## Consequences
Agent 必须独立授权而非使用人类共享的全权 token；UI 展示 Creator/Actor/Assignee/Accountable Human 不再混淆。标准 MCP 远程授权需要 RFC 9728 Protected Resource Metadata 与 OAuth 服务端方案，受控 PAT 模式不得对外宣称完整规范支持。

## Alternatives
共享管理员 API Key 无法审计来源、授权边界过大；把 Agent 当作运行实例会破坏产品非编排边界。

## Open Questions
人类 OIDC 与内置会话何者是首版必备？自托管环境的 OAuth Authorization Server 由 Qual 自身提供还是由外部 IdP 提供？这些需要单独的实现前安全评审。
