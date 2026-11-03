# ADR-0022：显式 Review Policy、Owner Acceptance 与原子 Done Gate

> **实际落地更新（2026-11-01）**：QUAL-21 Stage C 已由真实独立Human `reviewer:passed/self_review=false` 并 `done v16`；QUAL-24已实际将**默认Qual数据库迁移到Goose13**，受审新Server/CLI/MCP34/React在线运行，真实ReviewPolicy与Acceptance API/旧Done legacy_unclassified受限接口已在Human与PI会话核验。默认仍为 `independent_review`，Owner模式默认未开放，真实Human无独立Reviewer的Done请求被拒绝、WorkItem保持 `in_review`；此文以下旧文“Proposed / 未部署”是历史研发阶段信息，不能用作当前版本判断。仍不代表全局产品/生产安全认证。最新记录见 [默认Goose13真实上线报告](../pilot/qual24-default-goose13-live-upgrade-2026-11-01.md)。

- 状态：**Proposed — QUAL-21 A/B/C 工程已实现并通过隔离 PostgreSQL13/Goose12 回归，`in_review v14` 等待独立 Human 业务验收**（2026-11-01）。仓库已提交 Goose13 SQL、显式 `/complete` 原子事务、Project 与 WorkItem 级 Review Policy、真实 Acceptance GET（旧 Done 为 `legacy_unclassified`）、CLI/MCP34/React、项目 ZIP 四类治理归档；另修复 Done 后评论历史读取和**治理记录存在时拒绝破坏性 Goose13 Down**。**默认 `qual-self/QUAL` 仍为 Goose12，未迁移、未启用新完成门槛，也未得到独立 Human 签收。** 源码/隔离测试不能替代正式上线。阶段 B 历史见 [早期技术验证](../pilot/qual21-goose13-review-gate-stageb-2026-11-01.md)，最新见 [阶段 C/迁移 Human 交接](../pilot/qual21-24-goose13-operator-release-handoff-2026-11-01.md)。
- 背景：真实 QUAL-2 `done v13`、QUAL-3 `done v10` 均由有权的真实 Human Owner 完成，但没有 `reviewer` RoleEntry；其 `created_by` 是该 Owner。两次操作已有真实 `work_item.transition`，不能事后伪造 `reviewer:passed`、补写 `self_review=true` 或反向否认既有 Done。QUAL-4 的历史 `reviewer:passed/self_review=true` 与后续 needs_work 也不能当独立签收。当前仅凭 `work:review` 可将 in_review 转 done，缺独立审核/Owner 明示风险接受的结构化门槛。

## 2026-11-01 后续方向：Agent 自治，Human 可介入

Qual 将逐步以**被授权的 Agent 自主推进和签收**为默认工作方式，Human 保留操作、撤权和风险介入能力，不再要求逐项 Human 决断。无需第三套审批系统，复用 Goose13 的 `owner_acceptance`：无单独 Accountable Owner 时，真实认领的 Human **或 Agent** 可作为负责人签收。只有具备真实 `work:review` Credential Scope、项目 Allow、有效 Owner 策略并附实际证据和风险说明，才能通过原子 Done Gate。`self_review=true` 必须如实记录为非独立决策，不应自动阻止项目明确允许的自审。明确高风险和要求独立审核的工作项继续遵守独立门禁。

QUAL-25 修复的是之前遗漏的服务端 `DelegatedOwner` 派生，并以隔离 Goose13 PostgreSQL 真实 Agent HTTP 测试验证，不新增表或 REST/MCP 工具。**现有 QUAL 的默认政策和原 PI Token 权限不会因代码升级自动放宽**：以后项目管理员可一次性授权可信 Agent 的审核 Scope/Project Allow 和 Owner 策略，随后无需 Human 逐单签收。

## 决定与边界

保持**同一真实 Principal** 及应用授权事实。支持两种互斥、服务端验证的**完成决策模式**：

- `independent_review`：真正有 `work:review` 的认证主体发起显式完成，服务端从**真实不可变** Reviewer RoleEntry 库取最近一条结论。必须 `outcome=passed`、`self_review=false`、`work_status=in_review`，Review 工作版本不早于最后一次**实质变更**（内容/验收/有效依赖/证据绑定）的版本；关联真实且可验证的项目证据，并检查当前 WorkItem 仍为 `in_review`。不能因旧 Passed、后来 needs_work/failed、评论“已通过”、单 Agent Executor/Tester 或仅凭 work:review 变更为 Done。
- `owner_acceptance`：当前真实 Accountable Owner（或在项目策略里被明确指定的负责人）可以在持有 **`work:review`** 的情况下主动选择“自审签收 / 非独立批准”，声明不少于规定长度的风险接受原因、审核范围、真实项目证据（TestRun/Ready Artifact 或明确的外部 Git 核验引用），并作出显式确认。服务端从 AuthN Actor、项目策略、当前 WorkItem 读取身份和证据，**不接受客户端提供 actor_id、self_review、credential_id、policy_source**。不产生伪装为独立审核的 `reviewer:passed`；追加独立的不可变 `work_acceptance_decisions` 记录，其 `acceptance_mode=owner_acceptance`、`independent=false`、Actor、WorkItem 版本、风险说明和来源可审计。历史显示必须直称 Owner 自审签收。

## 持久化与配置（阶段 C 工程已实现；未部署到默认 Qual）

