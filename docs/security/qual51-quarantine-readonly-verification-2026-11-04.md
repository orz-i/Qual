# QUAL-51 · 已签名恢复快照只读检查无副作用（2026-11-04）

> **代码加固已通过，但不是完整可信来源新鲜度验收。** 此变更以先前 `847e97f` 新鲜 Spool 双来源见证防护为基线，仍禁止旧签名快照恢复后解除隔离；默认 Qual Goose17 实例未升级、未增授事件写入或运行真实 GitHub Webhook。

## 发现的问题

恢复旧有效签名 ZIP 后，会通过 `OpenSpool` 进行校验。此前 `OpenSpool` 即使发现 `recovery.quarantined=true`，也执行正常活动队列的 `reconcileLocked` 和 `reconcileArchiveLocked`。正常 Activity 队列崩溃时，这两个函数有合理的自动修复能力：同步后的 Delivered 作为权威，可以删除已遗留的同源 Pending；已经创建和同步 ArchiveIndex 时，可以移动 Delivered → Archived。

但是**在未经独立可信来源对账的旧签名恢复队列中，只读 `inspect` 不应修改任何 Pending、Delivered、Archived 或 ArchiveIndex 事实**。如果恢复数据包含未解决的重叠，自动修复会在独立 Operator 授权之前改变原快照回执，与“隔离只读”契约不一致。

## 本轮整改

- 新增 `validateQuarantinedReadOnly`，持有现有 worker+queue 跨进程锁，扫描已恢复的四类目录、限制名称与类型、检查回执和 SHA 索引一一对应、拒绝 Pending 和 Delivered/Archived 重叠、拒绝已索引但尚未规范完成的热归档移动。
- `OpenSpool` 先检查真实恢复隔离标记。**Active 正常 Spool 保留原有崩溃后修复行为**；Quarantined Spool 只调用无写入的完整性校验函数，出现任何未归一重叠直接 Fail Closed，记录字节不变。
- 新增两条真实合成文件系统负例：合法独立签名备份恢复后，手工模拟一个原本会被活动 `reconcileLocked` 删除的 Pending 重叠；另一个原本会被活动 `reconcileArchiveLocked` 移动的已索引热归档。新代码必须拒绝 OpenSpool，且原始文件和恢复隔离标记仍保留。
- 该校验发生在恢复快照的实际内存实例之外，不访问任何 Operator HMAC、生产 PAT、GitHub Webhook Secret 或外部项目数据；不会把已恢复的旧签名回执当成经可信最新性对账的历史事实。

## 实际执行的验证

- `(cd integrations/github-adapter && go test -race -count=1 -v ./... && go vet ./...)`：**51 条 Go Race 顶层测试 PASS、0 SKIP**，原始安全日志 SHA-256 `a282d169e3c1b883e5238fbd03fb2cd67a4476f8cc53014cc5f2ba9615cb9748`。
- 独立真实 PostgreSQL/开发 S3 的 `scripts/tests/ci_goose17_guarded.sh`：**19/19 专项 PASS**，包括完整 T0→T1→T2 真正 Adapter/Goose17/归档/PI Run/TestAttempt 与 4 项上下文/历史安全边界，原始日志 SHA-256 `6df12e6fd7e92b42fa963334cbb0fd5f07175f0124600c083eca75b09db5a4e3`。每个测试数据库由受限 CI 脚本清理，正式 `qual` 不接收模拟事实。
- 此变更没有修改或掩盖先前 `QUAL-TC-49` 真正的 Failed Attempt；之前的 `QUAL-TC-50` 部分恢复隔离用例可以记录真实通过，但整体 QUAL-51 仍 In Progress。

## 尚未完成的阻断项

**强制隔离、来源见证和只读校验不能证明旧签名快照包含 T0 后的最新 GitHub Delivery/Qual Event。** 仍必须由真正隔离的 OS Principal/可信 Operator 和不可被同 UID 修改的外部事件账本完成 T0/T1 水位与全量来源核对；否则不能启用已恢复旧 Spool。当前不提供本地 `--approve` 解锁，也不能把 Go 测试通过、Grok/PI 同宿主协作、自审或一份签名 ZIP 误称独立 Human 生产发布验收。
