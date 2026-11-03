# ADR-0020 — 项目审核队列是工作履历视图，不是 Agent 调度或无权审计入口

- 状态：Accepted for Phase 10 local pilot
- 日期：2026-11-01（Qual 本机工作区）
- 追踪：QUAL-16 / Phase 10 C Light Brand

## 背景

已有 `work_role_entries` 保存真实 Reviewer 决策，`self_review` 由服务端计算，但前端缺少项目级队列。当前 QUAL-13/14 处于 in_progress、最近独立 Reviewer 为 needs_work，如果只筛选 `in_review` 就会掩盖真实整改请求。不能把 Reviewer 的任何 `needs_work`/`passed` 文本误认为 WorkItem 自动完成，也不能通过 UI 绕过 `work:review` 或 `audit:read`。

## 决策

1. 新增只读 `GET /api/v1/projects/{projectID}/review-queue?limit=50&offset=0`；要求当前身份及凭证和项目策略均允许 `work:read`。`work:review` 不是查看队列的前提；当前 PI Agent 可查项目工作履历，但不能执行审核。
2. 只入队当前 `in_review` 的工作项，以及当前 `in_progress` 且**最新 Reviewer 履历结果 `needs_work`** 的工作项。两类在 UI 分开：等待审核、最新审核要求整改。不将旧记录当成新审核；报告 Reviewer 时间、版本、summary、reference、actor UUID、`self_review`，关联真实 Principal 名称/类型只通过现有项目级 `principals` 接口。
3. 仅读取最新角色履历的事实，不读取项目级 `audit_records`，不扩大 `audit:read`。`work:read` 只可见业务可查的 Review/Role 记录。独立原始审计视图只在有效 `audit:read` 时向浏览器请求现有 `/audit` 端点；审计身份不可借工作队列推断。
4. 数据库按项目范围查询，服务端支持限制 1–100 条与 offset 0–10000，返回 `items`、`has_more`、可选 `next_offset`；这是非快照 offset 分页，界面明确显示当前已加载数量，不宣称全量统计。
5. 队列**只导航到详情及真实证据**，不提供快速 Done 或伪“一键批准”。Reviewer 权限存在时提示通过有 CAS/幂等的真实角色记录/状态迁移；无权者只读及缺权限提示。相同 Principal 的 Reviewer 记录显式标记自审“非独立批准”，不隐藏不同身份的审核事实。
6. 继续复用既有 WorkItem/RoleEntry/Principal 授权及 DDD 事务。新接口不创建 Agent、不执行 CI、不过度暴露凭证/邮箱、不允许通过无审核能力的 Agent 假冒 Human。

## 验证

- Go 集成数据库验证真实 Reviewer needs_work 在 in_progress 入队、in_review 入队、403/404 权限边界、工作项版本变化和分页。
- React 实际项目只读内容 / 隔离视觉夹具分别验证 Reviewer 身份、needs_work、自审、只读入口；登录 Human/VoiceOver/实际权限切换如不可访问则明确保留验收阻塞。
- WorkItem status 不能单凭队列成员资格推断 Reviewer 的批准/否决；所有真实批准需授权用户的审计记录。
