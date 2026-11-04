# PR #2 · Restored-spool publication race: adversarial technical follow-up

Original source examined: public PR #2 pinned Head aa90dd0ff010b591f0473698cecdad17b52da6f2, extracted into a separate Git-ignored source snapshot. This is a technically separate review procedure but still the **same real PI Principal / GitHub author**, NOT an independent GitHub security approval.

## Finding R1 · Directory publication had a check/use window

Original code in integrations/github-adapter/githubadapter/signed_restore.go, RestoreSignedBackup, checked os.Lstat(destination) before and after creating/verifying a staging directory and then called os.Rename(stage, destination).

Go's os.Rename **does** reject an existing directory when its internal Lstat runs. However, Go subsequently calls the OS replacement-capable rename syscall. The two operations are not atomic; a second Operator reserving an empty directory after the final Lstat and before the kernel syscall can lose that directory. A deterministic, isolated check/use interleaving reproduced the underlying syscall behavior on the Mac. A separate test confirmed that directly calling Go os.Rename when the destination already exists normally returns an error. **Do not misstate the narrower race as proof that Go os.Rename unconditionally overwrites directories.**

The remediation uses pinned golang.org/x/sys/unix, matching the version already used by the Qual server. Darwin uses RenameatxNp with RENAME_EXCL; Linux uses Renameat2 with RENAME_NOREPLACE. Unhandled platforms fail closed instead of falling back to os.Rename. Actual regressions cover successful initial publication and refusal to overwrite another Operator's empty destination, an existing regular file, a symlink, or a previously published snapshot. On both supported platforms the OS enforces exclusion atomically. The source still verifies HMAC, all ZIP contents and owner-only paths before attempting publication.

## Finding R2 · Older valid signed snapshots do not establish freshness (NOT FIXED)

A detached HMAC authenticates the saved bytes, **not** whether more Webhook deliveries were accepted after the backup was created. A separate synthetic HMAC test constructed a valid earlier snapshot, then accepted and completed a later signed GitHub payload, restored the earlier snapshot to a new directory, and replayed the later payload under another unsigned Delivery UUID. The intact live spool returned new=false, but the old restored spool returned new=true. This is a missing external monotonic high-watermark, not a cryptographic forgery or failed HMAC validation.

This task does **not** invent a trustworthy external ledger or claim to have reconciled the real GitHub delivery history. The restore CLI now explicitly returns snapshot_freshness_verified=false and requires_source_reconciliation=true, and the safety guide forbids production activation without independent trusted GitHub/Qual event reconciliation. This advisory flag does **not** cryptographically enforce freshness or prevent a same-UID operator from manually launching the restored spool. The remaining production acceptance blocker must be tracked in Qual with its actual reproduction evidence.

## Evidence and scope

The original pinned source and adversarial tests reside in the local ignored audit path tmp/audits/pr2-independent-20261104. No real Operator key, default Qual PG/S3 content, GitHub Webhook or production service was read or written. The remediation is checked separately using Go Race/Vet, Darwin tests, Linux cross-compilation and guarded disposable Goose17/PostgreSQL/S3 integration. Existing real TestAttempt/CI evidence stays immutable; every new run is bound to the actual SHA. A reviewer with a different real authorized Principal must assess the final PR and real Human Operator's backup/freshness/cutover procedure.


## QUAL-51 部分整改续报（2026-11-04）

原来 valid-older-signed-snapshot 可复现的 T0/T1/T2 重放缺口仍是**真实独立来源新鲜度未满足**。后续源码候选已新增不可由本地 CLI --approve 清除的默认 Recovery Quarantine：恢复在未公开的暂存目录里首先写入两份 0600/owner-only 隔离标记并 fsync，OpenSpool、Handler 和 Publisher 等入口在对账前 fail closed；read-only inspect 显示恢复源 SHA 和状态。实际 Goose17 隔离测试验证 T1 后另一条真实 PG Event 已写入、旧 T0 签名快照的 serve 和 once 均无法启动／投递，Event/Audit 只有既定 T0/T1 两条。完整说明见 [QUAL-51 受控恢复隔离及局限](qual51-restored-snapshot-quarantine-2026-11-04.md)。

这一补丁只阻断未经核验的**使用**，不代表已找到可信跨主体事件水位，更不能对抗与该 Agent 共享 OS UID 者同时篡改全部文件。旧本机 PI 对抗审查仍不是 GitHub 第二 Principal 的独立批准。
