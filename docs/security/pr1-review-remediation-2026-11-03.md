# PR #1：独立技术审查后的安全整改（2026-11-03）

原审查的公开固定 Head 为 fc240bd7da0c26d08f72e9ac116d0183159feadd。报告及失败复现留存在 Mac 工作区被 Git 忽略的 tmp/audits/pr1-independent-review。此文仅记录 PR 候选源码的真实修改和未满足门禁；不包含 Human Token、真实数据库或本机私有归档。

## 已整改的 P1

1. **未签名 Delivery ID 重放**：X-Hub-Signature-256 对请求 Body 做 HMAC，不对 X-GitHub-Delivery 做签名。新代码在跨进程 Queue Lock 下，对已落盘的 Pending 和 Delivered 执行完整 Raw Body SHA-256 去重；同一 HMAC 和原始 Body、不同 Delivery UUID 不再创建第二个队列条目或第二次 Qual Event POST。回归测试在旧代码失败，整改后仅一条记录且 new=false。
2. **崩溃窗口与映射变更**：启动时协调 Pending/Delivered 双副本，已有 Delivered 回执优先且不可覆盖；发生不一致元数据时拒绝继续。首次 HTTP 投递前持久固定已审批的 Project/WorkItem/Thread UUID，网络结果不确定时仍不能因批准映射变化而静默改投另一项目。三项回归在旧代码先失败，修改后 Go Race 通过。
3. **剩余信任边界**：只按原始 Body SHA-256 去重，无法对一份从来没有在本地看到过但被重放的有效签名请求证明其首次 Delivery ID 真正由 GitHub 生成。未来真实 TLS 接入层、GitHub App 安装和跨端到端真实事件回放仍需独立验证。受限本地合成签名测试不等于公网生产验收。

## 已强化但尚需运营门禁的 P2

- **人工重试**：命令现在要求 --approve 和受限 reason-code（auth-restored、link-repaired、transient-verified），状态/重试原因在有界 History 中追加、禁止通过普通 Update 移除历史。OS UID 并不是 Human Principal；共享 macOS UID 时仍不代表独立 Human 授权，更不代表 OS 不可篡改证据。
- **PR 内容门禁**：PR 描述校验改为 pull_request_target，仅检出受保护的 Base SHA 并运行 Base 中固定的检查器，不运行 PR Head 自己提供的脚本。首次 PR #1 的 Base 仍没有该工作流，不能将本 PR 的绿色校验当成可信独立门禁；待默认分支合入后需配置 required check。
- **队列容量**：inspect 增加容量使用率和 80% 预警。1000 Pending/10000 Delivered 的硬限制和满载 503 仍存在，缺少安全归档/墓碑式去重/回收策略是无人值守生产发布阻断项。严禁直接清空历史 Delivered 来规避容量。

## 验收纪律

本轮由执行整改的同一 Agent 负责回归，不能冒称另一个 GitHub Principal 的独立安全审批。远端真实 GitHub Webhook/App、TLS Ingress、实际 Integration Credential 的最小授权、正式映射、真实 PI 恢复试点、QUAL-37 独立验收仍在范围之外。PR 继续保持 Draft；修复代码 + 本地/CI 测试不等于正式合并批准。


## 2026-11-04 后续专项整改索引

“缺乏安全热目录归档”的阻断已由显式 Operator 安全归档实现解决，详见 [专项设计与回归](pr1-safe-delivered-archive-2026-11-04.md)；原文所述的长期磁盘无限增长、自动运营/独立 Human 审批、真实 GitHub App 和独立验收仍待完成。历史审查原结论和当时缺口不应直接覆写。
