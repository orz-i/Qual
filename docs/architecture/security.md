# 身份、授权与威胁模型

> **状态**：Phase 2–4 已落地 scoped PAT/Project RBAC/Scope、受控本机 Human Session 与追加测试历史；Phase 5 实现私有 S3/Hash、Human 项目导出及 Webhook HMAC/目的地限制；Phase 6 新增 schema/S3 就绪检查、独立 Operator metrics、脱敏日志、安全响应头、HMAC 签名 PG/S3 备份和随机 scratch 恢复演练。Go 仍**只监听 loopback**，生产 OIDC/远程 MCP OAuth/完整网络出口与密钥托管审查未交付。见 [ADR-0009](../decisions/0009-phase2-auth.md)、[ADR-0010](../decisions/0010-phase3-local-session-mcp.md)。正式 Human OIDC/SSO、远程 MCP OAuth、生产 TLS/限速/部署仍未实现，**严禁作为公共服务发布**。

## 1. 可信边界

人类 Web 浏览器、外部 Agent、Go CLI、CI Reporter 都是不可信请求来源。不存在“因为来自 MCP 就自动可信”的权限。Qual 的唯一可信执行边界为服务端 AuthN/AuthZ + Domain/Application 规则，数据库与对象存储在受控网络内。

`ActorContext` 包含 `principal_id`、`principal_type`、`credential_id`、`workspace_id`、`authorized_project_ids`、`scopes`、`request_id`、`source`。客户端不能通过 JSON 字段直接设置这些特权字段；服务端从认证结果推导。

## 2. 身份与凭证

| 身份 | 初期方向 | 注意事项 |
| --- | --- | --- |
| Human | Phase 3 本机一次性码 + HttpOnly SameSite=Strict Session，写请求 CSRF+同源 Origin；生产 OIDC/SSL 尚未交付 | 仅受控 loopback HTTP；公网时必须 TLS Secure Cookie、OIDC/PKCE 与分布式限速 |
| Agent | 独立 Principal + scope 限制的短期或可撤销 Personal/Service Token | token 只存 hash；按 Project/操作类型最小权限，Phase 4 新增 test:read/write |
| Integration | 单独 Principal，允许只写指定 TestRun/Artifact/ExternalReference | 不给读取全项目或分配任务的权限 |
| MCP 外部客户端 | Phase 3 只有本机 stdio 客户端，宿主通过 scoped PAT 调用同源受保护 REST；公共远程 MCP OAuth 尚未实现 | 不应向 Agent 宿主下发 DB 连接串；PAT+REST 不能宣称标准远程 MCP OAuth 兼容 |

MCP 2026-07-28 标准远程授权使用 OAuth 2.1 概念与 Protected Resource Metadata（RFC 9728）。如 MVP 暂不实现完整 OAuth discovery，应将 MCP 适配说明标注“受控环境 PAT 模式/兼容范围”，不在对外文档宣称完整标准授权支持。

## 3. 权限设计

- RBAC 最小角色：`workspace_admin`、`project_maintainer`、`project_member`、`project_viewer`。
- **已实现** Scope：`work:read`、`work:write`、`work:claim`、`work:review`、`audit:read`、`test:read`、`test:write`、`artifact:read`、`artifact:write`、`archive:export`、`webhook:manage`、`access:manage`。Human 与 Agent 可以获得相同的明确项目授权，Integration Reporter 保持受限。旧 Agent 不因迁移自动获得审核/归档/Webhook 权限。
- **Phase 8 最终授权 = Principal Active ∩ Credential Scope ∩ Project Membership ∩ (Project Override allow/deny/inherit + 默认角色)**。Deny 优先，allow 不绕过 Token Scope。所有 Project Permission Change 采用 CAS/Idempotency，在事务中同步写不可变历史 + Audit/Activity/Outbox。
- 删除/归档、Webhook、普通权限修改可委托明确授权的 Human 或 Agent；任何 Principal 均不可修改自身项目权限。`access:manage` 必须由 Human Workspace Admin 首次授予，受托 Agent 不能再分发管理权，也不能修改 Workspace Admin。密钥管理、正式系统运维仍独立。
- 历史 Phase 2 的 `in_review → done` 仅限 Human。**Phase 8（ADR-0015）已改为具有有效 `work:review` Scope + Project Allow 的 Human/Agent 均可审核完成**，即支持外部 Agent 无人值守审核；可按项目 Deny 敏感 Agent。当前尚无任务级双人审批或 OS 沙箱。

