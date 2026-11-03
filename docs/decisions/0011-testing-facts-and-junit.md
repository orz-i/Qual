# ADR-0011：Phase 4 版本化测试事实与受限 JUnit 导入

- **状态**：Accepted for Phase 4 implementation
- **日期**：2026-10-31

## Context

Qual 不执行测试，也不控制 CI。Case 的规范更新不可改写已有 TestRun 的测试依据；失败与复测须同时保留。常见外部工具输出 JUnit XML，但测试框架之间不存在统一、全面的 JUnit XML 标识语义。

## Decision

- TestCase 的当前 draft 可编辑（乐观版本）；每次 Publish 在事务中生成含标题、前置、步骤、预期的规范快照、SHA-256 和严格递增的 TestCaseVersion（附 source_revision）。发布后由数据库拒绝 UPDATE/DELETE；同一 draft revision 不允许无变化重复发布。
- TestSuite 首期采用单层项目内集合；TestCase 可选归属 Suite，可关联多个现有 WorkItem（需求覆盖关系）。
- TestRun 仅是某一来源（Human/Agent/Integration）登记的容器。创建时明确指定一组同项目的已发布 CaseVersion；RunItem 永久绑定具体版本/checksum。Run/RunItem 不能用于执行测试。
- TestAttempt 对每个 RunItem 只追加记录：pass/fail/blocked/skipped、观察时间（由上报方提供或系统接收时间）、接收时间、来源 Principal、文字结果、原始来源标识、可选外部结果 ID；唯一 attempt_no 由数据库锁定 RunItem 生成，永不覆盖。
- 失败 Attempt 可关联已有的同项目 Bug，也可**事务性创建 Bug 并关联失败**；不得自动将失败等同于 Bug；Bug/Attempt 多对多。后续 passed Attempt 不删除既有 failed。
- 全部写入与 Audit/Activity/Outbox、Idempotency 在一个 PostgreSQL 事务中保存。Outbox 不自动执行测试，不调用 Agent。
- JUnit XML 首版只接受上报到已有 TestRun：每个 testcase 需 `properties/property name="qual.case_key"` 或 name 本身为稳定的用例 key，并且该用例必须是 RunItem 中固定的用例。未知/未绑定/重复标识拒绝整份导入，不自动生成 TestCase。<failure> / <error> -> failed，<skipped> -> skipped，其他 -> passed；不从任意 stdout/stderr 生成可信执行证据。
- 报告限 1 MiB、最多 500 cases；禁止 DTD/DOCTYPE、非预期根节点、重复 key 和不可信路径；原始 XML、SHA-256、上报方和时间保留于 PostgreSQL `test_report_imports`（**仅作为 Phase 4 小报告存档**）。Phase 5 再迁移到有完整权限/校验/保留流程的 S3 Artifact 机制。
- 身份 Scope 新增 `test:read` 和 `test:write`。Integration Principal 可以被赋予项目 test:write 以登记报告，但不能修改任务、批准审核或提升权限。
- REST 是权威服务接口；Web/CLI/MCP 后续测试工具均复用业务规则，不允许隐藏的“测试执行”功能。

## Consequences

明确区分测试规范、测试运行登记、测试上报来源和验证结果的历史记录；数据量随 Attempt 累加，需要索引、分页和归档保留政策。JUnit 需要显式 Qual key 对照，目前不做自动匹配与迁移。Phase 4 数据库原文存档限小文件；后续需独立可恢复的附件存储。

## Alternatives

- 复用 WorkItem 卡片表示 Case/Attempt：无法表达不可变测试历史，排除。
- 覆盖 RunItem 最新结果：破坏失败留证，排除。
- JUnit 导入遇到陌生用例自动创建正式用例：会伪造人工审核的规范，排除。
- 直接执行本地/CI 测试：违反 Qual 的非执行/非编排边界。
