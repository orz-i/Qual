# ADR-0014：Phase 7 使用 Qual 管理 Qual 自身开发（PI Dogfooding）

- **状态**：Accepted（Phase 7 历史；其 Human 独占 Review 策略已由 [ADR-0015](0015-human-agent-project-permissions.md) 扩展；真实用户已注册 PI、仍待 Human/委托 Agent 审核）
- **目标**：将默认本机 Qual 服务中的 `QUAL` Project 作为自身 Task、Bug、TestCase、Attempt 与 Evidence 的权威工作档案；外部 PI 自行执行代码修改，由受控 CLI/MCP 将事实同步到 Qual。
- **不可改变的边界**：Qual 仅是任务/测试/证据存档器，不能启动 PI、模型、测试或 CI；Webhook 不能被当作 Agent 调度。

## 身份、接入与最小权限

- 离线 `qual-admin init` 创建 Human Owner 与 `qual-self` Workspace / `QUAL` Project；一次性 Admin bootstrap PAT 仅用于初始 REST 种子数据，成功后即撤销并删除本地文件。Human 后续通过离线五分钟一次性 Browser Code 登录，不向 PI 提供 Admin PAT。
- 为本地 PI 额外创建独立的 `agent + project_member` Principal（有效 7 天），仅授权 `work:read/write/claim`、`test:read/write` 与 `artifact:read/write`。Agent **不能**使用 `work:review`、`archive:export`、`webhook:manage`，不能直接访问 PostgreSQL/S3 长期凭据。
- 试点阶段**PI Agent PAT**以用户个人目录内 `0700` 目录的 `0600` 文件暂存，绝不写入 Git、Pi 项目设置或命令行参数。Admin bootstrap PAT 不长期留存。PI Agent 与 Human 在同一 macOS 用户下有相同文件/终端/Docker 访问权，**0600 不构成对本地 PI 进程的 OS 级隔离**；真正不可信的 Agent 需单独受限 OS 账户/容器且禁用 Docker/离线管理凭证。生产推广前迁移至系统 Keychain/专属密钥管理器。
- 由用户在 PI 中自行注册：一份本地 Pi package（轻量命令 + 专用 Skill）、一个独立 stdio MCP server 和一个有显式 scope 的 CLI wrapper。项目内不自动修改 `~/.pi/agent` 或 `.pi/mcp.json`，不替用户做信任确认。
- Pi 插件仅提供连接状态与入口提示；23 个真正业务 MCP Tools 继续由 `qual-mcp` 提供。CLI 是人工/文件上报辅助入口，不在插件内另造重复工具。
- MCP 和 CLI 通过同一个 `QUAL_TOKEN` + REST 到 Qual Application Service；Skill 为引导指令而非具备权限的可执行代码。

## 自举约定

1. 每次有真实 Qual 开发任务，先在 Qual `QUAL` Project 建立/选取 WorkItem（而不是只在聊天里记状态），并从 MCP/CLI 读取 UUID 与 Version。
2. PI 在自己宿主外部实现代码；Claim 只更改数据库记录，不会自动执行。提交前记录 Commit SHA、验证命令与结果。禁止将失败测试上报为 Passed。
3. Phase 7 当时默认 `in_review → done` 由 Human 复核；**Phase 8 ADR-0015 已支持显式授予 `work:review` 的 Human 或 Agent 审结**，原始 PI Agent Token 不会自动升权。提交不等于完成验收。
4. 后续 TestRun 只有绑定已发布 CaseVersion 才可创建，Attempt 仅记录真实外部观察。暂不预填 Passed/Failed；Evidence 附件校验 SHA-256，避免提交凭证和隐藏提示。
5. 使用稳定 `Idempotency-Key` 幂等初始化试点 Task/Suite/Case/覆盖关联；脚本重跑不得创建重复数据，不改写历史发布版本。
6. 外部 PI 首次会话需由用户确认 `pi mcp list` 的工具数、`qual projects` 仅包含授权项目、读取/领取/写评论/版本冲突、Human 审核与 Token 撤销。用户未完成注册/真实 PI 调用之前，试点状态只能是 **Seeded / Awaiting PI Registration**。

## 取舍和未实施

- 不引入 Agent Runtime、执行队列、Hook 自动修改工作项或检测模型输出（避免隐式改状态）。
- 不把 `AGENTS.md` 的任何自动提示当作身份或授权；PI 插件只做 diagnostics，不会自动调用业务写 API。
- 本机试点依赖 loopback 登录与开发密码，不能直接公网发布；真实多用户/高可用、安全扫描/数据保留另行评审。
- 真实 PI 安装和试用由用户完成，不能由自举脚本假装已登录或已执行某个模型/Agent 任务。

## 参考

- [Phase 7 安装和试点步骤](../pilot/phase7-qual-dogfood.md)
- [Phase 6 工程硬化](../engineering/phase6-verification.md)
- [Pi 专用注册包](../../integrations/pi/README.md)
- [通用 Qual Agent Skill](../../integrations/agent-skills/qual/SKILL.md)
