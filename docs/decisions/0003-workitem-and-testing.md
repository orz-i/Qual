# ADR-0003：统一 WorkItem，独立 TestCase/Run/Attempt

- **状态**：Accepted（WorkItem Phase 2、TestCase/Run/Attempt Phase 4 已实施并在本机验证）
- **日期**：2026-10-30

## Context
Task 与 Bug 共享状态、看板、活动、负责人，但测试用例是可复用规范，运行记录与结果是历史事实，生命周期截然不同。

## Decision
Task 与 Bug 作为 `WorkItem.type`，Bug 独有字段放 `BugDetails` 扩展；TestCase 身份和可编辑草稿分离，不可变 TestCaseVersion；TestRun 绑定版本化 RunItem；TestAttempt 追加不可覆盖。失败 Attempt 与 Bug 通过多对多关联，不自动创建 Bug。

## Consequences
减少重复的工作项 CRUD，同时保留测试历史不变性；需要明确关联约束、Bug 扩展字段校验以及版本发布事务。

## Alternatives
“所有对象都变成 WorkItem 卡片”会破坏测试语义；“Task/Bug 完全独立两套聚合”会重复工作流与审计代码；“测试结果覆盖最新值”使追溯不可用。
