# QUAL-33 · WorkItem 上的外部 AgentRun 观测（P1 增量）

- 同一独立工作树 `feature/qual-p1-work-observability`，与 QUAL-31 分开原子提交。QUAL-33 由当前真实 PI Principal 通过版本 CAS 领取并转为 `in_progress v5`。
- 复用 WorkItem 详情、既有 `GET /work-items/{itemID}/agent-runs` 和 `GET /agent-runs/{runID}/reports`，按权限延迟加载；Run 分页 / Report sequence 向后分页，绝不启动或管理 Agent 进程。
- 使用**服务端**计算的 current/stale/unknown/terminal 观测，明确来源声明 `succeeded/failed` 不等于 WorkItem Done、TestAttempt Passed 或 Reviewer 结论。乱序状态报告说明 `applied=false` 只归档不更新投影，心跳不被解释为完成/失败。
- 展示来源、外部 Run ID、Host、真正的 Qual Reporter Principal、上次接收时间、历史错误与仅在受权下可见的证据锚点；用户文本作为纯文本，不内嵌执行指令，也不把 WorkItem 的其他 Event 冒称 Run 直接关联。详情页复用 EvidencePanel，不新增第二套审核或状态按钮。
- 从真实 worktree 运行：TypeScript `tsc -b --pretty false`、Oxlint、Node 原生单元回归 **30/30 Passed**（新增 2 条 Run 观测/旧报告专项）、Vite Build 和 Git Diff Check，实际通过。未修改 Go 服务、OpenAPI 或 Goose 迁移；当前运行服务仍为正式 Goose17 基线。
- 尚未实测受权登录的 320/390px Chromium/键盘焦点/跨项目权限撤销与真实报告分页场景；QUAL-33 继续 In Progress。生产 CI 和独立 Reviewer 不能由本地单元通过替代。
