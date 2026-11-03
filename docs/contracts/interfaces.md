# REST / MCP / CLI / Plugin 接口设计

> **当前状态**：Phase 2–4 的工作项、测试与本机 Human/Agent 接入已完成；Phase 5 的**S3 Artifact、ExternalReference、Project Export ZIP/Manifest、HMAC/Outbox Webhook、Go CLI 和 4 个证据 MCP Tools**也已在本机实现。真实 REST 接口以 [OpenAPI 3.1](../../packages/contracts/openapi.yaml) 为准；Phase 9 在运行中仍为 stdio MCP 28 Tools；QUAL-21 Stage C 候选源码已增至34 Tools（审核策略+签收），需正式发布/重载后才在线。**尚未实现**生产 Human OIDC、远程 HTTP MCP OAuth、恢复导入、对象锁/扫描/完整生产运维，故不提供公共 `/mcp` Endpoint。

## 1. 核心原则

1. **统一 Application Service**：Web/REST/MCP/CLI 使用完全相同的业务命令、权限、冲突策略和审计写入。
2. **API-first**：OpenAPI 定义 REST 契约，生成 TS SDK、测试用客户端与 Go Handler 的一致性校验；MCP Tool 显式映射到 Use Case，而不是复制 JSON Endpoint。
3. **安全与版本**：`/api/v1` 稳定，破坏性变更需 v2/弃用窗口；`/mcp` 依据协议版本协商，不能混同 API version。
4. **明确对外 ID**：资源带内部 UUID `id` 和人类可读 `key`（例如 `ENG-123`），REST path 首期使用 UUID，CLI 可以解析 key。跨 Workspace 不能仅靠 Key 查询。
5. **准确记录来源**：每次写入以服务端识别的 ActorContext 为准；`reported_by` 表示上报主体，而非 Qual 对结果的背书。

## 2. Phase 2 已实现 REST 与后续资源提案

### 当前已实现：受保护工作项

Phase 2 的 Workspace、Project、Principal、Credential 通过离线 `go run ./cmd/qual-admin` 管理。**尚没有** Workspace/Identity 的公共管理 REST、Human OIDC/Session 以及公开 MCP OAuth。

| Method | 已实现路径 | 行为 |
| --- | --- | --- |
| GET/POST | `/api/v1/projects/{projectID}/work-items` | 项目作用域内列表/创建 Task 或 Bug，列表支持 status/kind/limit/cursor |
| GET/PATCH | `/api/v1/projects/{projectID}/work-items/{itemID}` | 详情/基础字段更新，PATCH 必须有 ETag 版本 |
| POST | `/api/v1/projects/{projectID}/work-items/{itemID}/claim` | 原子领取，需 expected_version |
| POST | `/api/v1/projects/{projectID}/work-items/{itemID}/transitions` | 合法状态迁移；当前 Goose12 仍按历史 `work:review` 做 Done，迁移到 Goose13 后该路径 Done 返回422，必须调用带明确模式/有效证据的 `/complete` |
| GET/POST | `/api/v1/projects/{projectID}/work-items/{itemID}/comments` | 读取最近 100 条评论/追加评论 |
| GET/POST | `/api/v1/projects/{projectID}/work-items/{itemID}/dependencies` | 查询/建立项目内无环阻塞关系 |
| GET | `/api/v1/projects/{projectID}/activity` | 按项目读取最近 Activity |
| GET | `/api/v1/projects/{projectID}/audit` | 需要 audit:read 的审计元数据 |

POST 使用作用域幂等键，PATCH 使用 `If-Match: "N"`；无凭证 401，Project 隔离 404，角色/Scope 不足 403，版本或幂等冲突 409，非法转换/环 422。工作项 `context` 投影仍属于 Phase 3 提案。

**QUAL-21 Stage C 候选已实现但默认 Goose12 尚未实际发布**：`GET|PUT /api/v1/projects/{projectID}/review-policy`、`GET|PUT /work-items/{itemID}/review-policy`、`GET /work-items/{itemID}/acceptances`、`POST /work-items/{itemID}/complete`，服务端执行 CAS+Idempotency+独立 Reviewer/Owner 真实权限、审计和旧 Done `legacy_unclassified`。Operator 需参考 [Goose13 受控迁移手册](../pilot/qual21-24-goose13-operator-release-handoff-2026-11-01.md)。

### 后续计划：Workspace 管理 REST；测试/结果接口现以 OpenAPI 实现为准

下表部分路径是**早期设计草案**，与 Phase 4 实际 Project-scoped 路由未必相同，务必以真实 OpenAPI 为准；高级统计、归档等仍未实现：

