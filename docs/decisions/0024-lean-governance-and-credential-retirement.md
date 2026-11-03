# ADR-0024 · Qual 治理轻量化：Agent 自治、分层验收和免查 UUID 的凭证收尾

- **状态**：Accepted for bounded local implementation（2026-11-02）
- **范围**：受控本机 Human/Operator 与外部 Agent 的日常治理；**不是**生产 IAM/OIDC/TLS、OS 级沙箱或远程自动化部署。
- **关联**：[ADR-0001](0001-product-boundary.md)、[ADR-0015](0015-human-agent-project-permissions.md)、[ADR-0016](0016-single-principal-role-evidence.md)、[ADR-0022](0022-explicit-done-gate-owner-acceptance.md)、QUAL-5/8/17。

## 问题

当前 QUAL 的正常操作需要 Human 频繁获取短期 PAT、手动寻找 Credential UUID、重复登记每条角色/测试/评论，甚至因一次性 Secret 不可读而误以为无法撤销。此前自举流程将 23/28/34 Tools 的历史数量与日常验收混用、旧版安全脚本把合法 `work:review` 误判为越权。让每个低风险任务都单独等待完整 VoiceOver/备份恢复/Human 验收，导致正常开发无法自动收尾，审计报告又越来越多。

这些问题应**减少操作次数**，而不是削弱核心授权和不可变历史。

## 决定：三层治理

1. **自动化工程检查（每次实际代码变更）**：由外部 PI/CI 实际执行测试，通过现有 CLI/MCP/REST 登记真实 CaseVersion/Run/Attempt/Git/S3 事实；不预填 Passed。项目后续可增设 *单个外部客户端提交证据* 功能，但必须复用已鉴权 use case、原子幂等及审计，不在 Qual 运行测试或 Agent。
2. **项目 Owner 自主签收（普通风险）**：已存在的 `owner_acceptance` + `work:review` Credential Scope + Project Allow + 实际 WorkItem owner/assignee + 真正 TestRun/Ready Artifact + CAS + 明确 `acknowledge_self_review`，允许授权 Human/Agent 自主完成；绝不伪称独立 Reviewer。存在最新 `needs_work` 和未过验收条件时先整改，不为了减少 backlog 绕开事实。
3. **按风险验收，不堆叠专项工具**：普通变更检查语义化、基础键盘/焦点、自动无障碍规则和实际业务测试；真实跨身份权限/审核、必要的端到端与运维恢复按相应业务风险验收。macOS VoiceOver 与 Chrome 原生 400% 缩放仅在明确的无障碍用户需求、产品承诺或发布专项要求时测试，**不是普通 WorkItem 的强制 Done 门禁**。高风险仍强制独立审核，未执行的测试绝不标 Passed。

## 本地 Operator 凭证清理：不依赖旧 Secret / 手动 Credential ID

新增 **`qual-admin retire-superseded`** 作为可选的离线 Human Operator 命令（只有可信环境拥有 `DATABASE_URL`）。这不是 Agent CLI、MCP Tool 或服务端公开端点：

- 参数只需要显式 Workspace、Project 及本地**当前正在使用的 Agent PAT 文件路径**；不需要手动输入 Principal UUID、旧 PAT、Credential UUID。始终默认只读预览；真正撤销还需显式 `-apply`，不会因为用户说“继续”而在 PI 宿主代运行。
- 从私有普通文件读取当前 PAT **仅作 SHA-256/数据库匹配**，从不输出、保存或上传 Token；目录必须是本 UID 私有 0700、文件必须是 owner 0600 且不是 symlink。当前 Credential 必须未撤销/未过期、Subject 是有效 Agent、确实属于指定 Workspace/Project。
- 在数据库事务中锁定当前及同 Principal 的候选，**只退休同一 Workspace/Principal、早于当前、仍有效、Scope 为当前 Scope 的子集**的旧 Credential。若有更晚/同时间的另一有效凭证、旧凭证拥有新凭证没有的 Scope、缺少项目成员关系、私有文件不安全、数据库状态有歧义，则 **fail-closed**，不能部分撤销；预览列 UUID/Scope 与计划数量但不显示任何 Secret。
- `-apply` 针对当前清单执行单个 PostgreSQL 事务；再次执行应安全返回 0 个候选。旧凭证哈希不可复原，所以**不能在未保存旧 Token 的情况下假称实测旧 HTTP401**；可记录数据库 `revoked_at` 元数据与新凭证现时合法 GET200。真正旧401 可通过受控测试专用旧 Credential 在隔离数据库完成，真实默认环境只有 Human 持有旧值才能重测。
- 离线 DBA 操作**不包含在线 Human AuthN Actor**；必须记录 Operator 终端执行时间/批次和计划内容用于交接，不伪造 User Audit / Reviewer。仍保留令牌轮换的 **先签发→新连接通过→再撤销旧凭证** 的故障回退顺序，绝不在签发失败时撤销旧 Token。
- 该命令不会自动授予 `audit:read` / `work:review` / `access:manage`，不会修改 `Project Allow`，不会更换正在运行的 PI 环境或隐式启动 Agent/CI。线上密钥管理、强隔离 Operator 和全自动安全轮换是另一个经过独立审计的交付阶段，不把新命令标为“完整一键轮换”。

