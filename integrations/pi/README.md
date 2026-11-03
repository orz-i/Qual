# PI 本地接入包（Phase 7 · 用户注册已验证）

本包仅适用于已信任的本机 `/Users/mini/Coding/Qual` 仓库，使用现有 Go REST 后端。本包**没有 API Token、数据库密码或 S3 Key**；不会替用户修改 Pi 设置、启动 Agent、执行测试或进行 AI 编排。

## 安装前

在 Qual 仓库根目录执行：

```bash
# 仅第一次创建受控 Qual 自举 Workspace/Project/Owner/PI Principal，
# 幂等写入真实任务/测试规范，密钥存 ~/.config/qual 而非仓库。
bash scripts/phase7_bootstrap.sh

# 不要把 ~/.../qual-pi-agent.token 的内容粘贴到 PI、Git 或聊天。
# 确认本机 Go/S3 服务就绪：
curl --fail http://127.0.0.1:18080/readyz
scripts/qual-pi.sh cli projects
scripts/qual-pi.sh cli items -project QUAL
```

最初自举数据包含六个 WorkItem 和三个已发布的测试规范，**初始化时没有 TestRun/Attempt**。目前真实 PI 自测已额外发布 `QUAL-TC-4 v1`，`QUAL-TC-1` 的连接测试与 `QUAL-TC-4` 的权限安全测试各有 1 次实际 Passed Attempt；`QUAL-1/2` 等待 Human Review。初始化时的 Human Admin PAT 已撤销并删除本地文件；Human 后续在**独立于 PI 的普通终端**执行 `bash scripts/phase7_human_login.sh` 获取五分钟有效的一次性码，再访问 `http://127.0.0.1:4173/login`。

## 本机 PI 已注册（以下命令用于重新安装/核查）

```bash
cd /Users/mini/Coding/Qual

# 1. PI 插件（/qual-status、/qual-pilot）和专用 Skill。
# 安装会写入本地 .pi/settings.json，Pi 会要求你批准项目可信任。
pi install -l ./integrations/pi

# 2. 将标准本机 MCP stdio Server 登记在本项目的 PI 配置。
# 命令参数及本地配置不含 PAT；凭证由 wrapper 读取私有 0600 文件。
pi mcp add -l qual-local \
  --cwd "$(pwd -P)" \
  --description 'Qual project-scoped task, test and evidence archive (no execution)' \
  -- "$(pwd -P)/scripts/qual-pi.sh" mcp

# 3. 只读检查，并手动启动/重新加载 PI。
pi mcp list
pi list
pi
```

在 PI 里手动执行 `/reload`、`/mcp`，然后 `/qual-status`、`/qual-pilot`、`/skill:qual-pi-dogfood`。

**标准 MCP**：Goose13 受审新二进制 `bin/qual-mcp` 通过真实 stdio tools/list 返回 **34 个工具**，新增显式 Done Gate、工作项级审核政策、Owner自审与真实签收历史相关能力。旧版28工具为之前Goose12历史。**当前已运行PI会话必须由用户手动执行 `/reload`（或重启PI）才可能刷新MCP注册工具；不能把复制新二进制误称已更新旧会话。** 同一个 PI Principal 可以通过 `work_role_entry_record` 按职责存档执行/测试/复核/审计；服务端保留真 Actor 和自我复核标识。Pi 使用其内建 MCP 支持，工具名带 `mcp__qual_local__` 前缀；插件不会再次注册 MCP 或数据库 API。

**CLI**：无需全局安装，使用 `scripts/qual-pi.sh cli` 作为有作用域的命令入口。可按需在 PI 中人工运行，例如：

```bash
scripts/qual-pi.sh cli projects
scripts/qual-pi.sh cli items -project QUAL
scripts/qual-pi.sh cli show -project QUAL -item '<uuid-from-real-list>'
```

