# GitHub → Qual 外部 Adapter（QUAL-36，隔离候选）

这个 Go 程序由外部宿主运行，**不是 Qual 内置 Agent 执行器，也不授予任何 WorkItem/Review 权限**。其功能只有：接受 GitHub 签名 Webhook → HMAC-SHA256 验证 → 只保留可核查的事件元数据 → 先写 owner-only 持久队列 → 使用经另行批准的 Integration Credential 上报 Goose17 ExternalEvent。

## 约束

- 默认只监听 `127.0.0.1:18717`，**不会**开启公网端口，也不会修改 Qual 正式运行进程。GitHub 无法直接访问本机 loopback；公开 Webhook 的 TLS 终结、通道保护、IP 规则和 GitHub App 安装由**另行审查**的外部入口提供，不能把现有 `127.0.0.1:18080` Qual API 暴露公网。
- 配置 `QUAL_GITHUB_REPO_ID` 必须匹配 GitHub 仓库**固定数字 ID**。单单同名 owner/repo 不足以获权；可额外指定 GitHub App `QUAL_GITHUB_INSTALLATION_ID` 约束 App 安装身份。
- `QUAL_GITHUB_WEBHOOK_SECRET` 为独立高熵 HMAC Secret，最少 32 字节，由有权 Operator 在进程环境安全注入；不能和 GitHub CLI OAuth、Qual PI PAT、Qual Operator 签名 HMAC 共用。
- 使用 GitHub `X-Hub-Signature-256` 的**原始请求字节**验签，常量时间比较。只有 HMAC、仓库数字身份、事件类型/Action、时间/编号全部合法才在 0700 的专用目录写入 0600 JSON。**绝不保存** webhook raw body、issue 标题、评论、用户消息、URL 等不可执行可信指令之外的任意文本。仅保存 `source=github`、`owner/repo/pull/N` 或 `owner/repo/issues/N`、发生时间、原始 payload SHA-256、固定格式短摘要和审计状态。
- 同一个 GitHub `X-GitHub-Delivery` UUID、相同内容的重投返回 202（已有记录）；同 UUID 但内容变更返回 409。持久队列最多 1,000 条 Pending 和 10,000 条 Delivered，超出拒绝 503，**不承诺自动接收 GitHub 重试**。GitHub 投递失败须在 GitHub 后台查看、人工或自动 redelivery；不能把 503 视为已经收到。
- 只有单独创建并由 Human 授予 Qual `event:write` Credential Scope **和 Project Allow** 的 Integration Principal 可上报。**不要修改当前 PI/人类用户的凭据**。Connector 不含 `work:write`、`work:review`、`run:report`，不会通过 GitHub Event 自动更改 WorkItem、创建 Thread/Link、推进状态或记录 Agent Run。
- Adapter 队列及关联配置的**父目录必须由运行该进程的 OS 用户拥有，且不能被 Group/Other 写入**；队列目录 0700、记录/映射文件 0600。仅保护子目录是不够的，父目录可写会允许替换攻击。
- `QUAL_GITHUB_MAPPING_FILE` 为由 Human 预先审核并设置 0600 的 JSON，明确将受信任的 `owner/repo/pull/N` 或 `owner/repo/issues/N` → 已存在同 Project WorkItem UUID + Thread UUID 关联；不得按标题猜测。没有映射只在本地保留 `blocked_mapping`，向 Qual 的 POST 次数为零。**一个 GitHub Delivery ID 最多对应一个 WorkItem**；真正多 WorkItem 场景需独立设计来源指派，不要伪造第二个 Delivery ID。
- `once` 为显式一次性批次（可由外部 Operator 作业调度），不是内置 Agent 或后台自治执行引擎。401/403 保留 `blocked_auth`，已确认目标关联不存在的 404 保留 `blocked_mapping`，这些错误**不会自动重复请求**，必须由有权维护者核对权限/关联并通过 `requeue --approve --reason-code <code>` 明确恢复；尚未建立本地批准映射的事件也保留 `blocked_mapping`，在映射文件得到有效批准后才首次上报。409 保留 `conflict` 并隔离，不提供自动重排；5xx/网络故障指数退避，六次后 `manual_review`；同样需要人工审核再明确重排。所有历史 Attempts 和 Event ID 保留在持久队列。所有状态和已有 Event ID 保留在持久队列。
- `QUAL_API_URL` 被严格限制为 `http://127.0.0.1:<非特权端口>`；HTTP 客户端**不自动跟随重定向**，不能将 Integration Bearer 发送到非 loopback 域名。

## 本机验证入口

构建与 Race：

```sh
cd integrations/github-adapter
go test -race -count=1 ./...
go vet ./...
go build -o /tmp/qual-github-adapter ./cmd/qual-github-adapter
```

Adapter 本机配置示例（全部为占位符，**不要**将真实 Secret/Token 写入 Git）：

```sh
export QUAL_GITHUB_REPO_ID=1443998164
export QUAL_GITHUB_REPOSITORY=orz-i/Qual
export QUAL_GITHUB_SPOOL_DIR=/private/owner-only/qual-github-spool
export QUAL_GITHUB_MAPPING_FILE=/private/owner-only/approved-links.json
export QUAL_API_URL=http://127.0.0.1:18080
# 由可信 Operator 安全注入独立的 QUAL_GITHUB_WEBHOOK_SECRET；
# 显式向 Qual 上报时另外注入仅含 event:write 的 QUAL_ADAPTER_TOKEN。
qual-github-adapter inspect
qual-github-adapter serve
# 验签与落盘由 serve 完成；只有经过权限和映射独立配置后：
qual-github-adapter once
# Only after reviewing and resolving the incident:
qual-github-adapter requeue '<github-delivery-uuid>' --approve --reason-code auth-restored
```

