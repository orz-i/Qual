# QUAL-50/51 · 可执行只读来源对账与独立卷备份门禁（2026-11-04）

> 公开源码历史阶段验证回执：以下工作项状态、真实测试和限制均为 2026-11-04 当次快照，并非目前 Qual 服务的实时任务状态。

> **状态：两个受测子范围已交付；QUAL-50、QUAL-51 仍 In Progress。** 修复源提交 `61d2ddb41ccb1b1622151013f974cfa008eed38b`。本轮未访问真实 GitHub Delivery、未建立可信外部高水位、未获得生产签名私钥、未挂载独立备份卷、未恢复/解锁旧生产队列，也未修改 Qual 默认 Goose17 服务、现有 Agent 权限或任何已交付业务事实。

## QUAL-51：把原有只读签名校验库接到 Operator CLI

此前 `VerifyRestoredSourceEvidence` 只能作为 Go 库 API 从测试使用，实际 Operator 无受限命令入口。现在新增：

```sh
# 所有路径必须为 owner 所有、立即父目录不可被 Group/Other 写入；
# keys/evidence 文件必须是非链接的 0600 普通文件。
# 仅针对已恢复且 quarantined=true 的既有 Spool。
qual-github-adapter reconcile-audit \
  --keys /private/owner-only/pinned-public-keys.json \
  --evidence /private/owner-only/independently-signed-ledgers.json
```

在运行此命令前仍需配置只读的 `QUAL_GITHUB_REPO_ID`、`QUAL_GITHUB_REPOSITORY`、`QUAL_GITHUB_SPOOL_DIR`。`reconcile-audit` 永不发起网络请求，不创建任何来源账本、签发身份、应用恢复授权、发送 Qual Event 或修改恢复来源。输入是各自带 Ed25519 域分离签名的 **Source/Qual/Operator** 声明；Source 的签名事实必须源于可信外部来源日志，Qual 的签名事实必须由独立 Event/Audit 权威导出，Operator 围栏必须绑定相同 SHA 与当前可信来源水位。不能让已恢复的 Adapter 自己伪造这三份来源。

文件协议：`pinned-public-keys.json` 包含 `source`、`qual`、`operator` 三个 Base64 Ed25519 公钥，每个解码后 32 字节；`independently-signed-ledgers.json` 包含相同的三个顶层字段，各有 `canonical_json` 和 `signature`，均为 Base64 字节串，签名解码后 64 字节。各 `canonical_json` 内层须是先前协议要求的、精确规范 JSON。公钥文件最大 2 KiB，签名证据总文件最大 18 MiB；使用 `O_NOFOLLOW`、`fstat` 和 owner/权限检查，不允许符号链接、未知字段、额外 JSON 内容、截断/篡改签名或超限输入。

CLI 会在原本**永久恢复隔离**状态下重新比对 Snapshot/Source/Qual/Fence，返回 `missing_from_restored`、`unexpected_restored`、`qual_inconsistencies` 等差异计数。即使返回 `records_match=true` 及命令退出码 0，输出始终保持：

- `recovered_spool_quarantined=true`
- `activation_authorized=false`
- `latest_source_externally_proven=false`
- `requires_independent_review=true`

这些状态**不会**因为三个不同公钥或一个 15 分钟内的围栏而变为生产级新鲜度。只要缺失合法签名的 T1 新事实，`records_match=false` 且命令退出非零；源目录仍处于隔离状态。此 CLI 的成功只代表“所提供的证据与当次本地快照一致”，不意味着签名服务真正独立或 GitHub 当前事实没有遗漏。

### 已测试

`TestReconciliationAuditIsReadOnlyAndNeverAuthorizesRestore` 用临时真实持久 Spool 生成已 Delivered T0 备份，三个**随机合成** Ed25519 密钥分别签 Source/Qual/Fence，在真实 CLI 参数和文件输入路径下验证匹配；随后附加经三份正确签名的 T1 事实，确认旧 T0 恢复副本缺失一条、非零退出且一直隔离。也测试了签名篡改、文件权限变为 0644、符号链接、未知字段、超限和缺失路径拒绝。本机使用的随机密钥仅存在测试临时目录，不被用作生产信任身份。

## QUAL-50：仅凭 HMAC 不能声称已保存到独立卷

原 `backup --out ... --approve` 可以创建完整 HMAC 签名 ZIP，但即使输出文件就在当前 Spool 同一设备上，也不能声称它是独立存储保护。现在增加**可选的强制不同文件系统设备**检查：

```sh
# 仅供真正挂载、经确认的另一文件系统卷使用：
qual-github-adapter backup \
  --out /mounted/owner-only-backup/new-snapshot.zip \
  --approve --require-separate-volume
```

