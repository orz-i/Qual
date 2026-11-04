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
# Explicit, bounded operator-only compaction of successfully Delivered receipts:
qual-github-adapter inspect
qual-github-adapter archive 200 --approve
qual-github-adapter inspect
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


## 2026-11-04 · 已投递记录的安全热容量归档（非删除）

2026-11-03 的初始整改仅提供了队列用量预警；现在增加显式可信本机 Operator 命令
qual-github-adapter archive 1..500 --approve。归档后 inspect.capacity.archived_used 显示历史已归档条数，
delivered_used 仅计仍占用热目录的记录，可以通过受控归档恢复热队列接收能力。

- 只能移动已经真实收到 Qual HTTP 201 且具有已验证 Qual Event UUID 的 Delivered 记录。不会移动 Pending、Blocked、Retry、Conflict，也不会调用 Qual API 或更改任一 WorkItem。
- 0700 的 archived/ 以原 Delivery ID 保存原始脱敏完成回执；0700 的 archive-index/ 以原始已签名 Body 的 SHA-256 作为索引，保存一致的完整回执副本；两处均为 owner-only 0600。不会存入原始 GitHub Payload、Issue/PR 标题或 Token。
- 单个记录依次执行“持久化 SHA 索引/目录 fsync → 从 Delivered 原子移动到 Archived → 两个目录 fsync”，崩溃时按同一不可变原回执安全恢复。旧 Pending 和归档索引的重叠崩溃也必须先协调；内容不匹配或索引丢失即拒绝启动，不会通过丢失 Tombstone 让旧签名再次入站。
- 入站 X-GitHub-Delivery 和原始 Body SHA-256 去重继续覆盖 Pending + 热 Delivered + 已归档 Archived。任意重放不能因归档变成新 Qual Event。新事件仍必须经过有效 GitHub HMAC、仓库数字 ID、Allowlisted Action 与真实 Qual 项目授权。
- 归档是热目录容量恢复，不是安全删除或长期磁盘容量解决方案：历史身份/内容索引均永久留存，磁盘消耗随真实 GitHub Event 单调增长。需要另行配置可信独立备份、磁盘监测、恢复演练和已授权外部调度；绝不直接清空 Archived 或 archive-index 来回收空间。--approve 依旧是同 macOS UID 下的 Operator 约定，不具独立 Human 审批。

完整故障模型、验证入口及未满足的生产前提见 [已投递安全归档设计和验证](../../docs/security/pr1-safe-delivered-archive-2026-11-04.md)。


## QUAL-50 受控本机磁盘与离线签名恢复（叠加变更）

- 新增磁盘可用空间水位/总容量可见性：低于 64 MiB 可用空间时新的 Spool 写入 fail closed 503；物理磁盘使用 >=80% 或可用空间 <=256 MiB 时 inspect 的 disk_near_limit 为真。不可对旧有效重放虚构新条目或丢失既有 Ready Event。
- 增加显式本机 Operator backup --out <fresh-absolute.zip> --approve 和 restore --file <signed.zip> --into <new-directory> --approve；只使用由可信 Operator **独立安全注入**的 QUAL_GITHUB_BACKUP_HMAC_KEY，不得复用 GitHub Webhook/Qual PI/Human/Operator PostgreSQL HMAC Key。签名为 HMAC-SHA256 完整性，**不加密**备份。恢复只到不存在的新目录，不自动替换正式 Adapter、授权或启动外部 PI。
- 只备份已脱敏的 Pending/Delivered/Archived/ArchiveIndex；不会复制原始第三方 Webhook Body 或秘密凭据。备份/恢复都会失败关闭而不毁损原有身份与失败历史；官方 Human-only Key 的真实部署、可信外部备份与隔离恢复演练仍 Pending。
- [QUAL-50 磁盘保护](../../docs/security/qual50-adapter-disk-watermark-2026-11-04.md) 与 [安全签名归档/隔离恢复](../../docs/security/qual50-signed-spool-backup-2026-11-04.md) 分别记录实际测试、性能和安全边界。


### 2026-11-04 PR #2 技术对抗复核补丁