已审核的关联文件示例（只是**假 UUID 示例，无法指向真实 WorkItem**）：

```json
{
  "repository_id": 1443998164,
  "repository_name": "orz-i/Qual",
  "links": {
    "orz-i/Qual/pull/1": {
      "project_id": "33333333-3333-4333-8333-333333333333",
      "work_item_id": "44444444-4444-4444-8444-444444444444",
      "thread_id": "55555555-5555-4555-8555-555555555555"
    }
  }
}
```

## 已验证与尚未验证

单独 Go module 的真实 `go test -race -count=1 ./...`、`go vet ./...` 验证了 GitHub 官方 HMAC 测试向量、签名篡改与恶意仓库拒绝、256 KiB 入站上限、重复投递、进程重启后队列/已投递幂等、父目录不可由他人写入、符号链接替换保护及持久来源元数据不可改写、401/403、404、409、503 重试与六次失败转人工、重定向禁止和伪造 201 响应拒绝。出站单元测试使用隔离 HTTP 测试服务器和合成凭据。

此外，`apps/server/integration/phase20_github_adapter_real_transport_test.go` 已在**全新、独立 Goose17 PostgreSQL 数据库和实际测试 S3** 中通过：从 Go 代码真实编译并启动外部 Adapter 子进程，向它注入本地合成 HMAC 签名的 `orz-i/Qual/pull/1`（公开 PR #1 对应来源 ID，**但不是 GitHub 实际投递**）；确认缺映射不提交 Qual、Integration 未获授权403；仅在测试数据库的 Synthetic Integration Principal 获取明确 `event:write` Scope+Project Allow 后才登记一条真实 Goose17 ExternalEvent。独立的合成外部 PI 随后实际 Claim WorkItem、注册并去重 AgentRun、把实际测试传输结果上报为 Run 状态，完成真实 S3 Ready Artifact 及 TestRun/Attempt/Coverage。旧版本 CAS409、重复 Webhook 不追加 Event/Audit、凭据撤销401、新 Event 不再写入以及 Qual 不自行推进 Done 均已断言。测试完成时删除**仅此合成实例**产生的 S3 对象，不碰正式项目数据。

公开 CI 的 `ci_goose17_guarded.sh` 分别使用专属 `qual30_event_*` 和 `qual36_adapter_*` Goose17 测试库执行 Event 核心及独立 Adapter 端到端两条回归，共计 19 项受保护的真实测试（默认/正式 `qual` 永不运行合成测试）。本机真实运行 `go test -race -tags integration -run '^TestQual36ExternalAdapterSignedSyntheticPRToRealGoose17PI$' -count=1` 已 Passed；这是**隔离合成端到端测试**，不冒充真实 GitHub App 交付、真实 PI 会话或公网安全验收。

**未完成**：真实 GitHub App 安装、真实 Webhook Secret/Ingress 和来自 GitHub 的实际 Delivery、人工审核的真实 WorkItem–Thread 映射、专用真实 Integration Principal 的最小授权，以及一个真实 GitHub Delivery → Qual Event → 外部 PI Run/Test/Evidence 的运营闭环。需按 QUAL-36/37 后续验证，不能因为隔离合成测试通过就把工作项 Done。


## 独立审查后的安全整改（PR #1，2026-11-03）

- **签名重放边界**：GitHub HMAC 只覆盖 Body，不覆盖 X-GitHub-Delivery。持久队列在跨进程锁内检查整个原始 Body 的 SHA-256，若相同已签名 Body 被带不同 Delivery UUID 投递，只应得到 202/new=false，不得创建第二条来源事实或消耗更多队列配额。对同一个 UUID 变更了 Body 的情形仍拒绝 409。
- **崩溃恢复**：对 pending 与 delivered 中的同 Delivery ID 重叠记录，以已同步的 Delivered 为权威，重启时校验元数据并清理残留 Pending；若数据相互冲突则拒绝恢复。首次 HTTP POST 前持久固定已审批的 Project/WorkItem/Thread UUID，既有事件不得随映射修改静默跨项目重发。保留已有 Event ID。
- **人工重试协议与能力边界**：requeue 要求 --approve 与 --reason-code，允许 auth-restored（blocked_auth）、link-repaired（blocked_mapping）或 transient-verified（manual_review）。已有失败状态、HTTP 状态、原因码、时间及执行者 OS UID 按顺序持久留存（最多 24 个状态变更，超限拒绝写入）。这里仍是同一个受信任 OS 账户的操作约定，**不具备独立 Human Principal 的强制鉴权或 OS 不可篡改审计**；Human 与 PI 共享 macOS UID 时不能据此宣称独立安全审批。
- **容量可见性但尚无安全回收**：inspect 输出 capacity 的 pending/delivered 用量、上限、80% 的 near_limit 及满载 ingress_blocked。容量仍严格限制为 1,000 个 Pending 与 10,000 个 Delivered，满载新入站 503。尚无安全保留墓碑和去重语义的已投递记录归档/回收，不得手动直接删除 Delivered 并继续声称重放安全；本机 Adapter 不能据此宣称长期无人值守生产就绪。
- **PR 校验可信度**：质量门禁改由受保护目标分支的 pull_request_target 工作流运行，绝不检出/执行 PR Head 代码；它只在此规则已经存在于默认分支之后才真正生效。首次引入该工作流的 PR #1 不受该新门禁的自我保护，仍需独立审查。
