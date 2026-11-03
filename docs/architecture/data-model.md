# 数据模型、证据与归档

> **Phase 9 当前物理模型（Goose 12）**：在 Phase 5–7 的身份/工作/测试/证据/出站事件表之上，新增 `00011_project_permissions.sql` 与 `00012_work_role_entries.sql`：逐项目 Principal Allow/Deny/Inherit 与乐观锁版本表 `project_permission_overrides`，追加式 `project_permission_changes`，所有业务变更同事务 Audit/Activity/Outbox；迁移不放大旧 Token 的能力。Phase 9 增加 `work_role_entries` append-only 同 Principal 多职责履历、真实 Actor/Credential、WorkItem 版本、外部 Git/TestRun/Artifact 引用与 `self_review`；写入同事务 Audit/Activity/Outbox，并纳入项目 ZIP。以下仅保留部分长期设计表草案。历史 Phase 5 已实现证据、归档与出站事件模型：身份/工作项 `00002`–`00003`；本机 Browser Session `00004`；测试规范与追加结果 `00005`–`00006`；私有 S3 Artifact 元数据/ExternalReference/ZIP Export `00007`；Webhook Subscription/Delivery `00008`；不可变维护日志与过期 Pending/Uploaded 清理 `00009`–`00010`。真实 PostgreSQL + SeaweedFS 集成测试通过。下表仍是**全产品目标模型**，部分字段只是后续设计；实际表与约束以 `apps/server/migrations/` SQL 为准。恢复导入和长期备份/保留流程尚未实现。

## 1. 主键与租户隔离

- 内部主键统一 UUID；人类可读 Key（如 `ENG-123`、`ENG-TC-12`）是稳定显示编号，不作为跨域引用的唯一物理主键。
- 所有项目数据都有 `workspace_id`、`project_id`（或可经不可变父键解析）；必要的复合外键与查询条件共同防止跨租户串读。
- `created_at/updated_at` 使用 UTC timestamp with time zone；测试报告另存原始 `observed_at` 与 `ingested_at`。
- 关键实体有 `version bigint` 用于乐观锁；唯一约束按业务场景确定，不以显示字符串代替 ID。

## 2. 建议核心关系表（含部分已实现，非完整 DDL）

| 表 | 核心列 | 关键约束 |
| --- | --- | --- |
| `workspaces` | id, slug, name, created_at | unique(slug) |
| `projects` | id, workspace_id, key, name, workflow_id | unique(workspace_id, key) |
| `principals` | id, workspace_id, type, display_name, disabled_at | type in human/agent/integration |
| `memberships` | workspace_id, project_id?, principal_id, role | scope/role 不得超越 Workspace |
| `project_permission_overrides` | workspace_id, project_id, principal_id, permission, effect, version | Phase 8 已实现；unique(project_id, principal_id, permission)，CAS，allow/deny/inherit |
| `project_permission_changes` | project_id, principal_id, permission, before/after_effect, old/new_version, changed_by, request_id, reason | Phase 8 已实现；只追加，trigger 禁止 update/delete，事务性审计 |
| `credentials` | id, principal_id, secret_hash, scope, expires_at, revoked_at | secret 不落明文 |
| `workflow_states` / `workflow_transitions` | workflow_id, state/edge, guard | unique workflow/state/edge |
| `work_items` | id, workspace_id, project_id, key, type, title, description, status, priority, assignee_id, accountable_owner_id, version | unique(workspace_id,key), version >= 1 |
| `bug_details` | work_item_id, severity, reproduce, expected, actual, environment | PK/FK work_item_id，检查 type=bug 由业务层保障 |
| `work_comments` | id, work_item_id, author_id, content, created_at, version | 追加/修订可审计 |
| `work_dependencies` | from_id, to_id, type, created_at | unique(from,to,type), from != to；业务层检测跨图环 |
| `test_suites` | id, project_id, parent_id, title | 目录自引用避免环 |
| `test_cases` | id, project_id, key, draft_jsonb, published_version_id, version | unique(project_id,key) |
| `test_case_versions` | id, case_id, version_no, payload_jsonb, checksum, published_at | unique(case_id,version_no)，发布后不可改 |
| `test_suite_cases` | suite_id, case_id, position | unique(suite_id,case_id) |
| `test_runs` | id, project_id, name, source_principal_id, external_run_key, environment, build_ref, started_at, ended_at | 来源去重 scoped project/source |
| `test_run_items` | id, run_id, test_case_id, test_case_version_id, snapshot_hash | unique(run_id, case_id) 默认；允许参数化时扩展 case instance key |
| `test_attempts` | id, run_item_id, attempt_no, status, observed_at, ingested_at, reported_by, provenance_jsonb | unique(run_item_id,attempt_no)，追加记录 |
| `attempt_defects` | attempt_id, bug_work_item_id | unique(attempt_id,bug_work_item_id) |
| `artifacts` | id, project_id, object_key, sha256, size_bytes, mime_type, upload_state | unique(project_id, sha256, object_key) 可优化 |
| `artifact_links` | artifact_id, target_kind, target_id | kind/target 由应用层校验 |
| `external_references` | id, project_id, provider, kind, external_id, url | unique(project_id,provider,kind,external_id) |
| `activity_records` | id, project_id, actor_id, entity_kind, entity_id, payload, occurred_at | 可分页的用户活动投影 |
| `audit_records` | id, workspace_id, project_id, request_id, actor_id, credential_id, command, target_id, old_version, new_version, diff_redacted, occurred_at | append-only；敏感值过滤 |
| `idempotency_records` | project_id, actor_id, operation, request_key, request_hash, result_ref, expires_at | unique(project,actor,operation,key) |
| `outbox_events` | id, event_type, aggregate_id, payload, created_at, attempts, next_retry_at, delivered_at | 事务写入；发送可重试 |
| `webhook_subscriptions` | id, workspace_id, project_id, endpoint, secret_hash, event_types, disabled_at | URL / SSRF 校验 |
| `archive_exports` | id, project_id, requested_by, status, manifest_key, checksum, created_at | scoped Export + lifecycle |

