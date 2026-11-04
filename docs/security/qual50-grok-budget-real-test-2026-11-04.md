# QUAL-50 · Grok CLI 并行安全测试与真实执行边界（2026-11-04）

> **结果：一项由真实 Grok CLI 输出、经 PI 实际运行核验的负例回归提交。不是 Grok 自己执行测试，也不是第二位 Human 或独立 OS Principal 的安全验收。QUAL-50 仍为 In Progress。**

## 真实委托与执行事实

- Grok 实际 Qual Principal 为受限 `project_viewer`，具有 `work:read` 与 `work:review` 等只读/审查权限，但没有 `work:write`。共享 macOS 用户意味着 Grok 不具备独立 OS 安全身份。
- 所有操作仅限独立工作树 `fix/qual50-independent-grok`，初始源码为 `a4fb6dec255aa3bafd6ec95ff5f04bca49b0b0e8`，未更改内部主线或正式默认 Goose17。
- 第一轮 Grok CLI `acceptEdits` 在非交互权限请求处拒绝；第二轮范围过大的任务长期没有文件变更，PI 仅停止该 Grok 子进程，没有对默认 Qual Server 发送信号；第三轮明确限定单文件安全测试，Grok 实际生成了 `integrations/github-adapter/githubadapter/qual50_grok_budget_regression_test.go`，但在 `--max-turns 10` 耗尽后退出代码 1，**Grok 本身没有运行完整测试和提交 Git**。
- 真正的 PI 随后逐行检查、格式化、运行测试并在 Grok 原工作树提交 `7387e28922133d00ff2f955022dca08ea881b570`。这项贡献是“Grok 提议测试代码，PI 验证/提交”，不是独立 Reviewer approval。

## 新增可重复的失败门禁

`TestQual50BackupDestinationSpaceBoundaries` 有四个子用例：
1. 物理可用容量大于总容量的畸形文件系统统计必须 `ErrSpool`。
2. 可用容量恰好低于 64 MiB 安全保留区 1 字节，必须 `ErrFull`。
3. 带 8 MiB Payload 和 4 条记录的 ZIP 开销预算低 1 字节必须 `ErrFull`。
4. `uint64` 最大 Payload 必须 `ErrInvalid`，防止容量计算溢出后误认为足够。

**真实结果：**PI 在 Grok 源码工作树执行 `(cd integrations/github-adapter && go test -race -count=1 -v ./... && go vet ./...)`，得到 **47 个顶层 Go Race PASS、0 FAIL**，新四子用例全部 PASS；原始输出位于本地忽略目录 `tmp/qual50-grok-review/grok-proposed-test-real-race.log`，SHA-256 `6ce0a7c72344832e3bc9a9f0e4b158c102841a4025965a2fbef3a1ee0e6e9761`。

## 保留的高风险门禁

这些容量边界检查不能解决长期 Archived/ArchiveIndex 单调增长、离线独立存储与加密、真实 Human Operator HMAC、跨安装恢复后的可信 GitHub Delivery/Qual Event 来源水位，以及共享 OS UID 弱隔离。QUAL-50/51 应保持开放；PR #1/#2 仍应保持 Draft，不能把 Grok 与 PI 的不同 Qual Principal 当作不同 Human 的独立签署。

本测试不需生产权限、不会读取私有密钥/备份、不会为真实 Adapter 配置 GitHub App 或 Webhook。
