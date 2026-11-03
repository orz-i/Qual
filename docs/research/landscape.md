# 竞品与技术调研记录

> **截至 2026-10-30**，公开官方文档快照。以下分清官方可查事实与 Qual 的产品推断；不对市场规模、普遍投诉频率或用户意愿作未经验证的结论。

## 竞争产品/相邻方案

| 产品 | 官方文档可确认 | 对 Qual 的设计启发 |
| --- | --- | --- |
| Linear | 支持第三方 Agent、委派、MCP Server；强调人类与 Agent 的责任关系 | 不把 MCP 作为唯一差异；明确责任与审查，**不**复制触发 Agent 工作的委派机制 |
| GitHub Projects / Issues | 有 Issues/Projects 与自动化、官方 GitHub MCP Server | 将 PR、Commit 等作为证据和外部引用，不替代 Git |
| Plane | 支持自托管、REST API、Webhook、MCP | 自部署和开放 API 是重要基础，不是独有卖点 |
| Qase | 用例、套件、运行、测试结果、缺陷关联形成独立业务流程 | TestCase/Version/Run/Attempt 应当独立于 WorkItem |
| TestRail | API 可以创建 TestRun/结果，对接自动测试 | 使用外部 Reporter 上报结果，不需要内置测试执行引擎 |
| Beads 等 Agent 工作记录工具 | 面向 Agent 持久化工作和依赖查询（需在实施前复核具体项目版本） | 提供跨会话的结构化 Context View、增量活动与稳定编号 |

## 差异化主张（待用户验证）

假设：人类与外部 Agent 共同协作时，最难维护的不是任务列表，而是 `WorkItem → TestCaseVersion → TestRun → Attempt → Bug → Fix Evidence → Verification` 的长期一致性与可查询性。

因此 Qual 的产品优先级：**事实、关系、审计、上下文恢复、开放接口**。不以聊天界面、模型选择器、Agent 运行面板作为差异点。应在小团队真实使用时记录因追溯不完整、上下文丢失、责任模糊导致的阻塞，作为是否继续投资的依据。

## 技术核实

- MCP 2026-07-28 规范采用 stateless core 与新的 Streamable HTTP；不应把旧的 GET SSE/Session ID 当作当前强制协议。Go 官方 SDK `modelcontextprotocol/go-sdk` 支持新版本并可针对旧客户端测试兼容。
- React Router 官方将 Data Mode 与 Framework Mode 区分；因 Go 是独立后端，先采用 Vite + React Router Data Mode。
- shadcn/UI 以代码导入组件而非打包黑盒；根目录工作区通过 pnpm 管理。

## 官方来源（用于实施时复核）

- Linear Agents: https://linear.app/docs/agents-in-linear
- Linear assignment & delegation: https://linear.app/docs/assigning-issues
- Linear MCP: https://linear.app/docs/mcp
- Plane developer docs: https://developers.plane.so/
- Qase Test Cases: https://docs.qase.io/en/articles/5563704-test-cases
- Qase Test Runs: https://docs.qase.io/en/articles/5563702-test-runs
- TestRail API: https://support.testrail.com/hc/en-us/articles/7077083596436-Introduction-to-the-TestRail-API
- MCP 2026-07-28 changelog: https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/changelog.mdx
- MCP Streamable HTTP: https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/transports/streamable-http.mdx
- MCP Go SDK: https://github.com/modelcontextprotocol/go-sdk
- React Router Modes: https://reactrouter.com/start/modes
- shadcn/Vite: https://ui.shadcn.com/docs/installation/vite
- shadcn/Monorepo: https://ui.shadcn.com/docs/monorepo

## 缺口与用户调研安排

当前是桌面研究，没有真实用户访谈、使用频次或客观转化数据。下一步针对 3–5 个同时使用 Agent 和 CI 的研发团队验证：1) 是否已有工作流分散问题，2) 谁负责最终审查，3) 愿意迁移/复制哪些现有数据，4) 实际更需要 REST、CLI 还是 MCP，5) 数据主权/自部署要求。