MVP 的 TestRun 包含有限 RunItem，Attempt 历史可持续增长，因此 Attempt 不放入 TestRun 聚合内加载。发布用例版本采用单事务写入 version + test_cases.published_version_id。

## 3. 关键写入模式（伪 SQL）

### 原子领取

```sql
UPDATE work_items
SET assignee_id = :agent_principal_id,
    version = version + 1,
    updated_at = now()
WHERE id = :id
  AND project_id = :authorized_project_id
  AND assignee_id IS NULL
  AND version = :expected_version
  AND status IN ('backlog','todo')
RETURNING id, version;
```

返回 0 行后读取新状态并判定是 404/403/409，不能当作成功。合法可领取状态由 Workflow 策略控制，此 SQL 只是默认工作流示意。

### 追加 Attempt

在同一个事务内，锁定 RunItem 或用唯一索引保证 `attempt_no` 原子递增；同时验证 CaseVersion 与 RunItem 绑定、Actor Scope、幂等键唯一；插入 Attempt、Activity/Audit、Outbox。外部重放相同报告必须得到同一逻辑结果。

## 4. 推荐索引

- `work_items(project_id,status,priority,updated_at DESC)`、`(project_id,assignee_id,status)`、`(workspace_id,key)`。
- `test_case_versions(case_id,version_no DESC)`、`test_run_items(run_id)`、`test_attempts(run_item_id,attempt_no DESC)`。
- `audit_records(project_id,occurred_at DESC,id)`，`activity_records(project_id,occurred_at DESC,id)`。
- `external_references(provider,external_id)`；项目级外部引用唯一约束。
- 全文搜索采用 PostgreSQL tsvector 或 trigram，不能直接依赖全表 ILIKE；JSONB GIN 仅对明确查询模式建立。

索引以 `EXPLAIN ANALYZE` 和实际负载验证，不为不存在的访问模式提前添加过多索引。

## 5. Artifact 上传与校验

1. 请求上传会先验证 Project 权限，生成随机对象 Key 和短时上传许可；对象本身默认私有。
2. 外部客户端上传后调用 Complete，服务端验证大小、摘要及媒体类型（必要时扫描恶意文件），再写入 `ready` 状态。
3. 数据库 ArtifactMetadata 保存 SHA-256、字节数、内容类型、来源、关联对象；禁止用外部 URL 直接替代受控存储的证据完整性承诺。
4. 上传未完成、校验失败产生 `pending/failed` 状态，后续清理孤儿对象。不能假装跨 PostgreSQL/S3 的原子事务存在。
5. 下载前鉴权并发放短时签名链接或代理传输，不暴露内部 bucket key。

## 6. Audit 与 Activity

Audit 作为强制业务写事件的追加链，存身份、目标、request_id、版本差异与时戳；Activity 是供 UI 的可读投影，可以重新构建。对于隐私数据变更记录字段名和摘要，不保存 token、原始密码或不必要的敏感请求体。

禁止普通业务接口执行 Audit 的 UPDATE/DELETE；保留政策应由独立管理员程序、迁移或合规流程实施，并记录操作。Outbox 可重试并最终失败转人工可观测队列，不等同于 Agent 调度。

## 7. 项目归档协议草案

归档出口包含版本号 `archive_schema_version`、项目 metadata、分实体 JSONL、附件文件列表与 SHA-256 manifest，以及 export timestamp / scope / requested_by。实体 JSONL 必须带稳定 UUID、显示 key、source、时间、版本、跨实体引用。只导出有权限的 Project 数据，按一致性快照或显式水位线生成，不能暗中混合不同数据版本。

示例 Manifest（结构草案）：

```json
{
  "archive_schema_version": 1,
  "project_key": "ENG",
  "exported_at": "2026-10-30T00:00:00Z",
  "files": [
    {"path": "work_items.jsonl", "sha256": "example-placeholder"}
  ]
}
```

checksum 样例是占位，不表示真实生成。恢复/导入、保留期限、增量导出和不可抵赖签名属于后续明确决策；MVP 首先保证导出完整可验证。