## 4. 威胁与防护

| 威胁 | 防护与验证 |
| --- | --- |
| 伪造操作主体 | 服务端从凭证产生 ActorContext，拒绝传入自称 actor |
| 越权读取/修改别的项目 | 所有查询必须带 Project 范围过滤；自动化 BOLA/IDOR 测试 |
| Agent 抢占或覆盖 | 条件 Claim、`expected_version`、409 冲突、不丢失更新 |
| 重复 Result / Webhook | 有作用域的幂等键、唯一索引、交付记录 |
| 不可信任务文本指令注入 | 文本作为数据处理；不能改变服务端工具权限；不进行模型推理 |
| 外部 URL 与 Webhook SSRF | ExternalReference 只记录不访问；Webhook 需要精确 HTTPS hostname allowlist，DNS 查询结果必须全为安全公网 IP，拨号锁定 IP、禁止重定向/代理，HMAC 签名及超时；生产另需网络层 egress 防火墙 |
| 附件泄漏/恶意文件 | 私有 S3 桶、同源授权下载、10 MiB 上限、声明/回读 SHA-256、S3 密钥仅后端持有；病毒/内容扫描和对象锁目前**未实现**，生产前评审 |
| API Token 泄漏 | hash 存储、token 期限和撤销、路由模板日志不含原始 Query/Body/Cookie/Authorization；Phase 6 Operator metrics 使用**独立于 PAT 的高熵 Bearer**且默认 404。正式分布式限速、密钥轮换仍未完成 |
| Audit 数据篡改 | 最小 DB 写权限、追加记录、备份与校验、特权操作审计 |
| MCP DNS Rebinding / Origin | 按 MCP Streamable HTTP 规范校验 Origin；本地默认仅绑定 localhost |
| 个人/敏感信息通过导出泄漏 | 项目级授权/快照校验；Phase 6 原生 PG/S3 备份包含敏感凭证哈希和加密密文，HMAC 仅保证完整性而不加密文件，备份目录 0700/0600、拒绝 symlink，异地备份加密/保留需另行部署 |

## 5. MCP 特别要求

- Tool input 不可信，`tools/call` 的具体授权由服务端重新验证。
- 2026-07-28 Streamable HTTP：`POST /mcp`、协议版本与请求头严格校验；历史会话/GET 方式只做显式兼容，不盲目复制旧教程。
- 工具列表与描述应稳定；能力发现不意味着授权使用全部工具。
- 根据发布环境限定 CORS/Origin、TLS、代理可信头、速率限制；不得把 token 写在 URL、MCP Tool 输出或日志中。
- Resource 查询返回范围必须等同于 REST 的 Project 范围，绝不能把用户导入内容的 Prompt 文字解释为系统命令。

## 6. Phase 6 安全与灾备门禁

- 已本机通过 `govulncheck` 代码可达 0 个漏洞（Go 1.27.3，x/text 0.41.0），仍存在 1 项未触达依赖告警；`pnpm audit --prod` 当前无已知漏洞，开发期 shadcn 上游仍有告警。
- 已验证缺少或错误 Operator metrics Token 拒绝访问，HTTP 5xx 不泄露 DSN/原始 panic，路由模板不包含 UUID/Query；`/readyz` 非就绪 503 与 `/healthz` 存活语义分离。
- 已验证本地 PG Custom Dump + S3 私有字节与 HMAC 签名，随机 scratch PG/S3 恢复全表/对象 hash 一致，错误签名拒绝，测试资源清理。必须停写，**不属于生产热备或跨安装项目 ZIP 导入**。
- 生产 TLS/OIDC、远程 MCP OAuth、网络层 egress、可恢复 RPO/RTO、备份加密/密钥轮换、Virus Scan/WORM/HA 仍未完成，真实团队试点也尚未签收。

