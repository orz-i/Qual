# ADR-0016：单 Principal 多职责的可追溯工作履历

- **状态**：Accepted for controlled local Phase 9 (2026-10-31)
- **背景**：当前只有一个真实 Local PI Agent，用户要求它先承担执行、测试、审核、审计等多个职责，以真实留痕和归档为重点，不依赖同时运行的多个 Agent。
- **决策**：真实 AuthN Principal 永远不变。**职责（capacity）是对单次事实的标注，而不是可伪造的 Agent 身份，也不是权限来源**。只有按项目 Credential Scope + Project Allow/Role 检查通过才可记载相应职责。

## 记录

新增 append-only `qual.work_role_entries`：
- 项目、WorkItem、Actor Principal、Credential ID（仅 DB）、职责 `executor|tester|reviewer|auditor`；
- 结果 `observed|passed|failed|needs_work`、文字说明、外部证据引用（Git SHA、现有 TestRun/Artifact UUID）；
- 写入时的 WorkItem 状态与版本、事件创建时间、请求 ID；
- 服务端计算的 `self_review`（同一 Principal 担任任务 Assignee、在该任务有 executor/tester 历史记录时，或为该任务创建者）。**自我复核可以存在，但不能冒充独立审查。**
- 流程状态继续由 WorkItem 状态机与 `work:review` 权限控制。写 `reviewer:passed` 的职责记录**不自动变更 WorkItem 为 done**。

每次写入在同一个 PostgreSQL 事务完成角色记录、WorkItem CAS Touch（版本 +1）、Audit、Activity、Outbox、Idempotency Response。行禁止 UPDATE/DELETE；GET 列表按时间倒序，最多 100 条，不提供隐式“最后一条覆盖前一条”。重复相同 Idempotency-Key 返回原记录，同键不同请求返回 409。

## 权限

- 读取职责履历：`work:read`；不会列出其他项目的记录。
- `executor` 写入：`work:write`；
- `tester` 写入：`test:write` + `work:read`，**Passed 记录必须提供真实 Git/TestRun/Artifact 来源**，并不等于 Qual 启动过测试；
- `reviewer` 写入：`work:review` + `work:read`；自我复核不是独立审查，是否接受由项目策略和用户决定；
- `auditor` 写入：`audit:read` + `work:write`（读审计不等于可改写项目事实；只读 Agent B 仍然只能读取 Audit，若需写审计意见必须有写入权限）。
- 来源类型仅允许 `none|git_commit|test_run|artifact`。Git SHA 只作外部引用，不代表 Qual 验证仓库内容；TestRun/Artifact UUID 必须位于同一项目并实际存在。
- 历史记录由服务端认证生成真实 `actor_id`、`credential_id` 和 `self_review`，客户端不得传入 Actor 或这些服务器字段。任何授权失败都不得生成事件。

## 应用

- REST: `GET|POST /api/v1/projects/{projectID}/work-items/{itemID}/role-entries`。
- Go CLI: `qual role list|record -project QUAL -item UUID ...`；PI MCP: `work_role_entries_search` 和 `work_role_entry_record`；Web 工作项详情展示履历与自我复核标记。
- 单个真实 PI Agent 先作为 Executor 实现、Tester 记录真实测试，再由拥有真正 `work:review` 的同 Principal / Human 添加 Review 记录。若仍没有该 Scope，不伪装 Reviewer，而在 Comment/Executor 日志注明待审。
- Qual 不承担 Agent 编排、工作流调度、测试执行或模型调用。角色留痕是工程事实存档，不能声称存在多个独立 Agent/审核人。

## 风险

- 同 Principal 自己审核自己的结果可能降低独立质量控制。所有复核都明确 `self_review`，高风险项目可在后续启用独立 Reviewer 策略，当前不自动批准。
- 同机 PI 与 Human 共享 Unix UID/Docker 时，不构成主机级隔离，应用 Scope 和职责记录不代表 OS 沙箱。
- 项目 ZIP 跨实例 Import、生产 OIDC/远程 OAuth/HA 仍未实现；此方案不改变上述非目标。