**真实测试结果单条归档（Phase 12）**：外部 PI 实际运行测试后，使用 `scripts/qual-pi.sh cli test report -project QUAL -case-version <已发布UUID> -title <真实运行名称> -status passed|failed|blocked|skipped -note <实际观察> -request-key <稳定事件键> [-build <真实 SHA>] [-work-item <同项目WorkItem UUID>]`。CLI 只登记已发生的外部测试事实，不运行测试；重复调用必须保持请求键和输入不变，部分失败可原样重试，单次命令返回 Run/RunItem/Attempt 的真实 UUID。

**Phase 13 Git/TestRun/WorkItem 证据包**：真实外部 Git Commit、TestRun/Attempt/Coverage 已有后，`scripts/qual-pi.sh cli evidence packet -project QUAL -work-item <UUID> -test-run <UUID> -git-commit <full SHA> -git-repo . -request-key <stable key>` 会在现有权限下生成 WorkItem 私有 Ready Artifact 和 SHA 回执。按相同键重放不重复新增；无真实 Remote 不捏造链接，关联的正式实施任务是 `QUAL-4`。

PI extension 插件只做本地连接诊断，不持有 Token、不自动 Claim/Transition；所有业务变更由 MCP/CLI 和 Project Policy 控制。Phase 9 可用**一个真实 PI Agent**按任务阶段附加不同职责的不可变证据（参见 `docs/pilot/phase9-single-agent.md`）；原 PI 凭证未自动获得 Reviewer/Auditor 高权。旧 PI 交互会话需 `/reload` 才能看到新增工具。

## 保护与退出

- 仓库内的 `.pi/`、`bin/` 都在 `.gitignore` 中。Pi 自己读取项目级 settings/mcp 文件前需要由你确认信任，**不要自动跳过信任确认**。
- 只有 **PI Agent Token** 留在 `~/.config/qual/` 的 0700 目录、0600 文件，有效七天且不含 Human Review、归档和 Webhook 权限；Admin bootstrap PAT 已撤销。**同一 macOS 账号下的 PI 若启用任意 bash/Docker 操作，仍能读取同 UID 的文件与访问 Docker；目前是信任宿主上的受控试点，而非 OS 安全隔离。**正式隔离需受限 PI OS 用户/容器和外置密钥管理器。
- 重启 PI 后用 `pi mcp list` 检查连接、使用 `/qual-status` 查看 API 就绪情况；工作项 UUID 以 API 实时返回为准。
- 若 Token 泄漏，立即在本机由 Human 管理员执行 `qual-admin revoke -credential <uuid>`；不要通过将某个任务改成 done 来掩盖异常。
- 移除项目级注册由你主动执行：`pi mcp remove -l qual-local`，`pi remove -l ./integrations/pi`（如版本支持该路径），检查 `pi list`。移除 PI 配置不会撤销 PAT，仍需离线 revoke。
- 用户的**真实 PI 注册已通过核查**：项目包已加载，新Goose13二进制应连接34 Tools，实际 PI MCP 已完成读取/工作项变更，QUAL-1 处于 Human `in_review`。仍需用户在 PI 交互式 TUI 手动确认 `/qual-status`/Skill 行为和在 Web 进行最终 Human Review；不能把注册检查冒充完整的软件测试或生产试点。

## 模块边界

| 入口 | 用途 | 不负责 |
| --- | --- | --- |
| `integrations/pi/extensions/qual.ts` | PI 的 `/qual-status`、`/qual-pilot` 诊断与说明 | 业务 API/任务调度 |
| `qual-mcp`（stdio） | Goose13版本34个业务Tools，独立Agent PAT + 项目授权通过 REST；新会话需 `/reload` | 直接 DB/S3 管理 |
| `scripts/qual-pi.sh cli` | 查询/上报、文件上传、项目权限和职责履历 `role record/list` | 启动/编排 Agent |
| `qual-pi-dogfood` Skill | PI 开发流程与安全边界 | 执行权限或 Token 分发 |
| `qual-admin`（离线 Human 运维） | 初始化、撤销、临时网页登录码 | PI 本身的任务执行 |

继续步骤与验收见 [Phase 7 自举试点](../../docs/pilot/phase7-qual-dogfood.md) 和 [ADR-0014](../../docs/decisions/0014-phase7-pi-dogfood.md)。