## 7. Phase 7 PI 同机自举边界

- 默认本机建立独立 `qual-self/QUAL` Project，PI Agent 凭证为 7 天的项目 Scope：work/test/artifact 操作；Human Owner bootstrap PAT 仅用于首次种子 REST 写入，成功后由离线工具撤销并删除文件。
- PI 本机包装脚本 `scripts/qual-pi.sh` **忽略外部继承的 QUAL_TOKEN**，强制从本机 Owner-only 的 `~/.config/qual/qual-pi-agent.token` 读取专用 Agent PAT，并把 API URL 固定到 loopback；不能意外将 Human Admin PAT 通过 MCP/CLI 发出。
- **风险不被 Scope 消除**：同一个 macOS UID 的 PI 若有任意 bash/Docker 权限，仍可读取同一 UID 的文件、连接默认开发数据库和管理本地容器。`0600` 不是 PI 进程对 Human 的 OS 级权限隔离。对不完全可信的 Agent 必须分配独立受限用户/容器、撤去 Docker 访问，并接入密钥管理器。
- PI 插件只提供 `/qual-status`、`/qual-pilot` 本机只读诊断；业务记录由 23 个 MCP 工具或 CLI 执行，且每次都走 REST 身份和项目策略。PI 已由用户进行真实项目注册，且外部 PI MCP 已完成首次读取/评论/状态操作；`QUAL-1/2` 的 Agent `done` 请求被实际拒绝，且 `QUAL-2` 通过匿名/伪造 PAT 401、跨项目 404、管理员接口 403、过期版本 409 检查，实际证据已做 S3 SHA-256 校验。**PI 及 Human 共用 macOS UID 时仍不具备 OS 级隔离；Human 最终 Review 未完成。**

## 8. Phase 9 单 Principal 多职责追溯

- 允许同一 Human/Agent 在 `executor/tester/reviewer/auditor` 之间切换，但服务端绑定的真实 Actor 始终不变；职责本身不提供任何 Scope。离线 `qual-admin issue-existing` 只能对现有有效 Project Member 签发**同 Principal** 的新 Scoped Credential，不自动修改 Project Allow 或运行中的 PI Token；真实凭证 ID 随事件保留在 DB 中。
- reviewer 记录必须有 `work:review` + `work:read`，auditor 写记录需 `audit:read` + `work:write` + `work:read`。Tester 已完成的通过/失败必须引用真实来源。权限不足必须拒绝，而非通过创建假的 Agent Principal 绕过。
- 服务端根据真实创建者/负责人/历史角色记录计算 `self_review`；即使 `reviewer:passed` 也不能冒充独立复核或自动结束 WorkItem。
- `work_role_entries` 追加记录与 WorkItem CAS、Audit/Activity/Outbox、幂等响应同 PG 事务，触发器禁止编辑/删除，项目 ZIP 包含 `work_role_entries.jsonl`；历史查询当前限 100 条，归档超额拒绝而非截断。

## 9. 上线前必过的安全测试

跨项目 UUID 枚举 / IDOR、不同 scope 的读写拒绝、禁用 Principal/撤销 token、并发 Claim/Update、重复幂等、受保护状态转移、附件下载鉴权、恶意 URL 和跨项目导出。安全日志不得包含密钥、Cookie、完整 Authorization Header。

## 10. 资料

- MCP authorization: https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/authorization/index.mdx
- MCP Protected Resource Metadata: https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/authorization/authorization-server-discovery.mdx
- MCP Streamable HTTP: https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/transports/streamable-http.mdx
