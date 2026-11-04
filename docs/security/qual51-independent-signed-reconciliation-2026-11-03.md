# QUAL-51 · 三方签名来源账本与恢复隔离只读对账协议（2026-11-03）

> **阶段性质：可编译、可测试的离线证据校验候选。没有建立生产独立来源账本、没有签发可信生产水位、没有独立 Operator 真实审批、没有恢复激活功能。** QUAL-51 必须继续 In Progress；原 QUAL-TC-49 的真实 Failed 历史不被覆盖。

## 为什么必须先做此验证层

GitHub 的 HMAC-SHA256 只保护 Webhook **请求正文**，不签署可由传输层变化的 `X-GitHub-Delivery`。T0 已签名快照可以完全合法，但 T1 后实际接受的新事件在 T0 快照中不存在。对 T0 的 HMAC 认证和本地 Delivered/Archive SHA 索引无法证明 T1 事件已被发现。

QUAL-51 先前的恢复隔离和目录来源见证实现已明确：旧备份只能恢复到**隔离的新目录**；取消两个恢复标记也不会让旧快照成为正常来源；`OpenSpool` 对隔离目录只读检查、绝不自动修补原始事件历史。

**本次增加的是离线的多方签名证据比对能力，不是解锁或恢复完成协议。**

## 只读验证结构

实现：`integrations/github-adapter/githubadapter/source_reconciliation_models.go` 与 `source_reconciliation.go`。

要求三个分别由**不同的受信任公钥**验证的 Ed25519 证据：

1. **独立 GitHub 来源账本**：`SourceLedgerStatement`，包含固定的 GitHub 数字仓库 ID/名称、来源捕获时间、从序号 1 到 `last_sequence` 不留缺口的接收记录。每条来源事实包含递增序号、Delivery UUID、原始已验签正文 SHA-256、经允许的 EventType、该仓库下的 PR/Issue Thread 路径。重复 Delivery ID、同 Body 更换 Delivery UUID、跨仓库来源、非法 Action 或序列缺口一律拒绝。
2. **独立 Qual 事件证明**：`QualLedgerStatement`，包含同一仓库作用域下由另一密钥签署的实际 Event、Body SHA-256、Event UUID、Project/WorkItem/Thread UUID。需要按照 Delivery ID 严格排序、不得重用 Qual Event ID。必须对应来源事实；对已恢复 Delivered/Archived 回执还必须与原 Event ID 和已固定的目标关联完全相符。
3. **独立 Operator 的最新水位围栏**：`ReconciliationFence`，签署固定仓库、已恢复快照 SHA-256、最新来源序号、前两份签名账本的**确切 SHA-256** 以及签发时间。验签时要求三套公钥两两不同、签名及域分离全部正确，围栏来源序号与账本连续长度相同且签发时间不早于账本时间；旧于检查时间 15 分钟的围栏不被接受。

证据的签名均覆盖加上不同协议域标识的**精确规范 JSON**。校验拒绝未知/重复 JSON 字段、非规范字段顺序或序列化、截断签名、错误密钥、跨域代签及不匹配的签名哈希。来源账本每批最多 10,000 条、总证据最多 4 MiB，故当前仅服务受控单仓库试点。未来规模化必须引入经审计的**分片、可信链根、跨分片高水位**，不能任意截掉旧事件后称作完整。

验证对象必须是带有效 `recovery.quarantined=true` 的恢复队列。核验时在 worker/queue 双锁内重读隔离标记，对原 Pending/Delivered/Archived/ArchiveIndex **仅做只读完整性校验**，不会执行启动、删除、归档、发送 Qual POST 或解除隔离。对比签名来源记录、签名 Qual Event、已恢复的所有持久 Delivery，输出缺失/意外/不一致计数。

## 输出中不可被误解的约束

`VerifyRestoredSourceEvidence(...)` 返回的 `AuditOnlyReconciliation`：