1. 新增版本化 `qual.project_review_policies`：`project_id`、`default_mode=independent_review`、`owner_acceptance_allowed`、`force_independent_for_high_risk`、`version`、`updated_by`。默认先最小授权；高风险默认强制独立，不能因角色模板或 Token Scope 自动放宽。
2. 允许项目级 `access:manage` 的真正已授权 Principal 在业务 REST 入口以 CAS+Idempotency 更改普通策略，禁止改自己权限/通过离线 SQL 直接绕过审计。特别敏感的 Owner 签收放宽和全局强制策略变更需要 Human Workspace Admin 或未来显式委托策略授权；本地当前低权 PI Agent 不得操作。项目可按 WorkItem 显式覆盖 `effective_review_mode`，但不能突破项目 `force_independent` 或禁止 Owner 的上界；覆盖来源、版本及变更者可追溯。
3. `qual.work_acceptance_decisions` 是**只追加**的历史：Project/WorkItem/Actor/Credential、决定模式、Owner 签收确认、明确原因、引用与摘要、`work_version_before/after`、项目政策版本、Reviewer Entry ID（独立模式）、日期和 Request ID；同一业务事务写 WorkItem Done、Decision、Audit、Activity、Outbox 和 Idempotency Response。DB Trigger 拒绝历史 UPDATE/DELETE。源 TestRun/Artifact 需在当前项目并可读/ready。
4. 旧版已 Done WorkItem/RoleEntry **不做回填、不改状态**。新迁移后可在 API/UI 读取历史 `acceptance_mode=legacy_unclassified`（**只读展示标签，不能用作新的完成模式**），仅说明当时有权 Human 执行合法状态变更但未记录结构化签收方式。旧未完成工作项须在首次新签收时显式选择合法模式；新工作项缺省独立审核。上线前由有权 Human 确认业务迁移窗口，不能将已有 Owner 自审完成偷偷重标为独立审核。
5. 有权 Human 对 Owner 自审 / 高风险独立策略的判断属于明确政策选择；**现有项目正式切换前，先以隔离 PostgreSQL 证明迁移、授权和历史兼容**。若迁移未部署到默认实例，则旧 `done` 行为依然如历史，不宣称 Done Gate 已运行。

## 原子完成接口（阶段 C 已接入 CLI/MCP/React；运行中的客户端及默认 DB 尚未切换）

- 新推荐 `POST /api/v1/projects/{projectID}/work-items/{itemID}/complete`，需 `Idempotency-Key`、`expected_version`、`acceptance_mode`、签收理由、确认标志和限定类型的证据引用。拒绝未知字段。若 WorkItem/Project policy 无可接受方式则明确返回不可完成原因。迁移后普通 `/transitions` 的 `status=done` 应与**同一事务** Done Gate 共用 Application UseCase，不能旁路；未带显式决策时 fail-closed。
- `GET .../review-policy` 与 `GET .../acceptances` 需与 Owner/Agent 真实权限一致；CLI/MCP/Web 透出相同有效策略来源、可完成原因及 Owner 非独立警告。客户端 UI 不能仅显示绿色 `passed` 或根据 `self_review` 推断授权。
- 服务端先检查 Actor/credential、Membership/Scope/Project Allow，再在持有 WorkItem 行锁和项目策略版本快照的事务内检查 CAS、最新 Reviewer 决策、最近实质变更版本及证据，最后一次性写所有记录。重复同一 Idempotency Key 只回原结果；同键不同 payload 409；跨 Project 404、无权 403、过期 CAS 409；任何拒绝都不得留下部分 RoleEntry/Decision/Audit/Outbox。

## 技术分段与验收

- **阶段 A（本次启动）**：只交付纯领域 Done Gate 判定与负面测试、ADR；绝不接管线上 transition 或修改真实 Qual 数据。标记 `QUAL-21 in_progress`，不能用单测证明事务级 Done Gate 已部署。
- **阶段 B（已在一次性隔离 PostgreSQL 实测，默认库未迁移）**：Goose13 Review Policy/Acceptance/Change History、不可变 Trigger、项目级 GET/PUT Policy、显式 `/complete` Service/PG 原子事务和旧 `/transitions done` 防绕过、CAS/幂等/Outbox、Go Race 跨项目/并发/Owner/独立 Reviewer 负面测试均已提交。**仍未交付工作项级 override 管理 API、验收历史读取及全量项目 ZIP 持久归档路径**；这些归入阶段 C。
- **阶段 C（工程实现已完成，Human 业务签核仍 Pending）**：OpenAPI/SDK、REST/CLI/MCP34 和 React 详情显式 Owner/独立模式、WorkItem 级策略覆盖来源、​历史 `legacy_unclassified`、真实 Project ZIP 签收事实；PG13 -race 8/8 与 PG12 兼容测试已通过，真实 QUAL-TC-15 v3/QUAL-TC-19 v2 外部 Attempt/Ready S3 已归档。默认运行服务仍为 Goose12 阶段 B 前二进制；正式 Human 浏览器/VoiceOver/400% 全流程、授权策略审批及 Operator 备份/迁移发布未完成，不能标记 Accepted。

## 不可改变的 Qual 产品边界

Qual 永远**不启动或编排 Agent**，不替 CI/测试工具执行测量，也不生成虚假 Reviewer、Agent 身份或测试 Attempt。Owner 自审是带独立模式和风险理由的真实用户操作，不会被包装成 `self_review=false` 的独立签核。未经有权管理员的真实策略变更，不可向旧 PI Agent 自动赋予 `work:review`、`audit:read` 或 `access:manage`。
