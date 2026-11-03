# 信息架构与 UX 设计

> **状态**：Phase 3 已实现 React 工作台/工作项看板，Phase 4 已实现测试用例库、不可变发布版本、运行结果矩阵、失败关联 Bug 和 JUnit 导入界面，均已完成本机浏览器/接口验证（JUnit 浏览器文件上传仅实现控件，实际导入由 CLI/REST 集成测试验证）。Phase 5 已实现工作项/TestRun 私有附件与外部引用、Project ZIP/Manifest 及 Webhook 订阅/投递列表的受控浏览器页面。高级拖拽、快捷键、复杂图追踪、归档恢复导入等仍为未来目标，**不能将设计目标视为已实现功能**。

## 1. 导航与路由草案

```text
Workspace
  ├── Workbench              我的工作 / 需要人审查 / 最近活动
  ├── Project
  │   ├── Work Items         看板、列表（Task + Bug）
  │   ├── Defects            Bug 专属筛选与归因
  │   ├── Tests
  │   │   ├── Cases          套件树、用例表格、版本历史
  │   │   └── Runs           运行记录、每次 Attempt
  │   ├── Traceability       工作项 → 用例 → 运行 → 失败 → Bug → 验证
  │   └── Activity           时间线与过滤
  └── Settings               项目、成员、Agent/Integration、密钥与审计
```

建议路由：
- `/w/:workspaceSlug/p/:projectKey/work`：看板/列表，通过查询参数记住 view/filter/sort
- `/w/:workspaceSlug/p/:projectKey/work/:workItemKey`：可直接分享的稳定详情 URL
- `/w/:workspaceSlug/p/:projectKey/tests/cases`、`.../cases/:caseKey`
- `/w/:workspaceSlug/p/:projectKey/tests/runs`、`.../runs/:runKey`
- `/w/:workspaceSlug/p/:projectKey/activity`
- `/w/:workspaceSlug/settings/integrations`

## 2. 交互原则

1. **高密度、低干扰**：工作项在看板/表格之间切换，绝不用图表占据关键工作空间。
2. **状态/责任明确**：显示 Type、Priority、Status、Assignee（人或 Agent）、Accountable Human（若适用），不把操作人和负责人混为一谈。
3. **来源可证**：活动与结果标明 Human/Agent/Integration、凭证所属主体、外部来源和记录时间；“外部上报通过”不等于“Qual 独立验证通过”。
4. **少跳页**：WorkItem 详情可打开 Side Sheet，但 URL 始终稳定；独立页面用于评论、依赖、测试追踪等长内容。
5. **键盘友好**：搜索、创建、状态切换、筛选的快捷键与命令面板；所有拖拽行为必须可由键盘或菜单完成。
6. **延迟与冲突可理解**：Mutation 先展示操作中或乐观更新，但 409 冲突要显示新状态并允许基于最新版本重试，不能静默覆盖。

## 3. 工作看板与详情

看板列由项目状态机定义（初期默认 `backlog → todo → in_progress → in_review → done`；可加 `cancelled`），状态标签与视觉颜色是投影而非数据库事实。

卡片展示稳定编号、标题、类型、优先级、负责人、阻塞计数（有则显示）；不在卡片中填满审计信息。支持按类型、人员/Agent、标签、优先级、Blocked、更新时间过滤，支持保存到 URL。排序与拖拽优先采用明确的字段 `sort_order`；不要依赖浮点值无限插入造成排序漂移。

详情包含：描述、验收标准、任务活动、依赖、Comment、关联用例/结果、Bug 附加字段、外部 PR/Commit、证据附件。变更时保留当前版本；转状态前校验权限及工作流。

## 4. 测试界面

- **Cases**：左侧 Suite 树，右侧可筛选表格。用例详情包括前置条件、步骤、预期、版本历史、关联工作项。
- **Publish Version**：显式发布，不可改写已发布版本；草稿与已发布版本的差异要可查看。
- **Runs**：展示运行元数据（环境、build/commit、来源、时间）和用例项的最新状态；点击进入 Attempt 列表及各次证据。
- **失败→Bug**：选择创建新 Bug 或关联已有 Bug；新 Bug 自动引用来源 RunItem/Attempt 和用例版本。
- **Traceability**：优先实现一条可读、可跳转的关联链，不把完整依赖图可视化作为 MVP 前置。

## 5. 操作状态与无障碍

每个关键页面定义 Loading、Empty、Forbidden、Not Found、Conflict、Partial Failure、Offline/Retry 状态。空状态提示可以采取的明确下一步，而不是显示虚构指标。

表格提供焦点与屏幕阅读器标签；Sheet/Dialog 必有标题与焦点管理；颜色不能成为唯一的状态区分依据。键盘快捷键不得拦截用户输入框。大列表采用分页/虚拟化，避免一次渲染全部测试结果。

## 6. 前端组织与数据策略

- React Router Data Mode 负责路由与深链接，TanStack Query 作为唯一服务端数据缓存源；Loader 仅预取 Query。
- 所有契约类型通过 `packages/contracts` 生成 TS SDK；UI 表单校验可用 Zod，但不能替代服务端校验。
- shadcn/UI 复用默认组件与语义化主题 tokens；自定义看板卡片与测试矩阵只在通用组件无法表达时实现。
- Vite 构建 React SPA；本地开发反代 Go API，生产环境由反向代理或 Go 统一提供同域入口（待部署 ADR 决定）。

## 7. 待验证的交互问题

受测者能否从工作项详情在两次点击内定位对应失败证据？能否从 Agent 的更新看出“谁做、谁负责、谁批准”？能否从用例历史还原当次执行版本？用这些任务测试验证，而非仅评价视觉偏好。