- `records_match=false`：即使签名都正确，也已发现缺失的 T1、错配的 Qual Event、被修改的来源事实或旧快照缺少接收记录。**必须继续隔离**。
- `records_match=true`：仅表示**所提交的三份签名证据**与**所检查的恢复快照**数据一致；它并不能证明证据捕获了 GitHub 当前全部真实 Delivery，也不能证明签名服务未回滚。
- `activation_authorized=false`、`recovered_spool_quarantined=true`、`requires_independent_review=true`、`latest_source_externally_proven=false`：**无论比对结果如何，这些值都保持该安全状态**。不存在 `activate`、本地 `--approve` 或同 UID 自签名直接解锁入口。
- 这不是运行在公网的账本服务；生产调用所需的公钥固定和签署服务还没有被安装，也没有使用任何真实 HMAC、PI PAT 或 Human Operator 私钥。本地单元测试中三组 Ed25519 密钥均为随机**合成**，不同密钥不等于不同真人或受隔离 OS 主体。

## 实际执行的安全回归

精确源码基线：内部已审 PR #2 修复 `7d7c431baa8ef0dc6ec5a959b19afa6e5b21120d` 的独立子分支。本次新增源/Qual/Operator 三方签名校验、只读扫描及对抗性测试，未修改 Qual DDD 后端权限或已上线 Goose17 Schema。

实际执行：

- `cd integrations/github-adapter && go test -race -count=1 -v ./...`：**61 项顶层 Go Race PASS，0 SKIP**，含签名和公钥替换、重复字段、同 Payload 换 UUID、序列缺口、过期水位、跨仓库、恢复缺少 T1 以及正确签名仍不解除隔离等负例。
- `cd integrations/github-adapter && go vet ./... && go build ./cmd/qual-github-adapter`：真实静态检查与构建 PASS。
- `scripts/tests/ci_goose17_guarded.sh`：**19/19 项真实隔离 Goose17/Goose12 PostgreSQL + 测试 S3 专项 PASS**，仍验证现有独立 Adapter 子进程及 T0/T1/T2 无恢复重放。测试仅操作脚本新建、强限定名称的临时数据库，临时资源完成后清理；正式默认 `qual` Goose17 没有业务事件写入和权限变化。

上述测试只能证明**实现可按输入执行、拒绝已覆盖的故障样例**；并不是三种真实独立证据来源已经完成或任何实际生产恢复已获授权。

## 下一真正发布门禁（全部未完成）

1. **可信、独立且持久的来源账本生产者**：由 GitHub App 入口验签后，在与 Adapter 不同 OS Principal/宿主或外部不可变服务中、在向 GitHub 响应成功之前持续记录被接受来源事实；可验证不漏签名 Delivery、处理重试与 GitHub 不自动投递失败的差异。当前没有此服务。
2. **独立 Qual Event/Audit 权威导出器**：从真实项目 scoped Event/Audit 以不同凭据和可信宿主取得完整快照，排除漏掉已投递事实；其签名凭据不能与 Adapter/Human 日常 PAT 共用。当前没有此服务。
3. **第三方最新来源围栏与新鲜度证明**：具备不同的真实 Human/Operator 信任边界，读取 GitHub delivery 最新事实和 Qual 写入前后高水位，对 T0 之前、T0 至恢复期间、晚到/乱序、同 Body 不同 UUID、跨事件链签名根进行可验证无缺失证明。单独签名快照或当前 15 分钟时间窗口均不能替代该证明。
4. **独立恢复/激活授权机制**：必须在独立 OS Principal、固定二进制 SHA、可信审计和明确维护窗口下将经过对账的来源恢复为可使用状态；目前**故意没有实现**任何解锁函数或命令，防止一人同 UID 自签假证后切换生产。
5. 外部加密签名备份、容灾高可用、真实 GitHub App/TLS、专用 Integration Credential 和真实 PI 续作、不同真实主体的独立 Reviewer、QUAL-37 端到端安全验收。

**结论：**新增了有界、可复验的只读证据比对协议和对抗性回归；QUAL-51 关键的“独立来源账本真实部署及可信新鲜度/授权”仍是 P0 上线阻断项。不得因为本地 `records_match=true` 或 CI Success 将其转为 Done。
