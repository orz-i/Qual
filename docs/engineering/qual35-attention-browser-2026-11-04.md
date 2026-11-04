# QUAL-35 · 原协作审核页 Attention Inbox 交互与隔离 Chromium 验证

范围：原 `apps/web/src/features/reviews/pages.tsx` 内增量接入 `AttentionInboxPanel`，不重建第二个审核系统。它直接消费 QUAL-34 新的只读 OpenAPI/SDK；真正的 Reviewer/Acceptance 和受权限约束的原始 Audit 仍是独立系统。

## 实现说明

- 默认只展示一条 WorkItem 最重要的已授权注意力线索；六个过滤器对应全部、审核、整改、外部阻塞、失联/未知、需输入；计数只基于实际已加载数据，不冒充全项目统计。并发 offset 分页重合时去重，不隐藏有界分页截断提示。
- 显示真实来源类型/ID/报告时间和外部未验证时间、责任 Principal；外部 Run/Event 用 `hint_only` 明示未核实。Reviewer 自审、旧 Failed、外部 AgentRun 的 succeeded 不能由此自动推断 Done 或 TestAttempt Passed。
- 仅通过既有 WorkItem 详情和证据链接行动，没有新的批准、启动、停止或重试 API。旧版审核队列完整保留在可键盘展开的原生 `details` 中，展开时才请求历史队列，避免默认重复卡片。Audit 继续需要明确 `audit:read`，单独权限失效不读取。
- 正确处理局部空数据、外部 API 错误及没有 `work:read` 的状态；接口错误时不展示旧缓存卡片。后端处于未升级版本时保留原审核队列入口。

## 实际验证结果

1. 在独立源码工作树 `feature/qual34-35-attention`，使用 pnpm 离线缓存安装 `--frozen-lockfile --ignore-scripts`，保持工作树内真正的 SDK workspace 链接。`pnpm contracts:check`、`pnpm check:sdk`、`pnpm --filter web typecheck`、`pnpm --filter web lint`、`pnpm --filter web test:unit`（**29/29 PASS**）、`pnpm build` **全部 PASS**。首次借用外部 workspace SDK 的 symlink 时 typecheck 失败，已正确安装本工作树依赖并重新通过，不关闭安全检测。
2. 真实本机 Chromium 自动化，使用**隔离、合成** Session、权限、工作项/来源 JSON，建立只监听 loopback 的临时 5178 Fixture 服务，在 `390×844` 和 `320×740` 视口观察真实页面 DOM：两个宽度的 `document.documentElement.scrollWidth` 分别等于视口 390/320，无横向溢出；过滤器展示 5 条合成事实，整改过滤后只展示对应 1 条。键盘聚焦按钮按 Enter 后可切回“全部”，`aria-pressed` 正确更新，保留焦点。
3. Chromium axe-core 4.12.1 在 390 和 320 下执行 WCAG 2A/2AA：均 **0 violations，0 incomplete**（25 passes/次）。该自动化不等于独立 Human 原生辅助技术测试。
4. 合成 HTTP503 的 Attention 显示错误、不会显示旧事实卡片，且原 Reviewer 明细可展开；合成空页显示如实空态；移除 `work:read` 权限后 Attention 完全不渲染任何记录或可执行 Reviewer 按钮，原始 Audit 也未开放。权限测试验证的是**前端呈现**，真正 401/403/404 源 API 隔离另由 QUAL-34 真实隔离 PG 集成测试覆盖。
5. 合成服务运行结束后已真实停止，临时 loopback 5178 不再监听；没有写默认 Qual PostgreSQL、上传真实凭证、启动 Agent 或使用 Human 登录码。

## 未完成的边界

- 真实默认 Goose17 上仍未部署 QUAL-34/35；浏览器测试使用模拟 JSON，**不等于真正不同 Human Principal 登录或全量真实 Run/Event 正向验收**。
- 仍需在固定公开 PR Head 上完成 CI、真实授权浏览器单次 E2E、权限撤销后的数据展示回归和独立代码 Reviewer；不可将该局部测试标为完整生产安全认可。
- 当前正式 PR #1/#2/#3 和相关高风险发布门禁不在本次合并范围。QUAL-35 保留 `In Progress`。
