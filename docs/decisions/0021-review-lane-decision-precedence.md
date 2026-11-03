# ADR-0021 — Reviewer 决策与 WorkItem 状态分离的审查队列

- 状态：Accepted（Phase 10 审计 P0 修订）
- 日期：2026-11-01
- 关联：QUAL-16、QUAL-17；延续 [ADR-0020](0020-review-queue-work-visibility.md)

## 背景

QUAL-4 曾处于 `in_review` 且最新 Reviewer 为 `needs_work/self_review=true`；QUAL-8 为 `in_review` + 独立 `needs_work`。旧队列只按 WorkItem 状态将这两项呈现“等待审核”，与最新审核要求冲突。后续虽然按授权 WorkItem CAS 退回 `in_progress`，界面规则本身依然不安全。

## 决策

- `WorkItem.status` 与 Reviewer RoleEntry.outcome 是两个不同维度。后端在项目级只读队列增加派生的 `review_lane`，**不修改** WorkItem 或历史 RoleEntry：
  - `needs_work`：最新 Reviewer outcome = `needs_work` 或 `failed`，优先于 `in_review` 工作状态。
  - `ready_to_close`：当前 WorkItem `in_review` 且最新 Reviewer outcome = `passed`、`self_review=false`；只是“核验记录已通过，等待合法状态流转”，不等于 Done。
  - `awaiting`：当前 WorkItem `in_review` 且无独立 passed（包括没有 Reviewer、仅 observed、`self_review=true` 的 passed）。
- `in_progress` 仅当最新 Reviewer outcome 为 `needs_work` 或 `failed` 时进入队列。一个 `in_progress` 任务的历史 passed 不应被误展示为“可关闭”。
- 对 `status=in_review` 且 `review_lane=needs_work`，UI 必须显式显示状态冲突：最近 Reviewer 要求整改、当前工作项尚未由授权负责者合法退回进行中；不能呈现“批准”快捷操作。
- 自审 `passed/self_review=true` 仅是身份标记，不当作独立批准；再次审阅/变更仍须 `work:review`、CAS、幂等，不能由前端推断权限。
- 改动只影响项目受限 `work:read` 业务摘要。原始 Audit 仍由 `audit:read` 独立管理。允许新 `review_lane` 用于未来扩展 UI、CLI、集成测试。
- 回归覆盖五种互相冲突情形：in_review+needs_work、in_review+independent passed、in_review+self passed、in_review no reviewer、in_progress+needs_work。不能更改旧审计事实或升级 Agent 权限。
