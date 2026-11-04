# QUAL-37 · 跨进程恢复、Thread/Event、AgentRun、Attention 与证据联测

**范围**：2026-11-04 Mac Pi 本机隔离执行。不是公网生产真实 GitHub webhook、不同真实 Human 独立安全签收或正式部署。

受试源码位于独立 `feature/qual37-cross-context-e2e` 工作树，它组合尚未合并的公开 Draft PR #3（QUAL-31/33）和 PR #4（QUAL-34/35），只用于兼容性与真实持久事实联测。原 main 和默认 Goose17 二进制均未修改。

## 新增跨上下文集成

`apps/server/integration/phase21_qual37_e2e_test.go` 中的 `TestQual37CrossContextResumeAndEvidence` 必须使用新建的 `qual32_run_ci_*` Goose17 PostgreSQL 与真实开发测试 S3。数据库名称不符合隔离前缀时测试跳过；从不将真实默认 qual 数据库作为目标。

单一真实隔离 WorkItem 中执行并断言：

- 有版本的 WorkItem `todo→in_progress`、CAS Thread Link；未获 event:write/run:report 的普通 work:write Agent 实际收到 403，必须明确测试 Credential Scope 和 Project Allow 后才能追加。
- Event 首次记录及不同请求键重试均保留同一个来源事实；同 External ID 不同 payload SHA 返回 409，来源文本不会自动写入 Review 或 WorkItem。
- 合成 AgentRun blocked 优先覆盖相同 WorkItem 的 event_needs_input 线索，Attention 仍仅返回一个 hint_only 项，原 Audit 无权 403，跨 Project 404。
- 真实 TestCaseVersion、TestRun、先 Failed 后 Passed 的不可变 Attempt，真实 S3 Ready Artifact 和 SHA 验证；篡改对象后下载返回 HTTP422，原有数据可恢复。
- 构建并启动真正独立 Qual CLI 进程两次读取 resume：第一次 WorkItem v4 和两个历史 Attempt；合法评论增加版本至 v5 后第二个新进程回读 v5 且历史仍在；旧版本写入 CAS409。
- 真实同一 Human Principal 对自己创建的合成工作项写 self_review=true（并非独立批准）；测试结束前撤销 Agent Credential，新的 CLI resume 与 Run 写操作被拒绝。全部外部报告未修改原 WorkItem in_progress 状态。

## 真实执行回执

- 专项独立 PostgreSQL `qual32_run_ci_1793774604_1` 真 Goose17 集成测试：`go test -race -tags=integration -count=1 -v -run '^TestQual37CrossContextResumeAndEvidence$' ./integration` → **PASS**，测试数据库按保护流程删除。
- 受保护总脚本 `scripts/tests/ci_goose17_guarded.sh` 在单独隔离的四组 Goose17/12 PG，携带真正本机开发 S3：**19 个顶层 PASS、1 个预期历史 SKIP、4 组临时数据库残留 0**。
- 同一组合分支的 Go `go test ./...` 和 `go vet ./...`，OpenAPI contract/TS SDK、前端 TypeScript/Lint/Build 均实际通过，**33/33 Node 单元回归 Passed**。

旧 QUAL-35 的 320/390px 合成浏览器结果与本次独立；本次没有伪报真实 Human 登录态 Chromium E2E、真实 GitHub App 签名来源、不同 OS 安全主体或生产大容量/恢复验收。QUAL-37 不得仅凭以上局部 Passed 自动标为 Done。

## 交付边界

本工作仅新增跨上下文测试文件，并将其纳入已存在的受保护 Goose17 组；未新增生产 API、权限、SQL 表、执行器或密钥。独立测试分支先保留 PR #3/#4 的本地组合，正式合并须待依赖 PR 分别审核；不把 PR 的 CI 绿色当成独立安全认可。