- 检查真实源 Spool 目录与目标备份父目录的 OS `st_dev`；拒绝不可信/群组可写/符号链接父目录。同设备时**在写出 ZIP 或 .sig 前拒绝**，不改动 Spool 事实或旧文件。
- 原三参数本地备份方式保持兼容，但返回值明确 `separate_volume_verified=false`、`off_host_verified=false`、`encrypted=false`。只有真实不同设备身份时才可能报告 `separate_volume_verified=true`，并在备份完成后再次核查设备身份。
- 这只解决“同卷却宣称独立备份”的误报。不同 `st_dev` **不保证**跨宿主、可信 Operator 密钥保管、加密、异地复制、恢复新鲜度或持续快照；同 OS UID 攻击者也仍可能影响本地文件。
- **当前 Mac 没有可供本项目实际测试的第二个独立备份卷**；本轮实测同卷强制要求被拒绝且无 ZIP/.sig 写入，旧普通本地签名备份仍成功但明确不作独立存储声明。真实跨卷备份与跨卷隔离恢复继续 Pending，不以合成不同设备号的纯函数断言冒充实际跨卷通过。

## 当前 Qual 真实测试与证据登记

本轮新增两个**子范围**的 published TestCaseVersion，均使用现有 PI Principal 通过 Qual API 正式记录实际已运行的 Go Race / 临时 Spool / 隔离 PG/S3 测试，既有 Failed/Blocked 记录不覆盖。

| 工作项 | TestCaseVersion | 实际结果 | TestRun / Attempt | Ready Git/TestRun Evidence |
| --- | --- | --- | --- | --- |
| QUAL-51 | QUAL-TC-64 v1，`bcdc6c3d-1fe1-489a-9a7d-35b34fb65d7a` | Passed（**仅合成三方签名的只读 CLI 正确性**） | `22890d11-5794-4047-8497-0a9fc62b22d1` / `bd98319d-0b42-431e-9a90-fc77b94a1334` | `d57f80b8-b843-4462-a089-1bbd0da4c9d6`，SHA-256 `aeb39549a19b099f59aacd1e78b18d72a24e73a66b97b62ef8d55cea78a331b2` |
| QUAL-50 | QUAL-TC-65 v1，`42c48d52-111f-4233-bb19-a2a119e6b2ef` | Passed（**仅同卷拒绝、诚实标签和备份防护**） | `1123a697-879b-46a6-80b9-8e92af6d312d` / `dab589e3-bc1d-4659-b030-2cf1b2f5cd1e` | `1286b9b7-ddcb-4be0-b552-5978afddb873`，SHA-256 `d9e1f88c7c39bf351c3528341881f7d05963d11e492ea6f84867986ca1d254b4` |

两个 Evidence Packet 都绑定源提交 `61d2ddb41ccb1b1622151013f974cfa008eed38b`，经真实 Qual 授权下载逐字节 SHA 校验及本地 Git SHA 比对。原来的 QUAL-50/51 工作项仍是 In Progress：没有真实可信 GitHub 最新水位生产者或跨卷备份设施，不按这些子测试的 Passed 结案。

## 验证及运营现状

- `(cd integrations/github-adapter && go test -race -count=1 ./... && go vet ./... && go build -trimpath ./cmd/qual-github-adapter)` 全部 **PASS**；本轮真实输出日志 `tmp/qual5051-reconciliation-volume-20261104-race.log`，SHA-256 `3e9cb60ea8de1c1dc3585bf1658d0b54f10e7a4d20e9891ec3b8cf96dfdd8b89`。构建候选 SHA-256 `71b37ea18192e58292aa065ebcf8fc6451d0b5ddcd404b889b5dd748efed40f9`。
- `scripts/tests/ci_goose17_guarded.sh` 使用**全新 disposable Goose17/Goose12 PostgreSQL 和隔离开发 S3**，21 项顶层测试通过、1 项既定历史 Skip、测试数据库残留 0；日志 `tmp/qual5051-reconciliation-volume-guarded-20261104.log`，SHA-256 `9af656fade102dcb11b55a7b317c506d7a01e2cc824580c3cc15e5fc0cfabaff`。默认 Qual `127.0.0.1:18080` 未进行外部 GitHub Adapter 操作。
- 先前 QUAL-40 本地真正 PI Event/Run/Report 及审计历史保留；QUAL-51 **仍缺**可信 GitHub 来源端和 Qual Event/Audit 的独立签名服务、最新来源证明和严格授权的恢复激活。QUAL-50 **仍缺**外部独立卷/异地加密备份实际部署、增长率与归档生命周期治理。
- 此次在两个现有已授权 Qual WorkItem 中记录 scoped TestRun/Ready Evidence，而不是将整个高风险安全项改为 Done。

**禁止**仅凭 `records_match=true`、一个在同 OS UID 下自签署的临时证明、同卷 HMAC 快照或测试中的不同公钥，将已恢复的 Spool 当作新来源执行 `init --approve` 或启用 `serve/once`。