### 测试 / 结果 / 缺陷关联

| Method | Path | 行为 |
| --- | --- | --- |
| GET/POST | `/api/v1/projects/{projectId}/test-cases` | 用例列表/创建 |
| GET/PATCH | `/api/v1/test-cases/{id}` | 当前草稿或详情，PATCH 带 If-Match |
| POST | `/api/v1/test-cases/{id}/versions` | 发布不可变版本 |
| GET | `/api/v1/test-cases/{id}/versions/{versionNo}` | 历史版本 |
| GET/POST | `/api/v1/projects/{projectId}/test-runs` | 查询/登记运行 |
| GET | `/api/v1/test-runs/{id}` | 运行与 RunItem |
| POST | `/api/v1/test-run-items/{id}/attempts` | 追加 Passed/Failed/Blocked/Skipped |
| POST | `/api/v1/test-attempts/{id}/defects` | 创建/关联现有 Bug |
| POST | `/api/v1/artifacts/uploads` | 请求附件上传许可 |
| POST | `/api/v1/artifacts/{id}/complete` | 提交校验并关联目标 |

### 活动 / 导出

| Method | Path | 行为 |
| --- | --- | --- |
| GET | `/api/v1/projects/{projectId}/activity` | 按游标读取项目动态 |
| GET | `/api/v1/projects/{projectId}/audit` | 授权审计查询 |
| POST | `/api/v1/projects/{projectId}/exports` | 创建可校验导出（异步存档任务，不是 Agent 调度） |
| GET | `/api/v1/exports/{id}` | 获取导出状态、文件 Manifest |
| GET | `/api/v1/projects/{projectId}/external-references` | 查询外部 PR/CI 引用 |

大规模归档任务是普通服务端批处理，不负责启动 Agent。

## 3. 统一 HTTP 语义

- 授权：`Authorization: Bearer <token>` 适用于 CLI/服务集成；Web 可能采用安全 Cookie，具体依身份 ADR。URL 不带 token。
- `Idempotency-Key`：关键 POST 写操作必需，作用域 `principal + project + operation`，相同键不同请求体返回 409。
- `ETag: "8"` 与 `If-Match: "8"`：PATCH 使用；MCP/CLI 可使用 `expected_version: 8`。缺失前置条件可返回 428。
- `request_id`：每次请求返回并进入审计；客户端可传 `X-Request-ID` 提议值，但服务端限制长度与格式。
- List：`limit`、`cursor`、`next_cursor`，并限定最大页容量，Cursor 对 scope/filter 绑定。
- Time：UTC ISO 8601；不接受用户随意指定 `created_at` 来覆盖服务端时钟。
- 错误：统一 `code`、`message`、`request_id`、`details`，不要泄漏数据库 SQL 或跨项目资源存在性。

```json
{
  "error": {
    "code": "VERSION_CONFLICT",
    "message": "Work item has changed; reload before retrying.",
    "request_id": "req-example",
    "details": {
      "expected_version": 8,
      "current_version": 9
    }
  }
}
```

| HTTP | 错误代码 | 说明 |
| --- | --- | --- |
| 400 | VALIDATION_ERROR | 请求字段非法 |
| 401 | UNAUTHENTICATED | 缺凭证或认证失败 |
| 403 | PERMISSION_DENIED | 身份无权执行 |
| 404 | NOT_FOUND | 不存在或避免泄露的非授权对象 |
| 409 | VERSION_CONFLICT / ALREADY_CLAIMED / IDEMPOTENCY_CONFLICT | 并发冲突 |
| 422 | INVALID_TRANSITION / DOMAIN_INVARIANT | 领域规则不允许 |
| 428 | PRECONDITION_REQUIRED | 缺版本条件 |
| 429 | RATE_LIMITED | 受限 |
| 503 | DEPENDENCY_UNAVAILABLE | 依赖不可用，允许安全重试 |

MCP 工具错误需保留可机器识别的 `code` 与可恢复提示，不能把所有错误转成无结构字符串。

## 4. WorkItem Context Response

`get_context` 的目标是跨 Agent 会话**重建工作事实**，不是生成摘要或保管聊天历史。响应应有 schema、独立 revision、字段白名单、长度上限和增量水位。

```json
{
  "schema_version": 1,
  "work_item": {
    "id": "uuid-example",
    "key": "ENG-123",
    "title": "修复登录过期错误",
    "status": "in_progress",
    "version": 8,
    "acceptance_criteria": ["令牌过期有明确错误"]
  },
  "blockers": [],
  "related_test_case_versions": ["ENG-TC-12@3"],
  "latest_decision": "保留既有接口",
  "next_actions": ["补充回归测试"],
  "context_revision": 27
}
```

