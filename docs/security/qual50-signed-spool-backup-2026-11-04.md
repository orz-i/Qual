# QUAL-50 · GitHub Adapter signed spool snapshot / isolated restore

**Scope**: external Go Adapter only, private local pilot. Depends on PR #1 (GitHub signature ingestion, durable replay-safe Archive and Index). Does not read any Human production signing secret, actual GitHub App credentials, operator backups, live Qual database, or change the live server. This is an independently reviewed-operator design, **not** evidence of a second real Human reviewer.

## Trust and operational model

A trusted Human Operator must inject a **separate, independent high-entropy key** as process environment variable QUAL_GITHUB_BACKUP_HMAC_KEY (32–256 bytes). It must not be the Qual database/Pi/Human PAT, GitHub OAuth token, webhook secret, or the Qual PostgreSQL/S3 operator HMAC signing key. Do not type the value into shell history, write it in Git, or include it as a command argument.

The following invocations assume a properly protected OS process environment and an existing configured owner-only spool root. ZIP and detached signature outputs must not exist, must be absolute paths, and their parent must be owned by the effective operator UID and not writable by group/others.

    qual-github-adapter inspect
    qual-github-adapter backup --out /owner-only-backups/adapter-2026-11-04.zip --approve

After an operator-controlled transfer/storage of BOTH the ZIP and its .sig file in independently protected storage, restore only into a **new, nonexistent** owner-only directory:

    qual-github-adapter restore --file /owner-only-backups/adapter-2026-11-04.zip --into /owner-only-restore/new-adapter-spool --approve

This restore does **not** automatically switch the configured Adapter, restart any service, change the mapping/authorization, submit Qual Events, or reopen production ingress. The CLI explicitly returns snapshot_freshness_verified=false and requires_source_reconciliation=true. A real authorized Operator must reconcile source events accepted **after** the snapshot against an independently trusted GitHub delivery/Qual ledger and target mapping, then independently decide on recovery/cutover. Manual restore of a production Operator snapshot has **not** been performed.

**Critical rollback limitation (reproduced in an isolated pinned-source technical audit):** HMAC authenticates snapshot integrity, **not freshness**. A completely valid, earlier backup may omit a GitHub delivery that was accepted **after** the backup; restoring that older backup and replaying the later valid HMAC-signed Body under another unsigned Delivery ID creates a new Pending fact. Local signature verification alone cannot prevent this. Do NOT activate a restored spool as a production webhook listener/worker without independent external event reconciliation, a trusted monotonic high-watermark/checkpoint or an equivalent server-side deduplication guarantee. This is a remaining release blocker and is not fixed by atomic rename.

## Integrity design

1. A snapshot first acquires worker then queue cross-process locks, ensuring no signed ingress or Qual worker mutation races with its read. It reconciles existing Pending/Delivered crash states and archived SHA indexes, then scans strictly named owner-only 0600 JSON records from Pending, Delivered, Archived, and ArchiveIndex. Files/summary fields are safe structured metadata, never raw webhook payloads, comments or PATs. No files are deleted.
2. The snapshot is an uncompressed ZIP of those records and a deterministic manifest containing per-file SHA-256, relative path and byte count. The full ZIP is authenticated with HMAC-SHA256; the lowercase hex tag is stored separately in a 0600 .sig file. Both outputs are fsynced and published using no-replace hard links (rather than an overwriting rename) without logging their contents. Before writing either temp file, the destination filesystem is checked against actual owner-available bytes, the estimated raw record bytes, conservative ZIP/manifest overhead and an additional 64 MiB safe-write reserve. Partial publication is never reported as a successful backup.
3. Restore requires the matching independent key and intact detached tag. HMAC is verified over the **entire ZIP before creating any output directory**, then strict path, mode, size, count, manifest SHA, duplicate, zip-slip and compression-bomb checks run. All files are staged under a randomly named 0700 directory, fsynced, checked by the normal Spool integrity/reconciliation logic, then renamed atomically into an absent destination.
4. ArchiveIndex and Archived records preserve original GitHub Delivery ID, original HMAC-verified Body SHA-256, Qual Event UUID and failed/retried history. A restored Adapter refuses changes to the original Delivery ID and will not convert the same originally signed Body under another unsigned Delivery UUID into a new Event.
5. Out-of-bound inputs fail closed: no signing key, untrusted owner/mode, invalid path, wrong HMAC, missing signature, changed ZIP bytes, malicious ZIP path, target exists, invalid referenced spool metadata, insufficient free space or failed file/dir fsync.

## Limits and unresolved production prerequisites

- This is an **integrity-authenticated backup, not encrypted storage**. Transport and remote storage must provide separate confidentiality, access isolation, backup retention and key management. Human and PI sharing a macOS UID are not strongly isolated.
- One signed snapshot is explicitly capped at **100,000 records**, **512 MiB raw records** and **600 MiB total ZIP** (16 MiB manifest). Overflow stops rather than truncating history. Unbounded hot+Archived growth, multi-volume backup/retention, independent disk volume/backups, quota enforcement and continuous monitoring remain separate QUAL-50 release gates.
- The 64 MiB spool write reserve is for small journal records, not sufficient for a large backup or restore. Restore independently checks adequate available space before staging, but production recovery requires dedicated storage and reliable external Operator backups.
- Keyed HMAC library/CLI and actual disposable tests use **synthetic test-only keys**. This is not a verified signed Human Operator backup or real isolated production restoration.
- QUAL-37 real GitHub App Webhook delivery, TLS ingress, Human-reviewed mapping, independent Principal authorization, real PI session resume, and full independent security acceptance remain Pending; QUAL-36 and QUAL-50 should not be marked Done solely because CI is green.

## Real verification

The external Go module tests run Go Race/Vet on signed backup/restore, wrong/tampered key/tag/ZIP, destination overwrite refusal, signed malicious ZIP paths, replay of archived records after isolated restore and true filesystem disk probes with injected low-space failure. A separate protected Goose17 integration test builds actual Adapter binaries, records a locally synthesized signed event to disposable PostgreSQL/S3, archives, runs the signed backup/restore as distinct Adapter subprocesses, reopens restored private spool in a second listener, verifies old signed bytes under a different Delivery UUID are deduplicated and confirms no additional Qual Event/Audit. No default Qual DB mutation.

The source, test run, GitHub CI and official Qual artifact evidence must remain explicitly separated, with the **same actual commit SHA** in each reference.


## QUAL-51 恢复前默认隔离

恢复后的 owner-only Spool 现在默认带有两份一致、持久化的恢复隔离标记：read-only inspect 可查询，serve/once/Enqueue/PinTarget/Update/Requeue/Archive 都必须拒绝，且无本地自批准解锁命令。合法的 HMAC 快照仍不能证明未来事件是否丢失。实际 T0 备份、T1 新入站事件真实写入隔离 PostgreSQL、T2 恢复旧快照时的安全拒绝过程和真实限制见 [QUAL-51 文档](qual51-restored-snapshot-quarantine-2026-11-04.md)。
