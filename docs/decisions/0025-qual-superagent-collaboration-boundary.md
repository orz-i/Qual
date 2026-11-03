# ADR-0025：Qual × Super-agent —— 持久化协作事实与外部自治执行分离

- **状态**：Accepted（**仅接受架构边界及分期设计**；接口、存储和 UI 均未据此自动实现或发布）
- **日期**：2026-11-03
- **工作项**：QUAL-26；后续 QUAL-27～QUAL-40（真实依赖以 Qual 工作项为准）
- **前置决策**：[ADR-0001](0001-product-boundary.md)、[ADR-0004](0004-api-first-and-mcp.md)、[ADR-0005](0005-audit-and-outbox.md)、[ADR-0015](0015-human-agent-project-permissions.md)、[ADR-0016](0016-single-principal-role-evidence.md)、[ADR-0020](0020-review-queue-work-visibility.md)、[ADR-0022](0022-explicit-done-gate-owner-acceptance.md)、[ADR-0024](0024-lean-governance-and-credential-retirement.md)
- **详细设计**：[分期协作契约 v0（设计，不是线上 OpenAPI）](../architecture/superagent-collaboration-contract.md)

## 背景与目标

Qual 已通过真实本机 PI 使用支持 WorkItem/Claim、TestCaseVersion/TestRun/Attempt、Ready Artifact 与 Git Evidence Packet、独立 Principal/Project 权限、RoleEntry/ReviewQueue、显式 `owner_acceptance` / `independent_review`、Audit/Activity/Outbox 与出站 Webhook。但跨会话恢复仍依赖多次分散读取，外部事件/Thread 的稳定多目标关联和外部 AgentRun 状态没有统一投影，Human 需从不同页面寻找需要关注的事情。

Super-agent 的 Router/Worker/Trigger/Watchdog 与跨会话恢复模式启发了**接口可恢复、执行可替换、异常才打扰 Human** 的协作方式；这不是把 Qual 改造成模型宿主或调度器。既有 25 项本机工作完成不等于独立多人试点或生产就绪。2026-11-03 新建的 QUAL-26～40 是另一批待实施项。

## 决策

1. **Qual 是可信工作事实与治理控制平面，外部 Host 才是执行平面。** Qual 保存 Project/WorkItem、权限、审核与验收、外部上报的 Test/Run/Event/Artifact、引用和审计；不提供 Agent 启停、队列调度、长期模型上下文、Prompt 编排、CI 或测试执行能力。Agent Runtime/Router/Worker/Watchdog 由 PI 或其他外部宿主实现。`work_item_claim` 只是带版本的业务变更，永不启动进程。
2. **会话不是工作真相。** 以 Project+WorkItem ID/Version 为主轴，用来源标识与关联 ID 连接外部 Thread、Event、AgentRun；通过鉴权后的有界、可继续读取的上下文组合恢复当前状态。MCP 已有 `work_item_get_context`（WorkItem、最多 10 条近期评论和依赖、`schema_version=1`），须渐进扩展而不是另造不可同步的 Session 数据库。跨聚合摘要标明各来源水位与读取时间，不把 WorkItem 版本冒充全系统快照版本。
3. **来源事实与业务结论严格分离。** 外部 Event 内容只是不可信输入；外部 AgentRun 的 `succeeded` 不等于 WorkItem `done`，也不等于 TestAttempt `passed`。只有经授权的真实 WorkItem/Test/Review Application 用例才能改变各自权威状态；已记录的失败、`needs_work` 和自审历史不得自动清洗。
4. **重用既有领域对象；只补缺口。** 优先复用 `external_references`、`work_dependencies`、`work_role_entries`、`work_acceptance_decisions`、`outbox_events` 和现有 ReviewQueue。现有 ExternalReference 唯一键 `(project_id,provider,kind,external_id)`、非空 HTTPS URL、单个 `target_kind/target_id`，**不能视为已有 Thread 多目标或任意无 URL 事件系统**；如确需一对多关系，单独设计链接表和可回溯迁移，不修改历史引用含义。Outbox/Webhook 是**服务端向外投递**，不是外部事件接收器。
5. **授权不可继承自模型或外部策略。** 所有 REST、CLI、MCP 进入同一 AuthN/Project AuthZ/Application；同一真实 Principal 可担任多个职责但绝不成为多个独立 Reviewer。任何新权限默认拒绝、仅经显式批准分配；外部 `observe → auto` 策略不能扩张 Credential Scope/Project Allow，无法绕过版本 CAS、证据门禁、`work:review` 和高风险独立审核。
6. **先事实与上下文，再观测与交互，最后按需主动化。** P0 = 契约、恢复上下文、Thread/Event 关联；P1 = AgentRun 外部报告、Attention Inbox、真实单 PI 宿主试点与回归；P2 = 可选外部 Watchdog 和经明确授权的 Observe→Auto。P2 默认关闭，非 P0/P1 上线前置。
7. **不引入第二套看板/审核体系。** Attention Inbox 是现有 Reviewer 队列和真实外部阻塞/报告的授权只读投影；它只能定位用户要处理的事项，不能快速伪批准或静默更改 `done`。Human 看板仍走现有 Web Session 与 SDK。

## 责任边界与数据归属

