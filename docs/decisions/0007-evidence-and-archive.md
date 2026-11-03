# ADR-0007：不可变测试证据与可验证归档

- **状态**：Accepted（Phase 4 测试不可变历史 + Phase 5 S3/ZIP/Manifest 已本地实现；恢复导入尚未实现）
- **日期**：2026-10-30

## Context
Agent 可能跨会话退出，测试用例可能在失败后被编辑，外部 CI 的执行结果需要长期可追溯。简单“只保留最新状态”不足以审计和复盘。

## Decision
发布的 TestCaseVersion 不可编辑；TestRunItem 永久绑定指定版本；TestAttempt 只追加。Evidence Artifact 保存原始 bytes（对象存储）和 SHA-256、大小、来源与关联。项目导出采用版本化 Schema + JSONL + Artifact 清单 + SHA-256 Manifest，按授权范围与一致性水位生成。

## Consequences
长期存储成本增加，读取需要 latest projection；归档须规定保留、清理、脱敏和完整性检查。外部系统上报的通过只被记录为声明，不由 Qual 保证真实执行。

## Alternatives
覆盖 TestResult 最新值会抹掉失败历史；仅用外链不能验证历史附件未变化；全量数据库 SQL dump 不适合稳定的跨版本归档格式。
