# QUAL-31 / QUAL-33 · 只读事实失败关闭审查整改（2026-11-04）

## 缺陷及根因

PR #3 的 `WorkExternalTimeline`、`ThreadBacklinks`、`WorkRunObservability` 与 `RunReports` 直接使用 TanStack Query 的 `data.pages` 渲染外部事实。Query 在已经成功读取的页面后台刷新后若收到服务端 403/404 或网络错误，会保留先前读取的数据供缓存使用；如组件在 `isError` 下仍遍历旧 `data.pages`，则在显示错误的同时继续展示旧敏感来源。因此仅在 `enabled` 中判断权限不足以实现失败关闭。

整改只涉及 UI read projection：新增 `readableFactRows(canRead, query.status, query.data?.pages)`，仅在授权并且最近查询状态为 `success` 时呈现真实行；四类事实与分页按钮统一使用成功状态作为门禁。既有后端项目权限/记录不变，不添加任何授权、命令、审批或回写动作。

## 真实执行结果

- 在独立 `feature/qual-p1-work-observability` 工作树，使用离线锁定依赖，实际运行 `pnpm contracts:check`、`pnpm check:sdk`、`pnpm --filter web typecheck`、`pnpm --filter web lint`、`pnpm --filter web test:unit` 和 `pnpm build`，全部通过；前端 Node 单元 **32/32 PASS**，包括新加入的“成功→403/错误/撤权/加载中不显示旧缓存”负向单元测试。
- 真实 Chromium + **仅合成**授权会话和来源数据：首次加载 WorkItem Detail 时实际显示 Thread、Event、AgentRun 和历史 Report；本地只监听 `127.0.0.1:5179` 的 fixture 将四类请求改为 HTTP403 后点击“刷新观测”并重新导航回 WorkItem，真实确认外部四种缓存均不再显示，3 条权限错误提示可见。测试服务在拒绝期间确实接收到 4 个受拒读取请求。
- Chromium 合成拒绝态 `390×844` 和 `320×740` 均没有水平溢出；320px 状态下 `axe-core 4.12.1` 对 WCAG 2A/2AA 扫描报告 **0 violations、0 incomplete**（26 passes）。浏览器 Session 已关闭、临时 fixture 已停止，5179 端口不再监听。
- 浏览器 JSON/Session 都是合成，不是独立 Human 真登录；实际生产撤权并发、跨 OS Principal、GitHub 真实来源验签和生产级安全审查仍 Pending。未经独立 Review 的 PR #3 继续 Draft，本地默认受管 Goose17 没有发布变化。

## 发布边界

只更新 PR #3 已有工作项 QUAL-31/33 的 UI 数据呈现与负例测试，不以过去的 QUAL-TC-52/53 Passed 冒充本次修复测试。新测试结果须与新的精确 Git SHA 及独立 TestRun 绑定，并留存已有失败历史；本轮只完成经过实际执行的子范围。