| 对象 | 事实持有者 | Qual 责任 | 外部 Host 责任 |
| --- | --- | --- | --- |
| WorkItem/依赖/业务状态 | Qual `work` | CAS、合法工作流、活动/审计 | 认领、实现、如实报告 |
| TestCase/Run/Attempt | Qual `testing`（外部执行观察） | 版本、不可变 Attempt、失败关联 Bug | 实际执行测试、提交真实结果 |
| Ready Artifact/ExternalReference | Qual `evidence` | 私有权限、SHA 验证、来源链接 | 提供真实内容与可信引用 |
| Review/Acceptance | Qual Work + Identity | `owner_acceptance`/独立审核原子 Done Gate | 有权主体发起真实决策 |
| ExternalThread/Event | 新增事实关联（提案） | 项目隔离、来源/事件去重、受限检索 | 监听并验证外部 GitHub/IM/CI 事件、按需执行 |
| AgentRun | 外部运行；Qual 仅存报告（提案） | 只追加事实/状态投影、异常可见性 | 启停、重试、心跳、故障恢复与 OS 隔离 |
| Attention Inbox | Qual 读模型（提案） | 从真实记录计算需要关注的条目 | 外部可选择通知或响应 |
| Observe/Auto | 外部 Router + Qual 授权追溯（可选） | 存策略/决策来源，实时鉴权 | 判断和执行有权限的动作 |

## 分界原则

- **读取**：工作项基本事实须 `work:read`；测试、私有 Artifact、原始 Audit 均额外遵守原本的 `test:read`、`artifact:read`、`audit:read`，不得借“上下文”绕权。不存在 Project 的对象不泄漏存在性；跨项目按现有 404 语义。
- **写入**：每个新命令都需真实 Actor/Credential、项目成员关系、相应有效 Scope+Project 权限、幂等键、来源校验与必要 CAS；业务变更、不可变 Audit/Activity/Outbox 在同一事务中记录，重放同请求不产生重复事实。同 `Idempotency-Key` 不同 payload 应拒绝冲突。Event 仅登记事实，不自动 Claim/修改工作项。
- **外部身份**：`provider`/`external_actor_id`/`run_id` 仅是来源标签，不能当 Qual 认证主体，也不能在用户可提交的 JSON 里接受 `actor_id`、`credential_id`、`self_review` 等特权字段。
- **持久性**：采用 PostgreSQL 版本与外键；追加式事实与投影分开，禁止用模型生成摘要替换历史。大日志/文件仍走私有 Artifact；新增导出项需考虑版本化归档与既有恢复约束。
- **安全**：入站事件由外部 Adapter 完成第三方签名/网络接入验证；Qual 仅信任自己验证的 Bearer/Session，不执行不可信 URL/HTML/Prompt；OS 沙箱、密钥隔离由运行 Agent 的宿主负责。当前受控 loopback PI 同 macOS UID **不是** OS 隔离。
- **发布**：新增字段/表/接口增量兼容并默认关闭（未就绪时不改变旧 API）；真实迁移先在隔离库与兼容客户端验证。回滚关闭新读写入口，不删除不可变历史；任何需破坏性 Down 必须拒绝或独立审批。源码/文档成功不等于已部署。

## 分期实施与验收责任

| 阶段 | 真实 Qual 工作项 | 退出原则（必须独立实测） |
| --- | --- | --- |
| 契约（本项） | QUAL-26 | 文档 ADR、候选 OpenAPI/领域模型、Scope、风险/回退和任务矩阵完整且链接可读；无新增运行端点 |
| P0 / Context | QUAL-27、QUAL-28 | REST/MCP/CLI 的有界恢复事实、分项授权、断会话重读、旧失败和旧 Reviewer 历史可追 |
| P0 / Thread | QUAL-29、QUAL-30 | 一对多按真实设计登记；Project 隔离、事件去重、乱序/并发/拒绝路径准确 |
| P1 / 用户体验 | QUAL-31、QUAL-33、QUAL-34、QUAL-35 | Thread/Run/注意力投影可追源，不伪审核，空态、移动端与权限回归 |
| P1 / Runtime 报告 | QUAL-32 | 外部 Run 报告不成为执行器；超时只显示 unknown/stale；可审计 |
| P1 / 真实试点 | QUAL-36、QUAL-37 | 唯一真实 PI 执行、换会话恢复、失权拒绝、幂等和证据回读、实际测试记录 |
| P2 / 可选增强 | QUAL-38、QUAL-39、QUAL-40 | Watchdog 不启动 Agent；Observe 默认关；受控本机试点评估不冒充生产/多人签收 |

逐项详细接口、状态、返回码、负例、指标与风险见 [分期协作契约](../architecture/superagent-collaboration-contract.md)。本次设计没有为 QUAL-27～40 记录实际测试或改变其 Backlog 状态。

## 备选方案与后果

- **拒绝：把 Router/Worker/Watchdog 编入 Go Server。** 与 [ADR-0001](0001-product-boundary.md) 冲突，增加不可信命令执行、调度与安全责任。
- **拒绝：复制 Agent Session 和新的审批流为第二套事实源。** 造成 WorkItem/Review/Test 数据冲突、独立性不可追溯。
- **拒绝：用已有 ExternalReference 假装多目标 Thread 或用 Outbox 当入站队列。** 现有 schema 约束与语义不支持。
- **接受代价**：未来需要增量 OpenAPI/SDK、Goose 迁移、授权回归与外部 Adapter 合同；外部 Host 无法通过 Qual 自动恢复进程内状态，需从持久事实重试与验证副作用。性能、成本和准确性只能通过真实试点测量。
- **生产仍 Pending**：OIDC/TLS/远程 MCP OAuth、Agent OS 沙箱、正式运维/HA、第二个独立低权限 Human 亲自参加的多人试点以及需专项执行的原生无障碍测试，不会因为本 ADR 的 Accepted 自动转成通过。
