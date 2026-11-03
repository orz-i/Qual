# ADR-0005：追加审计 + Transactional Outbox

- **状态**：Accepted（Phase 2 事务性 Audit/Activity/Outbox，Phase 5 HMAC Webhook Delivery/重试/Dead 已实现；仅显式 Worker 投递）
- **日期**：2026-10-30

## Context
工作项、失败结果与归档需要长期追溯；Webhook 推送存在进程崩溃/重试风险。

## Decision
采用常规业务状态表 + `audit_records`、`activity_records` 的追加记录；业务写入、必要审计和 `outbox_events` 在一个 PostgreSQL 事务内提交。Phase 5 已通过独立 `qual-maintenance deliver-once|deliver-loop` 实现 Outbox Worker，按项目的 allowlisted HTTPS 订阅投递 HMAC-SHA256 事件，租约/退避/五次 Dead 并记录结果；消费端依据 Event/Delivery ID 做幂等。**Worker 由管理员显式启动，不由业务事务自动唤起，更不启动 Agent 或 CI。**不采用全量 Event Sourcing 或 Kafka 起步。

## Consequences
查询与开发相对简单，审计可追溯。业务状态本身不是从事件流重放；需要确保审计字段覆盖重要变更，并制定备份、数据保留与脱敏策略。

## Alternatives
纯 Event Sourcing 对业务团队的事件演化/回放要求过高；事务内直接发送 Webhook 无法保证失败后的可靠交付。
