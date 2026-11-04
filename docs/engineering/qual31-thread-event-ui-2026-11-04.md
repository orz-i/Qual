# QUAL-31 · WorkItem Thread / Event 只读时间线实施记录

- 实施分支：`feature/qual31-thread-event-ui`，基于本机主线 `72af1b7`；该分支**不是** GitHub 公共仓库的合并发布，也没有更改默认运行的 Goose17 服务。
- 真实 Qual 工作项 `QUAL-31` 由当前 PI Principal 在版本 2 领取，依次按合法状态机进入 `in_progress v5`。Qual 仅记录任务状态，不执行 Agent/测试。
- 复用已部署的 WorkItem→Thread、WorkItem→Event、Thread→WorkItem 三个只读 REST/OpenAPI 路径和既有 Project WorkItem 详情页，未新增 API、权限、数据库或执行入口。
- 详情新增 Thread 来源、来源引用跳转、展开反向 WorkItem 关联，以及按 Qual 接收时间倒序的 Event 事实时间线；来源时间与接收时间区别显示，时钟偏差/延迟给予非权威提示。所有外部文字通过 React 文本节点呈现，不执行指令或 HTML。
- 仅已确认 `work:read` 时发起查询；后端维持 Project/WorkItem/Thread 404/403 隔离和分页游标。前端额外限制外链为无凭据/无查询片段的 HTTPS 公共域名，`target=_blank` 和 `rel=noopener noreferrer`；反向链接仅指向同项目已返回的 WorkItem。未核验外部服务内容真实性。
- 真实测试：从独立 worktree 运行原有本机已安装的 TypeScript `tsc -b`、Oxlint、Node 原生前端单测（**28/28 Passed**，含 2 条新的外链/时钟行为测试）、Vite Build，全通过；`git diff --check` 通过。原先试图经 pnpm 运行脚本被 pnpm 对 symlink node_modules 的安全保护阻断，改用现有安全的编译器直接运行，未关闭 pnpm 安全机制。
- 尚未执行：真实授权浏览器 320/390px/键盘焦点、真实 Thread→WorkItem/外部 Event 的完整 UI 正反向 E2E、跨项目和权限撤销浏览器回归。测试通过仅证实上述实际范围。无真实 Human 独立审核，不申请 Done。
