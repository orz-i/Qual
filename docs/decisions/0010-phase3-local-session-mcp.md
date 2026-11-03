# ADR-0010：Phase 3 本机浏览器会话与 stdio MCP

- **状态**：Accepted for controlled local development only（2026-10-31）
- **适用边界**：仅 `localhost` / `127.0.0.1` 自部署演示和受控个人工作站，不代表生产公网认证方案。

## 背景

Phase 2 的 Bearer PAT 只适合 API/Agent，不允许进入浏览器 LocalStorage。需要让 Human 通过 Web 使用 WorkItem，但项目尚无已选定的企业 OIDC/Identity Provider。MCP 2026-07-28 远程 HTTP 需要 OAuth 2.0 Protected Resource Metadata、OAuth 2.1 授权服务端和 audience/resource 校验，不能把 PAT 接口声称为标准远程 MCP OAuth。

## 决定

- Human 通过 **qual-admin 本机离线命令**为现存且 active 的 Human Principal 签发 5 分钟有效、32 字节密码学随机、一次性登录码，数据库仅保存 SHA-256 digest。管理员通过安全本地渠道将代码交给本人。禁止匿名注册、默认口令或开放的 HTTP 身份创建端点。
- 本机浏览器在登录表单输入一次性码，服务端原子核销后生成独立、短期、可撤销的 **HttpOnly、SameSite=Strict** 会话 Cookie；数据库保存会话哈希，永不在 HTML/JS/LocalStorage 中存 PAT 或会话 Cookie 明文。
- 写请求要求与 Session 绑定的 CSRF header，并强制同源 Origin；服务端每个请求重新查已启用 Principal、未撤销 Session、项目角色与 Scope。Logout 立即撤销。会话最多 8 小时；浏览器不发 Agent PAT。
- 仅允许在 loopback HTTP 进行不带 Secure 的开发 Cookie。Go 服务启动时验证 `QUAL_HTTP_ADDR`，**拒绝监听 `0.0.0.0`、`[::]` 或非 loopback 地址**。**不开放公网模式**；未来生产使用 OIDC+PKCE 或现有 IdP，并通过独立 ADR 明确 HTTPS、反代、会话安全、限速及多因素策略。
- 对外 Agent 第一版提供官方 Go SDK 的 **stdio MCP Server**。外部 Agent 宿主只注入 scoped PAT 和 Qual REST API 地址，**不需要 PostgreSQL 凭证**。stdio MCP Tool 通过受保护的 REST 访问 Go Application Service，CLI 也是相同的 REST 客户端。服务端内部 MCP Bridge 的直接应用层接口仅用于集成测试/可信进程调用。**不启动 Agent、不做编排、不执行测试。**
- 远程 Streamable HTTP MCP 暂不公开。后续只有实现 OAuth Protected Resource Metadata + audience validation + 互通测试后才可称为公开兼容。

## 权衡

一次性离线登录适用于自托管本机和开发联调，不是大规模团队登录、账号恢复、跨设备身份系统。安全在于短生命周期、强熵、一次消费、同源 Session 与独立 Human 主体；保留 `qual-admin` 的受控数据库管理权限，不直接给普通用户/Agent。未来可移除浏览器登录码但保留 Session/Actor/DDD 接口。

## 替代方案

- 浏览器存 PAT：拒绝，难以安全处理脚本与生命周期泄露。
- 默认管理员用户名/密码：拒绝，存在初次部署口令泄露及恢复负担。
- 在 Qual 内自建完整 OAuth Authorization Server：当前不做，优先后续 OIDC 接入。
- 仅提供 CLI 而没有 Web：不能验证人机共同使用场景。
- 外部 stdio MCP 直接连接数据库：会向 Agent 宿主暴露过强的凭证权限，因此外部适配器最终选择仅使用项目授权 REST，旧的进程内 Bridge 接口限服务内使用。

## 核心参考

- MCP 官方说明 https://blog.modelcontextprotocol.io/posts/2026-07-28/
- MCP OAuth discovery https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/authorization/authorization-server-discovery.mdx
- MCP Go SDK https://github.com/modelcontextprotocol/go-sdk
