# QUAL-51：旧有效签名快照恢复后默认隔离的强制门禁（部分修复）

- 工作项：QUAL-51（高优先级 Bug，持续 In Progress，**尚未满足可信来源新鲜度证明**）
- 代码范围：外部 Go GitHub Adapter 和真实一次性 Goose17/PostgreSQL/S3 集成测试
- 关联 PR：PR #2（叠加依赖 PR #1，仍为 Draft）
- 本阶段**不**执行真实 GitHub Webhook 注册、生产 Qual 写入、真实 Human-only 签名密钥读取或主服务切换

## 威胁和修复边界

有效的 Operator HMAC-SHA256 签名备份证明保存的字节未篡改，但不能证明其后没有已经接收并写入 Qual 的新事件。如果 T0 备份之后，T1 原服务收到了新 GitHub Body/HMAC 和 Delivery ID 并将 Event 持久化，那么用 T0 快照恢复的新服务会缺失 T1 的两种去重身份。此时同一签名 Body 使用新的**未被 HMAC 覆盖**的 Delivery ID，旧代码会错误地返回 HTTP 202/new=true。

**本阶段修复的是安全激活门禁，不是假装已经完成可信账本对账。** RestoreSignedBackup 验证整个独立签名的 ZIP 和每条脱敏回执后，只在一个 owner-only、尚未发布的暂存目录工作。在写入恢复内容前，持久化双份相同且严格验证的 recovery-quarantine.json 和 recovery-witness.json：恢复来源 Archive SHA-256、Quarantined=true、requires_source_reconciliation=true 与时间。文件均为 0600、目录 0700，并在原子发布前文件/目录 fsync。两个文件只要单份缺失、内容不同、所有权或权限不安全、字段非法，就拒绝启动。

恢复结果经 OS 原子 no-replace 发布到全新目录，绝不自动切换当前 Adapter。**没有**仅凭 --approve、环境变量或同一 PI 身份就“解除隔离”的命令。

### 隔离状态的可检查行为

- Restored Spool 在 Quarantine 状态下允许 inspect、读取 Pending/Counts/Capacity 与静态回执；inspect.recovery 显示隔离状态和原始快照 SHA，不输出签名密钥或原始 GitHub Payload。Capacity 的 ingress_blocked / restore_quarantined / requires_source_reconciliation 反映真正禁止的业务活动。
- 新建 Handler / serve 必须拒绝。直接绕过 Handler 构造函数调用 Enqueue（包括同 UUID 原始签名重放）仍拒绝；**不能对任何未被完整对账的恢复来源请求返回 202**。
- Publisher.PublishOnce / once 在打开连接或向 Qual 发送任何 POST 之前拒绝；在 worker 锁内重复检查。PinTarget、Update、RequeueAfterReview 和 ArchiveDelivered 的队列变更入口也拒绝。
- 以恢复副本再创建新的 HMAC 签名 ZIP 并二次 Restore，仍再次默认隔离。原有未经 RestoreSignedBackup 的新建正常队列仍能合法验证签名并记入新事件。
- 如果恢复标记异常，OpenSpool 会 fail closed，而不是错误地返回正常队列身份。

### 可复现实际 T0/T1/T2 集成

使用本机真实外部 Adapter 子进程与**隔离** Goose17/PostgreSQL+开发 S3：

1. T0：已有合法事件写入真实一次性 PG 并落入 Delivered/ArchiveIndex，随后执行外部 Operator CLI 使用**合成独立**密钥生成签名备份。
2. T1：在仍运行的原 Adapter 中提交内容不同且 HMAC 合法的新 GitHub 合成事件，真实向一次性 Qual PostgreSQL 写入第二条 Event，按原 SHA 与新的未签名 Delivery ID 重放应返回 new=false。
3. T2：将 T0 签名备份恢复到一个新目录。CLI inspect 可读取归档历史和恢复隔离状态；独立的 serve 进程不能启动，once 不能 POST。隔离 PG 最终恰有 T0/T1 两条 Event/Audit，没有来自已恢复 T0 副本的新事件；原 WorkItem/TestAttempt/后续 PI Run 不因恢复自动改变。

本机执行 Go Race/Vet、Linux 交叉编译（**不**冒充 Linux 真机执行）、Goose17 受保护集成19项，临时 PG 库在验收后为0。历史已归档 QUAL-TC-49 的原 FAILED Attempt **必须保留**：它真实展示了修复前的回滚重放风险。新增安全隔离用例可以 Passed，但**不代表 QUAL-51 整体 Done**。

## 仍未达标的可信发布门禁

1. **没有真实独立签名来源账本／水位**。本实现故意不提供本地 override；日后需专用、不同可信身份的 Operator 对 GitHub Delivery 与 Qual 实际持久 Event/审计做完整 T0 后对账，建立可验证单调水位和安全解锁方案，避免缺失来源哈希与晚到事件。
2. **共享 macOS UID 不是抗恶意篡改边界**。双标记只是发现单文件缺失/误操作；同 UID 攻击者可以同时删改两份标记或篡改受控文件，旧版 Adapter binary 也可能完全忽略新标记。生产必须使用真正独立 OS Principal/隔离宿主、固定发布二进制 SHA、不可由该进程操作者修改的签名恢复授权与审计；在此之前不可启用恢复目录。
3. **签名快照不加密**，须另外建立离线加密备份、可信备份日志、可用磁盘扩容和外部监控。恢复阶段没有真实 Human-only 秘钥演练或公网 GitHub App/TLS 入站测试。
4. 不应把 CI 绿色、同一 PI Principal 的 Executor/Tester/Auditor 职责历史、合成签名验证当成不同真人独立安全批准。PR #1/#2 继续 Draft，QUAL-36/37/50/51 按真实门禁保持未完成。

## 后续明确的可验收工作

在独立可信身份上建立可验证的来源账本快照／水位，并据此完成全部 T0 之前、T0 至恢复期间、晚到／乱序、跨 UUID 同 Body、并发与断电恢复用例；只有完整对账且获得真实被授权 Operator 审批后，才可以另设计不可被同 PID/UID 自行伪造的恢复激活机制，并由独立 Reviewer 签收。当前阶段不提供这种机制，故可以安全地**保持隔离**，不能安全地完成生产切换。
