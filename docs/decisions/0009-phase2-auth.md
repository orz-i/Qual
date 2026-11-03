# ADR-0009：Phase 2 受控环境 PAT 与离线引导

- **状态**：Accepted（Phase 2 首版安全边界）
- **日期**：2026-10-31

## Context

Phase 2 要真实实现外部 Agent 与 Human 任务管理的服务端身份隔离和审计，尚未实现 Web 人类登录，也未决定企业 IdP 及公开 MCP OAuth 服务端。这些能力不可用匿名接口或硬编码管理员默认凭证替代。

## Decision

- Phase 2 仅为**本机/受控部署**提供 REST Bearer Credential：随机 256-bit token，仅在创建时向本机管理员显示；数据库仅保存 SHA-256(token)（随机高熵 token，不是用户密码），凭证绑定 Principal、Workspace、scope、失效日期和撤销状态。
- 操作主体 `Principal` 区分 `human | agent | integration`。角色为 `workspace_admin | project_maintainer | project_member | project_viewer`，有效权限是“Principal 有效 + 凭证有效且 Scope 匹配 + Project Membership/Role + 资源策略”的交集。
- **首次管理员/Workspace/Project 由本机离线 `cmd/qual-admin` 完成**（无 HTTP 公共引导端点，不内置默认账号或种子 token）；添加 Agent/Integration 凭证与撤销也走本机管理入口。普通业务 API 不可以提升 Principal/角色/Scope。
- Web 仍只展示无身份的 Phase 1 工程页面，直到安全的浏览器会话或 OIDC 方案完成。Phase 2 绝不将 Personal Access Token 放进浏览器 LocalStorage、URL 或明文日志。
- 所有业务端点必须认证且按 Project 授权，项目或工作项跨租户访问对未经授权的请求返回 404；缺失/失效 token 返回 401；已授权但 Scope/角色不足返回 403。
- 终态 `in_review → done` 仅允许 Human Principal，并须持有 `work:review` 与足够项目角色；不会通过请求体提供 `actor_id` 冒充。
- 网络保护：本机默认 `127.0.0.1:18080`，**向外网暴露之前必须提供 HTTPS 反代、限速、身份服务和单独部署评审**。PAT 模式不代表已完成远程 MCP OAuth。
- 认证选择不改变 Agent 的本质：它只是外部 API 客户端，领取工作项不导致执行、调度或模型调用。

## Consequences

Phase 2 可在本地真实演示人/Agent 授权和审计，并实施 BOLA/IDOR 与 token 撤销测试；无法用当前 Web 页面登录。更完整的人类 OIDC/Session、MCP OAuth Discovery 和生产部署由后续 ADR 决定，不得将 PAT “暂用”推广为公开互联网的安全承诺。

## Alternatives

- 匿名开发模式 / 共用管理员 token：破坏隔离和审计，不选。
- 内建用户密码登录：过早引入密码恢复、多因素和会话攻击面，不选。
- 立即落地完整 OAuth Server：与 Phase 2 核心任务不成比例，后续单独实施。
