# ADR-0026：Project 内 ExternalThread 与 WorkItem 多对多关联

- **状态**：Accepted for QUAL-29 implementation（仅源码与迁移设计，不代表默认数据库已部署）
- **日期**：2026-11-03
- **工作项**：QUAL-29（依赖 QUAL-26 / ADR-0025）；外部 Event 属于 QUAL-30
- **沿用**：[ADR-0012](0012-phase5-artifacts-exports-webhooks.md)、[ADR-0025](0025-qual-superagent-collaboration-boundary.md)、[Super-agent 分期契约](../architecture/superagent-collaboration-contract.md)

## 选择及原因

现有 `external_references` 的唯一约束 `(project_id,provider,kind,external_id)` 与单一 `target_kind,target_id` 只能表达一个目标，且 URL 必填。**不改旧表、不改旧唯一键、不根据同名/同 URL 推断旧链接**。Goose14 增加：

1. `external_threads`：`(project_id,provider,thread_kind,external_thread_id)` 唯一、项目复合约束，`id` 不随 URL/标题变化。来源标识 `provider` / `thread_kind` 为小写 ASCII slug；`external_thread_id` 是来源租户/仓库/频道的**完整限定标识**（例如 `org/repo/pull/15`，不允许只有 `15` 混淆仓库）；按 `/` 划分的各段非空、无空白或控制字符，区分大小写。GitHub `pull_request` 固定 `owner/repo/pull/<正整数>`，`issue` 固定 `owner/repo/issues/<正整数>`；其余 Provider 至少包括 `source-scope/thread-id` 两段。可无 URL；提供 URL 时要求 HTTPS、无凭据/查询/片段、无非 443 端口、IPv4/IPv6/内网快捷 IP 禁止，域名须为离线 Public Suffix List 可识别的 **ICANN TLD 下的可注册域名**（包括合法的多级公共后缀及 `team.github.io` 等私有公共后缀之子域）。`.corp`、`.lan`、`.onion`、`.example` 等非 ICANN 顶级域拒绝。此检查只用于展示输入过滤，不做 DNS/HTTP 访问，也不证明域名的真实所有权或外部内容真实性。
2. `work_thread_links`：与真实项目内 WorkItem/Thread 通过 `(project_id,id)` 复合外键关联；记录创建/解除 Principal、时间和可选真实旧 `source_reference_id`。同一对的活动关联唯一；解绑只设置 `removed_by/removed_at`，保留历史，之后可新建关联。所有变化同步写 Audit/Activity/Outbox。
3. 可选 `source_reference_id` 是**显式、逐条确认**的兼容桥接：仅当旧引用同 Project、`target_kind=work_item`、同 WorkItem、`provider/kind/external_id/url` 与新关联一致时接纳。历史引用行不更新，不自动填充、不合成占位 URL。同一个 Thread 可有多个工作项，第二个工作项不能冒用第一条旧引用。

## API 及并发语义

- `POST /api/v1/projects/{projectID}/work-items/{itemID}/threads`：`work:write`；要求 `Idempotency-Key` 和 `expected_version`；按源唯一键查找/创建 Thread，然后创建活动 Link。成功 201 返回 Thread、Link、**新的 WorkItem.version**。不同 URL 争用同一身份 409；已存在活动关联新请求键亦 409。相同 Actor+操作+Key+payload 重放返回存档响应而不重复审计。同 Key 不同 payload 409。
- `DELETE /api/v1/projects/{projectID}/work-items/{itemID}/threads/{threadID}`：`work:write`，要求 `Idempotency-Key`、`If-Match: "<WorkItem.version>"`；软解除并保留行/Actor，200 返回关联历史及新版本；不存在活动关联 404；重放同 Key 返回第一次成功响应。
- `GET /api/v1/projects/{projectID}/work-items/{itemID}/threads`、`GET /api/v1/projects/{projectID}/threads/{threadID}/work-items`：`work:read`，仅活动关联，稳定 UUID 游标 + 有界结果；同 Project 真实目标校验，跨 Project 404、缺权限 403。
- 链接/解绑属于 WorkItem 的实质元数据变更，**原子增加** `work_items.version`、`updated_at`，并以事务行锁校验版本，不改变状态、不自动废止或伪造已记录的 Reviewer/Acceptance。旧版本请求 409。跨 WorkItem 同一 Thread 可并发关联；数据库唯一索引解决来源竞争，不依赖进程内锁。
- 404 隐藏跨项目目标；请求负载不能提供 `created_by` 或特权 Principal。使用现有真实 Agent/Human 凭证授权，不新增 Scope，也不放宽第三方 Integration。
- 无外部事件摄取、Webhook 订阅、Worker、Agent Run 或下载网络流量。Qual 记录来源事实，不担保外部声称的内容真实。

## 发布、历史与回退

采用增量 Goose14，数据迁移**零自动回填**：原 `external_references` 以及其 API/索引不变；旧引用只能经带 `source_reference_id` 的明确关联。新表外键/索引同时生效，POST/DELETE 与 Audit/Activity/Outbox 使用**同一事务**。旧服务在升级数据库后仍能使用旧 API；新入口须待隔离迁移/测试通过后部署。不在默认运行库执行迁移。回退须先关闭新 API，保留 Thread/Link/Audit 历史；Goose14 的 Down 明确拒绝删除，不可破坏性回滚。项目 ZIP 在新表存在时升级为 `archive_schema_version=2`，同时包含 `external_threads.jsonl` 与 `work_thread_links.jsonl` 的全部活动/解除历史，并兼容验证旧 v1 ZIP；不可把旧 ZIP 的缺失关联误解释为不存在的历史事实。

## 必要验证

- 统一域模型校验（跨 Provider Scoped ID、GitHub 严格路径、ICANN PSL 公共域名）与 401/403/404/409/422/428，跨 Project 双侧外键；
- 同线程多目标成功、旧 Reference 明确绑定成功与不匹配失败、URL 不抓取；
- 并发重放/两个工作项同时插入同源 Thread、同 Key 异 payload、WorkItem CAS 冲突；
- 软删除后可重连、历史/审计/Outbox 可读，普通 WorkItem/Reference API 兼容；
- 正式测试/默认库部署状态应分别登记，不能由设计或单元测试自动宣称 Done。
