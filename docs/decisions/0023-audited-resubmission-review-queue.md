# ADR-0023 — 整改后显式重新送审，必须进入待复核队列

- 状态：**Proposed（本机隔离回归已通过，真实 Human UI 验收待执行）**
- 日期：2026-11-01
- 关联：QUAL-16、QUAL-20；限定修订 [ADR-0021](0021-review-lane-decision-precedence.md) 的队列显示规则，不改变 Reviewer 履历或 [ADR-0022](0022-explicit-done-gate-owner-acceptance.md) 的 Done Gate。

## 背景：真实工作流与 UI 分类不一致

QUAL-20 经 Human 记录 `reviewer:needs_work` 后，PI 完成真实代码修复和 Failed→Passed 回归，于 2026-11-01 使用服务端 CAS 追加整改评论，并以 `in_progress v13 → in_review v14` **合法重新提交复核**。旧 Reviewer `needs_work v9` 仍是最近不可变决策。历史的 `ClassifyReviewLane` 不区分“尚未执行整改的新提交”与“整改完成后显式重新提交”，导致工作项即使已经在 `in_review`，审核队列仍显示 `needs_work` 并提示状态冲突。这样“等待审查”计数看似 0，虽实际有人类工作待审。

## 决策

后端只读队列投影在审查身份/权限保持不变前提下，额外读取服务端**已有 Audit**（不是允许客户端伪造字段）：

- 如果当前工作项仍是 `in_progress`，最后 Reviewer 是 `needs_work/failed`，保持 `needs_work`。执行者没有真正提交审查，只写评论、证据或 Executor/Tester RoleEntry 并不意味着自动审查。
- 如果工作项是 `in_review`，最后 Reviewer 为 `needs_work/failed`，并且**不存在**晚于该 Reviewer 的真实 `work_item.transition → in_review` 审计事件，仍归类 `needs_work` 并显示旧状态冲突提示。这保存 ADR-0021 的原安全语义。
- **仅在**工作项当前为 `in_review`，且已有真正的服务器审计 `work_item.transition`，其结果 `status=in_review` 和 `new_version > latest_reviewer.work_version`，将 `resubmitted_after_review=true`，审核队列转为 `awaiting`，UI 明确“整改后重新提交 · 待复审”。原 Reviewer 的 `outcome`、`self_review`、`recorded_at` 和原始 Audit 全部保留，不假造新的 Passed，也不改变 WorkItem 本身的状态。
- 若此前 Reviewer 为 `passed`，但后来又有重新提交事件，仍应重新审核，不能把旧的 Passed 展示为 `ready_to_close`。
- 在此次重新提交后，只要 Reviewer 再次真实写入较新的 `needs_work/failed`，原 `resubmitted_after_review` 自然变回 false，进入整改队列。任何 UI Badge 都不能推导 `work:review` 或 Done 授权。独立/Owner 签收仍由 ADR-0022 的服务端 Done Gate 决定。
- 对外只增加一个只读 `resubmitted_after_review` 布尔事实；客户端不提交该字段，不读取私有 Audit 的内容，不得到 `audit:read`。投影仍由 `work:read` 授权，且可展示最新 Reviewer 历史。

## 已实施的可复验范围

- OpenAPI/TypeScript SDK：`ReviewQueueEntry.resubmitted_after_review` 必填布尔字段；`review_lane` 保持 `awaiting|needs_work|ready_to_close`，不破坏现有筛选。
- Go Domain Application + PostgreSQL：只读投影用项目/工作项和审计版本比较，明确重新提交，保持原 Reviewer 优先的其他场景。
- React 审核中心：分别显示“整改后重新提交 · 待复审”“当前仍需要整改”和“独立核验通过 · 待关闭”，历史 needs_work 以**上次 Reviewer 历史**标示，不暗示新批准。
- 测试：项目本地 Go1.27.3 Domain / PostgreSQL `-race` 真正进行 in_review→needs_work→in_progress→评论→in_review，验证 `awaiting`、历史仍为 needs_work、未提交状态仍 `needs_work`、重新复审后再 needs_work、跨项目 404、未授权 403、分页和不可变身份；Web 5 项策略测试、Python 50 项本机回归。
- **默认本机运行中的 Go 服务尚未重启到本次新二进制**，截至实现时其旧 API 仍会把 QUAL-20 分类为 `needs_work`。因此本决策不声称真实运行中前端已展示新类别；这是部署/受控重载待办，不能用接下来的隔离测试替代。

此项只改变**队列展示**，不变更真实权限/签收策略、不修改 Human Reviewer 历史、不执行 Human 测试/VoiceOver，也不为数量好看创建假待办。所有变更需分段原子提交和真实证据回执。
