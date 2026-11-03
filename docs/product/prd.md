# Qual 产品需求文档（PRD）

> **状态**：Draft v0.1（2026-10-30）。本文件定义设计目标；不是已上线功能。

## 1. 产品定位

Qual 为**人类与外部 AI Agent 共同使用**的工程任务、缺陷、测试与证据管理系统。长期价值是**可追溯工作事实**，而非托管执行能力。

- **谁使用**：同时使用人工开发、编码 Agent、外部 CI/测试工具的小团队；初期优先单组织/多项目，支持本地自部署。
- **核心工作**：创建并领取工作、协调状态与依赖、记录测试定义与运行结果、归集缺陷与修复证据、跨会话继续工作。
- **产品假设**：统一的领域模型 + 结构化 Agent API + 可审计历史，比把 Issue/测试报告/Agent 上下文分散在不同工具更有效。该假设须通过真实试点验证，非已证实市场需求。

## 2. 非目标（硬边界）

Qual **不**：启动/调度/编排 Agent；控制 Agent 生命周期；调用 LLM；保管推理轨迹或 Prompt；提供自动测试执行器；触发 CI 作业；替代 Git 仓库；成为通用聊天工具；在 MVP 里做复杂企业流程、计费、时间跟踪或路线图。

所有“测试执行”均指**登记外部执行事实**或人类手动录入。所有“Agent 领取任务”仅是对任务负责方字段的事务性更新，不能导致 Qual 启动 Agent。

## 3. 人物与权限

| 角色 | 典型行为 | 权限边界 |
| --- | --- | --- |
| Workspace Admin（Human） | 管理成员、身份、项目、策略 | Workspace 内 |
| Project Member（Human/Agent） | 创建/编辑/审核任务、管理用例与外部测试事实 | Phase 8 的凭证 Scope + 项目 Role + Allow/Deny/Inherit |
| Agent Principal | 查询上下文、领取/更新任务、追加证据和测试结果；经明确授权可审核工作项、只读审计、归档或管理 Webhook | 逐项目显式授权，scope + membership + deny 优先；不运行 Agent |
| Integration Principal | 上传 CI/test report、建立外部引用 | 受限写入目标资源 |

`actor_principal_id`（谁做了操作）与 `assignee_principal_id`（谁负责当前处理）独立；`accountable_owner_id`（最终责任人）可按项目策略要求是 Human。受保护的审核/完成动作可配置为只能由指定人执行。

## 4. 核心对象

- Workspace → Project → Workflow / WorkItem / TestSuite。
- WorkItem 类型至少 `task`、`bug`（后续可加 `story`），共享编号、看板、优先级、状态、负责人、标签、评论、依赖；Bug 附加严重程度、重现步骤、预期/实际行为、环境。
- TestCase → 不可变 TestCaseVersion；TestSuite 是分类/集合；TestRun 绑定项目、执行来源和版本化的用例项；每个 TestResult Attempt 是不可变事实。
- Artifact 关联对象存储的截图、日志、报告；ExternalReference 引用 Git commit、PR、CI run；Activity 是用户可读变更流，AuditRecord 是持久审计事实。
- 关联边可以表达 `verifies`、`caused_by`、`blocks`、`duplicates`、`fixed_by` 等显式关系。必须避免无约束的泛化关系。

## 5. MVP P0 用户旅程与可验收行为

### J1 工作项协作

1. 人在 Web 项目内创建任务，填写标题、描述、验收标准、优先级。
2. 外部 Agent 根据授权范围 `search/get-context` 检索可领取工作项。
3. Agent 通过原子 `claim` 领取；并发竞争仅一方成功，失败者得到确定的冲突错误。
4. Agent 追加评论/证据、更新状态；系统审计记录 actor、来源、版本和差异。
5. 人在 Web 审查并完成任务；若状态受保护，Agent 不允许直接完成。

**验收**：同一业务规则在 REST/MCP/CLI 上一致；错误返回代码明确；跨会话重读上下文不需要依赖 Agent 会话。

### J2 测试失败与缺陷闭环

1. 人创建测试用例，发布 Version 1；编辑后发布 Version 2，旧版本不被改写。
2. 外部测试器或人创建 TestRun，绑定当次用例版本。
3. 追加 Failed Attempt 和截图/报告；基于失败结果创建或关联 Bug。
4. 人/Agent 修复并关联 Git PR/Commit，外部工具上报后续 Passed Attempt。
5. 查看 Bug 时能反查失败与复测的历史证据，不能把失败覆盖为通过。

**验收**：任何 TestRun 仍可查看绑定时的用例规范；同一失败可指向已有 Bug；Passed 不会删除 Failed；外部声明与可信验证来源可区分。

### J3 长期查询与导出

1. Agent B 通过 `get_context` 获得工作项现状、验收标准、阻塞关系、相关测试和最近决策。
2. 人从活动时间线查清谁在何时修改状态。
3. 授权用户导出项目实体、历史及附件清单，附带 checksum Manifest。

**验收**：完整性可检验、分页可恢复、访问范围正确；导出不得越过项目权限。

## 6. MVP 核心功能（优先级 P0）

此处 P0 表示产品优先级，不等于实施路线图的 Phase 0（文档初始化）。

- Workspace 与 Project；身份/成员/Agent token、基础 RBAC。
- WorkItem：CRUD、类型 Task/Bug、状态机、乐观锁、原子领取、依赖、评论、搜索/过滤、看板/列表、审计。
- TestCase：用例库、版本发布、Suite；TestRun、结果 Attempt、关联缺陷、附件证据。
- REST v1 + OpenAPI；至少覆盖主要任务/测试写入的 MCP Tools；Go CLI 的核心读写与 JSON 输出。
- Web：项目切换、工作项看板/列表/详情、测试用例库/运行、缺陷详情、活动历史、基础集成凭证。
- 归档：MVP 导出 JSONL/Manifest 与附件清单；完整可恢复导入可后置。
- 易用性：可复制工作项稳定编号、明确数据来源与操作身份；危险操作确认。

## 7. 非 P0 / 后续

高级报表、路线图、定制状态工作流编辑器、Webhook 重试控制台、SSO/SAML、Git/CI 深度双向同步、全文/语义搜索服务、细粒度可视化 RBAC、批量迁移工具、多语言支持、组织计费与配额。与 Agent 调度相关能力始终在非目标中，不列为后续功能。

## 8. 产品与质量指标（试点目标，不作市场事实）

- J1/J2/J3 三项端到端场景都能由实际使用者完成。
- 对同一 WorkItem 的并发 Claim/Update 无丢失更新。
- 所有业务写请求都产生关联 Actor 与版本的审计数据；导出只包含授权数据。
- 测试运行及用例版本在历史中不可被后来编辑污染。
- 新 Agent 可仅凭授权 API 恢复上一 Agent 已记录的任务上下文。
- 试点测量：任务首次记录耗时、状态恢复耗时、失败到缺陷建立耗时、追溯证据完整度；不预设指标已经达成。

## 9. 待确认的产品决策

- 第一阶段是否仅部署单 Workspace，数据库保持未来多 Workspace 的隔离能力（建议是）。
- 受保护状态与 Accountable Owner 是否在所有项目强制（建议支持项目策略，默认有 Human 负责终态）。
- 首版测试报告导入从 JUnit XML 还是 Playwright JSON 开始（建议先 JUnit XML）。
- 是否需要在第一批试点对接 GitHub/GitLab 的只读 PR 关联（建议先通用 URL 外部引用）。

待确认事项不阻塞文档基线和独立模块的实现，但对应模块开始之前需转为 ADR 决策。
