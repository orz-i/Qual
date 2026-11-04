# QUAL-34 · Attention Inbox 服务端只读投影（局部实施证据）

**基线**：本机 `main 72af1b7`；仅隔离 `feature/qual34-35-attention` 分支。默认受管理的 Goose17 服务不重启、不新增持久迁移、不变更正式权限。此记录是代码与隔离测试阶段，不是正式 Human 独立验收或已部署证明。

## 最小实现边界

- 新增只读 `GET /api/v1/projects/{projectID}/attention?limit=30&offset=0`，沿用 WorkItem 的真正 `work:read` 权限（跨 Project 404；无授权 403；未登录 401），不给出原始 Audit、私有 Artifact、执行或审批动作。
- 复用已有 PostgreSQL `work_items`、Reviewer `work_role_entries`、AgentRun 和已登记 ExternalEvent；不增加 SQL 表或状态机。当前 Reviewer 的 `needs_work/failed`、`in_review`、真正重新提交、独立 `passed` 按既有 ReviewQueue 意义排序；`self_review=true` 不等于独立通过。
- 外部 Run `blocked`、`running` 的 stale/unknown 为 **hint_only** 线索；stale 根据 Qual **服务端收到的** last_seen 与已有 AgentRun 门限判断，不把源时间当作权威。Event 提示仅认可 `needs_input` 或 `attention.required` 字面类型，在最近 7 天且 WorkItem 自收到后未修改时出现，既不代表真实性证明也不自动改变工作项。
- 每个工作项仅显示一条最高优先级线索，保留 real `source_id` 和 `observed_at`；外部发生时间独立标识 `source_at`。总响应上限 50、offset 0..450，按优先级/服务端时间/UUID 稳定排序，明确 offset 在并发写入下**不是原子快照**，封顶报告 `paging_limited`。
- 不增加 Worker、Agent 调度或“自动核验”；已存在的 ReviewQueue 和 WorkItem 审核继续是唯一权威业务动作。

## 本轮实际执行结果

- Go 后端 `go test ./...`、`go vet ./...`、受限 HTTP Server 构建 **PASS**。
- OpenAPI lint **PASS**；已生成并核对 TS SDK 同步变更。
- 新增 `TestQual34AttentionScopedDeduplicatedReadOnly`，在新建的、限定命名 `qual32_run_ci_*` Goose17 PostgreSQL 中真实运行 **PASS**：Reviewer、Run blocked/stale/unknown、Event allowlist、超过时效 Event、同工作项多个来源去重、原 ReviewQueue 不变、前后 Audit 记录不增加、分页、撤权主体与跨 Project 401/403/404、无界查询 400。
- 受保护综合脚本 `scripts/tests/ci_goose17_guarded.sh`（带显式开发测试 S3 环境）真实运行 **18 项顶层 PASS，1 项已声明历史预期 SKIP**：Context 3、Run/Archive/Attention 5、Done Gate 9、历史 Goose12 1；四个专门创建的临时 DB 均已清理，残留 0。
- 第一次本机运行综合脚本未传 S3 环境，旧 Context 附件测试确实失败（HTTP 503），之后使用真实开发测试 S3 重跑并通过；**保留失败原因，不冒称一次通过**。另一次新增集成夹具使用了早于 Thread 创建的合成收到时间，已纠正且在新暂存数据库通过；非正式业务数据。
- 未测试：真实默认服务部署、生产大容量性能压测、恶意 Operator 跨主机鉴权、真实公网 GitHub App 来源。QUAL-34 保留 `In Progress`，必须在前端接入后按当前真实权限和发布基线验收。

## 回退

此 PR 是加法只读端点与 SDK：关闭 attention 路由或回退新增包即恢复之前 ReviewQueue 读取，不删改任何不可变来源数据。既有 Goose17 禁止破坏性 Down；当前无新增迁移。