## 可观察性和验收

- 新命令只接触 `qual.credentials` 的特定 Principal 记录，不修改 WorkItem、Run/Attempt、Audit/Activity/Outbox 或 S3；Unit 覆盖 Scope 关系、时间和错误路径，隔离 PostgreSQL 的集成测试验证预览不改变数据库、撤销后旧401/新200/幂等重试、不泄露 Secret、无法撤销当前有效凭证。
- 日常使用只需要 Operator 执行“预览→明确 `-apply`”两条相同的命令，而不再手工查询 UUID 和尝试找回只展示一次的旧 Token。
- 业务事件仍通过真实授权客户端写入。Human/Operator 反馈仍需真人填写，不把脚本输出代替真实用户。
- 未配置 OIDC/TLS/HA/生产 Operator Metrics 时仍如实标记对应生产就绪项 Pending；未执行的 VoiceOver/原生 400% 仅记录“按需专项未验证”，**不自动阻塞普通工作项**。真实权限拒绝、证据和审核独立性门禁不因本次简化而降低；不为了清空任务无依据地改成 Done。

**2026-11-02 用户纠偏（实施生效）：**上述轻量验收原则已同步修订正式 QUAL-14、QUAL-17 的第3条结构化验收标准，经原真实 PI 的 `work:write` 与版本 CAS 更新，并以真实评论记录修订原因；历史测试、Reviewer `needs_work` 和签收事实保持不变。避免再把 VoiceOver/400% 作为所有任务的共同阻塞项。

## 后续边界

后续可在 Agent 侧增加一个本地**受限 evidence submit** 聚合操作（先核查 Git/TestRun/SHA 来源、再按现有 REST 各 use case 逐项写入），减少重复 CLI 命令；保留天然不可能跨 API 整体原子的事实，不对外宣称一个请求完成“精确一次测试执行”。此项不属于第一批 `retire-superseded` 交付，按独立 PR/Commit 实现并验证。

**后续阶段实施补录（2026-11-02）**：外部 CLI 新增最小 `qual test report`，将“创建单个已发布 CaseVersion 的 TestRun → 追加一次真实观察 Attempt”组合成一个命令；可选 `-work-item` 再将同一 TestCase 关联到 WorkItem Coverage，满足现有 Done Gate 对 TestRun/WorkItem 关系的验证条件。它顺序调用最多三个现有受权 REST 用例，各自的持久化事务、Audit/Outbox 和 Idempotency 均不变；稳定 `-request-key` 推导各阶段不同请求键，失败回执必须明确指出可能已完成的步骤，并允许原样重试。它不执行测试、不自动产生 Bug/Reviewer/Done、不冒充跨 API 原子事务；Git 提交可填入 Run 的 `-build` 外部参考。

**Phase 13 实施补录（2026-11-02）**：外部受限 CLI 新增 `qual evidence packet`，在实际 WorkItem 上将本地已存在的 Git Commit 与同项目已保存的 TestRun/Attempt/CaseVersion/Coverage 组合为一个确定性、无时间戳的 JSON Manifest，使用现有三个 S3 Artifact 阶段上传并关联 WorkItem。CLI **只读** `git cat-file -t` 验证本地 commit 类型并要求与已登记 TestRun `build_ref` 一致；服务端不执行 Git/测试/Agent。每阶段继续保留现有权限、幂等、不可变 Audit/Activity/Outbox 与失败重试，返回真实 Ready/SHA，不伪造 Git Remote HTTPS 引用。与 QUAL-4 任务关联，QUAL-14/17 保留 Human 详情及 E2E 验收。详见 [Phase 13 实测](../engineering/phase13-work-item-evidence.md)。