`latest_decision`、`next_actions` 只有来自明确标注的结构化评论/决定事项时才会出现；**不得从任意自然语言历史自行捏造结论**。大字段按 `include` 查询参数按需拉取，使用 `since_revision` 获取变更。版本与时间均来源于数据库。

## 5. MCP Adapter 设计

**当前已实现**：Model Context Protocol 官方 Go SDK v1.8.0，独立 `qual-mcp` stdio 进程通过 scoped PAT 调用受保护 REST（不接触数据库凭证），共 23 个 Tools：9 个工作项、10 个测试管理、4 个证据和外部引用工具。完整名称、CLI 对照与安装边界见 [Agent Skill](../../integrations/agent-skills/qual/SKILL.md)。

**尚未实现**：公开远程 `POST /mcp` Streamable HTTP / OAuth 2.1 / Protected Resource Metadata / 令牌 audience 校验；远程客户端不得以当前 PAT/stdin 方案宣称标准 OAuth 兼容。

| MCP Tool | Use Case | Scope |
| --- | --- | --- |
| `work_item_search` | SearchWorkItems | work:read |
| `work_item_get` | GetWorkItem | work:read |
| `work_item_get_context` | GetWorkContext | work:read |
| `work_item_create` | CreateWorkItem | work:write |
| `work_item_claim` | ClaimWorkItem | work:claim |
| `work_item_update` | UpdateWorkItem | work:write |
| `work_item_transition` | TransitionWorkItem | work:write |
| `work_item_add_comment` | AddComment | work:write |
| `test_case_get` | GetTestCase/Version | test:read |
| `test_run_create` | CreateTestRun | test:write |
| `test_result_record` | RecordTestAttempt | test:write |
| `defect_create_from_failure` | CreateOrLinkBug | work:write + test:read |
| `activity_search` | QueryActivity | work:read |
| `artifact_search` / `artifact_get` | ReadAuthorizedArtifactMetadata | artifact:read |
| `external_reference_search` | ListExternalReferences | work:read 或 test:read |
| `external_reference_create` | RecordExternalReference | work:write 或 test:write |

当前工具按业务域分组；MCP Resources、Prompts 和公开远程 OAuth 都仍为后续可选增强，不能据此声称当前服务已经实现。不使用 MCP 协议 Tasks 扩展来模拟领域任务，也不使用 Sampling/Agent 调用链来执行工作。

远程 OAuth 授权与旧规范兼容矩阵见 [Security](../architecture/security.md)。受控环境的 PAT 集成应与标准远程 OAuth 支持明确标识，不能把二者混为一谈。

## 6. CLI

CLI 是简单 API 客户端，不直接连接 PostgreSQL；本地安全保存凭证，`--json` 输出稳定机器格式，其他输出面向终端。计划命令：

```bash
qual auth login
qual project list --json
qual item list --project ENG --ready --json
qual item show ENG-123 --context --json
qual item claim ENG-123 --expected-version 8
qual item transition ENG-123 in_review --expected-version 9
qual test case show ENG-TC-12 --version 3 --json
qual test attempt add --run RUN-42 --case ENG-TC-12 --status failed
```

CLI key 的解析必须先确定 Workspace/Project，避免同名误操作；批量操作默认 dry-run 可后置。退出码：`0` 成功、`2` 校验、`3` 授权、`4` 冲突、`5` 服务错误（实施时再正式固定）。

## 7. Agent Skills / Plugin

首先发布单一 `integrations/agent-skills/qual/SKILL.md` 说明允许的 Tool/CLI、认证、工作项领取/更新及结果上报步骤。不同宿主如 Codex、Claude Code、Cursor 只做适配包装，**不嵌入执行编排**。Skill 只能建议调用外部 Qual 接口，不得给 Agent 额外权限。

## 8. 外部导入与 Webhooks

- 首选 JUnit XML 作为报告导入，安全解析，限制文件大小/实体扩展，映射 `run_item`、`attempt` 与来源；保留原始报告 Artifact。
- Webhook 只作为已提交事实的事件通知，事件包括 `work_item.transitioned`、`test_attempt.recorded`、`defect.linked` 等；HMAC 签名、事件 ID、重试可重复、消费者幂等。
- Outbox 重试不是 Agent 调度。Webhook 不触发 Qual 内部的代码执行、测试运行或 Agent 任务。