- 已核实原 Restore 使用先 Lstat 再普通 Rename，在底层 rename 系统调用前存在较窄目标目录竞争窗口。现在 Darwin 调用 RENAME_EXCL、Linux 调用 RENAME_NOREPLACE，原子拒绝已存在的目录、普通文件或符号链接；不支持的平台必须 fail closed。Do not conflate Go os.Rename's preliminary directory check with an atomic nonreplacement guarantee.
- 备份签名验证不代表快照新鲜度。恢复成功输出 snapshot_freshness_verified=false 与 requires_source_reconciliation=true。使用早于某次 Webhook 投递的有效签名快照恢复，会丢失晚于备份产生的去重墓碑；恢复前后不能仅凭 HMAC 就安全切换接收器。实际已通过合成 HMAC 对抗测试复现，仍是正式上线阻断项。
- 详细证据、修复验证与未解决风险参见 [PR #2 对抗复核](../../docs/security/pr2-atomic-restore-audit-2026-11-04.md)。


### QUAL-51：已签名旧快照默认恢复隔离（不可当作完整生产恢复）

从本阶段候选代码起，RestoreSignedBackup 在对外发布之前为每个恢复队列持久写入双份 owner-only 0600 恢复标记，强制 Quarantined 状态；一个标记缺失、权限错误或内容不匹配，OpenSpool 会拒绝。受控 inspect 可查看 recovery 和 capacity.ingress_blocked，serve/once、Enqueue/PinTarget/Update、人工重排和归档都会 fail closed。即使新的操作员将已恢复数据再次备份并恢复，它也仍然被隔离。本地无 --approve 绕过开关，不接触 Human-only HMAC。

此外，新建的 Spool 现在必须持久保存双份绑定根目录路径的 `active-source-origin*.json`，只有 OpenSpool 首次创建真正不存在的目录才可生成。未知旧 Spool 不能自动取得新来源身份；恢复签名 ZIP 不携带 ACTIVE 见证。即使意外**同时删除两份 quarantine**，没有 ACTIVE 见证的已恢复快照仍不能被 OpenSpool/serve/once/Enqueue 当作新来源，从而避免一种 fail-open 恢复路径。迁移旧合法目录必须受控保留并核对完整事实，不可通过清空历史重建 Spool。隔离 Go Race 49 条和真实临时 Goose17/12 专项19条已通过。

这仍不是完整的可信来源账本对账；同 OS UID 可伪造/删改本地见证、旧版二进制可忽略它。生产激活前必须完成由不同真实授权主体维护的完整来源事实水位／独立核验和部署隔离。详见 [QUAL-51 恢复隔离设计与剩余阻断项](../../docs/security/qual51-restored-snapshot-quarantine-2026-11-04.md) 及 [新鲜 Spool 双来源标记门禁](../../docs/security/qual51-origin-witness-deny-by-default-2026-11-04.md)。

### QUAL-51：三方签名来源对账只读验证层（未具备可信生产签发服务）

新增独立 Go 库接口 `VerifyRestoredSourceEvidence`，要求三个分别由外部受信任公钥验证的 Ed25519 规范签名声明：连续的 GitHub 来源事件账本、对应的 Qual Event/Project/WorkItem/Thread 证明、以及绑定恢复快照 SHA、前两份账本哈希和最新来源水位的 Operator 围栏。密钥必须由真正独立的安全域固定；**本 Adapter 不生成生产签名、没有内置来源账本生产者，也不根据本地证明发放恢复授权**。

只读检查在恢复队列双锁内进行：发现 T0 旧备份缺失 T1 来源事实、Qual Event 错配、来源序列缺口、重复已签 Body、过期水位或伪造签名时拒绝或标记不一致。即使所有提交的证明一一对应，返回值也始终是 `activation_authorized=false`、`latest_source_externally_proven=false`、`requires_independent_review=true`；恢复队列继续 Quarantined，绝不启动 `serve`/`once`。

源码上限是从第一个来源事实开始连续的 10,000 个事件和总计 4 MiB 的规范证据；它不能单独承担无限历史生产账本或认证不同真实 Human/OS 主体。真实来源签发、Qual 可信导出、外部最新水位和受权激活**均未实现**。本地仅使用随机测试 Ed25519 私钥验证实现与安全负例。协议和发布阻断详见 [QUAL-51 独立签名来源对账的工程协议](../../docs/security/qual51-independent-signed-reconciliation-2026-11-03.md)。
