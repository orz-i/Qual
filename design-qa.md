# Phase 10 design QA (current scope)

- Source visual: user-selected C / Light Brand board (ChatGPT attachment, local copy unavailable inside Mac repository; concept is a multi-panel design board, not a 1:1 page screenshot).
- Implemented: real Qual UI (visitor home), responsive project shell and tokens.
- Screenshots: docs/design/phase10-c-light-brand/01-guest-desktop.png (1440x900 CSS / dpr1), 02-guest-mobile.png (390x844 / dpr1), 03-mobile-navigation.png (390x844 / dpr1).
- Functional checks: desktop navigation semantics; mobile menu opened and aria-expanded=true; browser page errors: none. TypeScript lint/typecheck/build, Go test/vet passed.
- Visual themes: selected pale lavender/white layout, strong violet action button, neutral readable typography. No imagery expected on guest page. The selected board is conceptual and contains fictitious data; real UI intentionally does not reproduce it.
- Typography: Geist Variable with CJK system fallback; 14px base text, 24–30px headline. Desktop hero title currently wraps awkwardly near the end; refine after authenticated screenshot baseline.
- Spacing/layout: responsive sidebar 232px on desktop and off-canvas drawer on mobile; no viewport horizontal scrolling in the observed desktop screenshot. Mobile hero extends below first fold, which is expected.
- Colors/tokens: near-white #fbfafd, accent #5145dc, sidebar #f7f6fd, semantic colors. Full WCAG contrast has not yet been independently measured.
- Copy: no fabricated KPIs/people/test results. Visitor copy clarifies Qual does not execute Agent or CI.

## Critical gaps
- **P1:** No authenticated workspace screenshot: browser session remains unauthenticated and single-use Human code was not retrieved, as it must not be exposed to PI.
- **P1:** Selected C design board is not an exact guest-page target; same-state/same-viewport image-to-code visual diff cannot be scored fairly. Need a focused authenticated project screen mock followed by side-by-side browser QA.
- **P2:** Authenticated multi-project nav, role/permission, work item transition, and full mobile touch behavior not exercised.
- **P2:** Contrast, keyboard traversal, screen reader and 400%-zoom audit still pending.

## Next checks
1. User/authorized Human enters one-use login code directly in local browser.
2. Capture authenticated work item/QA screens in desktop/mobile and compare to focused C mockups.
3. Test primary navigation/transition/feedback and collect console/network outcomes.
4. Resolve P1/P2 issues, then change final result only after a verified re-capture.

## QUAL-13 — 新增列表/看板视觉审查（本轮）

- 来源：用户明确选定 C / Light Brand **多屏设计概念板**，并非可逐像素匹配的工作项页面。
- 实际浏览器：`04-board-fixture-desktop.png`（1440×900，1 CSS px / 1 image px）、`05-kanban-fixture-desktop.png`（1440×900）、`06-board-fixture-mobile.png`（390×844）、`07-kanban-fixture-mobile.png`（390×844，聚焦并用箭头滚动后）。
- 状态：项目工作项使用真实受限 PI API 快照，**Human 登录会话为视觉夹具 mock**；所有写动作未执行。不能宣称真实授权浏览器通过。
- 字体：沿用 Geist Variable + CJK 系统字体。标题、表头、列表密度符合 C 方案强调的简洁信息层次，无装饰性花哨背景。
- 版式：侧栏 232px、内容自适应；列表信息的优先级与 C 概念一致，移动搜索宽度 P1 问题已修复。
- 配色：现有 C 主题 token（白/淡紫/蓝紫主按钮、语义状态色）保持一致。
- 图像：此视图主要为真实项目数据，不包含必须另行生成的插画或照片。Logo 使用已有 Q 标识；不复制 C 概念的虚构头像/图表。
- 文案：保留“已加载”、对搜索范围的明确说明；不谎称完整项目统计或真实 Agent 活跃度。
- 首次截图发现的 P1 修复：390px 时搜索框被压缩、首列空 Backlog 挤掉活跃列。修复后重新截取 05/06；视觉证据在本轮审计文档。
- 交互：URL `view` 与 `q` 跨视图保留；移动看板可键盘滚动；弹窗打开/Esc 关闭；浏览器错误为 0。
- 自动化无障碍：390px 下 List 0、Board 0 violations（axe-core 4.12.1），不代表全 WCAG 合规。
- **剩余 P1 阻塞：** 概念图不是同屏同尺寸的对照图，且当前没有共享真实 Human Cookie 的浏览器完整功能截图。任务 QUAL-9/17 继续补齐，禁止宣称图像到代码 1:1 QA 通过。

## QUAL-14 任务详情可视与权限模拟复核

- 源视觉：用户已选择的 C / Light Brand **多屏方案板**，不是精确同屏样稿；仅做方向与信息层次比较。
- 实现截图：`08-detail-fixture-desktop.png`（1440×900）、`09-detail-fixture-mobile.png`（390×844）、`10-detail-privileged-fixture-mobile.png`（390×844，**模拟**授权状态）。
- 字体：沿用 Geist Variable + 中文系统回退，详情主标题和角色履历有明确视觉层级。
- 版式：桌面左右内容/属性两列，移动端单列，390px 页宽没有额外水平溢出。
- 色彩：保持已选 C 主题的浅灰白/淡紫和蓝紫主操作；深色语义状态结合文本。
- 资产：工作详情未用虚构 Agent 头像、测试图表或生成插画；保留真实 Qual 编号、文案和可追溯证据。
- 文案：没有测试覆盖就说明没有记录，不虚构通过率；无权限审核提示明确。
- 首次桌面扫描发现侧栏小号文字 **3 个对比度违规节点**，经 `51a8db6` 修复后再次扫描为 **0 violations**（20 条通过）。
- 模拟只读与模拟高权权限状态的按钮显示已分别检查；没有任何授权写操作在浏览器夹具执行。移动端 axe 自动检查亦为 0 violations。
- **P1 后续**：缺少真实 Human 浏览器授权会话，同屏设计像素基线也尚未建立；自审标记未在实际记录中出现，不能视为自审视觉验收。
- 详尽证据：`docs/design/phase10-c-light-brand/qual14-verification.md`。
## Phase 10 QUAL-13 / QUAL-14 二次整改后的设计 QA 补充

- 来源：用户已选定的 C / Light Brand 多屏概念板（仍非同页同状态精确对照），**不能以此宣称 1:1 视觉 QA 通过**。
- 浏览器证据 11（390×844，dpr=1）：只读模拟 Human 会话，看板以真实限定 PI API 工作项快照展示负责人「Local PI Agent · Agent」，没有“新建工作项”，可查完整 UUID。
- 浏览器证据 12（390×844，dpr=1）：详情使用真实项目身份摘要显示类型与名称，返回 URL 保留 `?view=board&q=QUAL-13`。
- 浏览器证据 13（1440×900，dpr=1）：真正拦截前端 PATCH 并模拟 HTTP **409**；草稿仍在编辑框，保存按钮禁用；模拟读取 v11 新状态后提示核对，未选复选框时禁用，复选框选中才可继续提交，**没有任何真实 PATCH**。初次试验发现已有 mock 路由会返回 200，故废弃该试验，重新以页面初始化 fetch 拦截实现正确 409。
- 浏览器证据 14（1440×900，dpr=1）：**明确合成的 self_review=true** 只用于视觉验证自审标识，页面文字说明非真实 Qual 审核；Go 隔离集成测试另外验证了真实 Principal 自审结果与独立 reviewer 区分。
- C 色彩、信息层次、真实姓名/身份标签、缺陷/角色可追溯语义得到改善。没有虚构进度、测试结果或 Agent。
- 实施中的一次新的 serious 语义错误：复制 UUID 按钮在 `<dl>` 的错误定义组内。已移动按钮到 `<dd>` 内；复测 axe-core 4.12.1 desktop 20 passes / 0 violations / 0 incomplete。
- 已复测模拟看板 1440px，axe 26 passes / 0 violations / 0 incomplete；浏览器 console/page errors 0。
- **未解决 P1**：PI 浏览器无用户实际 Human Cookie，无法做真实登录后权限表单的端到端写入验收。后端真实 403、409、撤销、自审由独立 DB 集成测试证实，UI 行为仍仅在模拟 Session 中检查。
- **未解决 P2**：当前模拟测试无法替代全链路 400% 缩放、读屏及同状态视觉源对照。真实大于 100 工作项用户搜索体验待实际数据验证。
- 本轮已完成的代码整改可供授权审查，**final result** 仍必须 blocked，直到所述真实 Human 场景补齐。

## Phase 10 QUAL-13 / QUAL-14 Human 浏览器补充审计（第四轮）

- 真实 Chrome Human 登录看板和项目权限页面可见：截图 `16-live-human-before-interaction.png` 与 `17-human-access-screen.png`。权限页显示 Qual Local Owner 是 Workspace Admin，拥有“审核并完成任务”能力；现有 PI Agent 独立且不具备该能力。
- 真实 Qual API 拒绝 PI Agent 对 QUAL-13/14 v13 的越权 `done` 请求（均为 HTTP 403 PERMISSION_DENIED）；两项任务的状态与版本都保持 `in_progress v13`。
- Chrome AppleScript 页面脚本执行被禁用；System Events UI 点击缺少 macOS 辅助功能权限；未在真实 Human 会话完成 409 恢复、400% 缩放、屏幕阅读器或授权写入，不能宣称完全通过。
- 独立 Reviewer 尚未写入有效审核事实。详细范围及限制见 `docs/design/phase10-c-light-brand/qual13-14-human-browser-audit.md`。

## Phase 10 QUAL-13 / QUAL-14 第五轮：真实 Human 登录端到端验收

- 使用真实一次性登录码通过自动化浏览器登录，并经同源 `/auth/session` 和 `/access/me` 返回 200 核实真实 `Qual Local Owner · Human` 和 `work:review`；未保存登录码、Cookie、PAT 或 CSRF。
- QUAL-13：列表/看板 URL 搜索与服务端状态筛选、详情返回、390px 响应式与方向键滚动均在真实 Human 会话验证；移动 axe 26 passes / 0 violations。
- QUAL-14：**真实**跨 Principal 并发验收：PI 合法评论将工作项从 v14 更新至 v15，Human 旧版 PATCH 被服务端拒绝，界面保留草稿、刷新 v15 并禁用未经核对的提交；原标题和描述未修改。截图 `23-real-human-409-draft-recovery.png`。
- 两页均实际启用 Chrome 网页缩放 **400%**（DPR4、物理1440→CSS360px）：文档没有整体横向溢出，看板仍可键盘内部滚动，axe 均为 0 violations；验证后恢复100%。截图 `24`、`25`。
- **未通过完整验收的部分：** 尚未使用真正 VoiceOver 等屏幕阅读器朗读并完成全键盘路径；无真实只读 Human UI 和 100+ 工作项分页压力验证。现阶段不能将 axe 自动扫描等同读屏通过，或把 Reviewer 记录直接等同 done。
- 独立 Human Reviewer 现在已由真实 `Qual Local Owner` 会话写入，PI 从真实不可变履历读回：QUAL-13 `reviewer/needs_work` v15、QUAL-14 `reviewer/needs_work` v16，均 `self_review=false`，审计证据 `7fc7309`。这是独立复核**未通过**，不是 `reviewer:passed` 或 `done`，无权冒名亦未发生。
- 详细证据与命令见 `docs/design/phase10-c-light-brand/qual13-14-human-e2e-audit.md`；当前正式 Design QA 继续阻塞。

## Phase 10 QUAL-15 测试运行质量摘要与缺陷追溯 QA

- 选定的 C / Light Brand 视觉方向继续沿用白/淡紫、明确状态文字和清晰信息层次；当前没有与这两张 TestRun 页面同尺寸、同数据的用户批准的 1:1 静态对照，因此仅确认方向一致，**不声明像素级一致**。
- Browser screenshot 28（1440×900）：真实 PI API 中六条 TestRun 只读快照；均最新 Passed，每条 1 RunItem，0 未上报，不展示任何假失败；按“有失败”筛选得到本地空结果。
- Browser screenshot 29（1440×900）：真实 QUAL-4 TestRun 只读快照，1 Passed/1 上报、当前证据附件可见；前端没有测试运行/导入/新建 Bug 写权限控件；默认收起 SHA/CaseVersion，真实 Attempt 历史可手动展开。
- Screenshot 30/31：**仅浏览器合成夹具**。显式显示“非真实 QUAL Run”；最新五状态各1，用例失败筛选 → Failed Attempt → MOCK-BUG-1；后来 Passed 仍能从早期 Failed Attempt 打开 MOCK-BUG-2，和最新 Passed Attempt 并列存在。没有任何模拟数据写入 QUAL 项目。
- Screenshot 32：320×800 viewport，documentWidth=320，无页面级横向溢出；模拟测试页可以浏览/扩展。当次 axe-core 4.12.1 23 passes / 0 violations / 0 incomplete，console/page errors 0。
- 初次详情 axe 发现 aria-label 在无 role 的 div 上导致 1 incomplete；质量统计容器加 role=group 后重新扫描 23 passes / 0 violations / 0 incomplete。
- 受控 Go 临时 PG 的真实 HTTP 集成测试验证 401/404/只读读取、失败 Bug Link、Later Passed 不覆盖早期关联及原 Phase4 不可变测试规范；临时 DB 自动删除。
- 合格门禁：Go test/vet, Web lint/typecheck/build, contracts lint/generate/sdk, quality Node 3/3、work Node 4/4、Python 18/18。
- **未完成**：真实 Human TestRun 写入/权限拒绝后的恢复、完整键盘+VoiceOver、真实浏览器 400% 此新测试页面的回归。QUAL-13/14 的真实独立 Reviewer needs_work 仍有效。本轮 QA 只代表已测范围，**总体 final result 仍为 blocked**。

## Phase 10 QUAL-16 C 风格协作审核队列 Design QA（本轮）

- 选定方向：用户批准的 C Light Brand 多屏概念板；本轮视觉实现沿用现有设计 Tokens、字级、密度与侧栏。不存在可同页同数据像素对照的原始设计图，因此**不宣称 1:1 还原通过**。
- 实际浏览器截图 33（1440×900）、34（1440×900）、35（320×800），数据来自 PI 合法 `work:read` 下真实 QUAL review queue 与真实 Principal 名称，Human BrowserSession 为仅本地视觉测试的模拟对象。呈现 3 in_review + 2 needs_work（QUAL-13/14）；没有不具有权限却可点击的 Reviewer 操作。真实 PI 的 `GET /audit` 返回 403，UI 未发起原始 Audit 请求。
- 视觉截图 36/37/38 为**明显合成状态**：模拟 Reviewer+Auditor 权限显示证据链接和可见审计摘要；模拟 audit-only 显示审计但不发起 WorkItem queue 请求；模拟同一 Principal Reviewer 记录显示「非独立批准」，这些均**不是**真实 QUAL 角色或审计事件。
- 语义、排版、色彩：明确分隔“等待审核”“审核要求整改”、审计与工作履历，深浅语义均含文字；无装饰性虚构数据。负责人和 Reviewer 真实 Human/Agent 类型由受限 Principal API 来源决定。
- 布局：390 CSS px 及 320 CSS px 的 WorkRead UI，文档宽度等于视口；原始 Audit-only 与特权模拟 UI 未发现页面级错误。
- 功能：分类 tab `aria-pressed` 正确更新；Reviewer 的权限只提供导航到实际 RoleEntry 卡片/工作项详情，不提供队列里的快捷 Done，审核记录仍需真实版本/CAS/证据；非快照分页的“已加载”范围明确。
- axe-core 4.12.1 自动检查：只读桌面/320px 21 passes、模拟 Reviewer+Auditor 23 passes、模拟 Audit-only 20 passes；全部 **0 violations、0 incomplete**；并不能证明完整 WCAG 或真正 VoiceOver 验收。
- **Remaining P1:** 无当前授权 Human BrowserSession 的真实 Review/Access/Audit 角色切换检查，未做 VoiceOver 与完整键盘焦点顺序、真实 400% 缩放或更大项目并发 offset 复核。原 QUAL-13/14 真实 Human Reviewer `needs_work` 结论保留。
- 具体门禁与证据：`docs/design/phase10-c-light-brand/qual16-verification.md`、原子 Git 提交；最终 Design QA 仍保持 `blocked`，不自动结案。

## QUAL 全项目 Human 审计补充（17/17）

- 真实 Human 登录完成对 17 个 WorkItem 的状态、角色、测试、依赖、证据和最新审核队列交叉核验；测试/权限页面在390px无页面级横向溢出，真实 Chrome 400% 下审核队列和 TestRun 列表亦无页面级横溢，axe自动0 violations。这不代表 VoiceOver 或完整 WCAG 通过。
- 从 Human 浏览器下载并哈希比对 3 份 Ready 附件均一致；Go Race/Unit/Vet、Web lint/typecheck/build、OpenAPI 契约和9个专项前端测试通过。Go 宿主版本 1.27.1 尚未达到 README 要求的1.27.3+。
- 最新 Reviewer：QUAL-7 独立 Human passed（仅本机Phase8工程范围），QUAL-8 独立 Human needs_work；QUAL-4 Human 为原创建者，旧 passed 是 self_review=true，现已通过追加 needs_work 显式纠正，两条都不构成独立批准。QUAL-13/14 原独立 needs_work 继续有效。所有任务都没有本轮自动变成 Done。
- 结构化验收缺口：QUAL-9～17 acceptance_criteria 均为空，全17项没有正式 WorkItem Dependency；QUAL-4/8 最新需要整改却仍在 in_review，队列可能误导。具体所有17项审计见 docs/pilot/qual-all-work-items-audit-2026-11-01.md。
- 尚需 VoiceOver/全键盘、只读 Human、真实完整 Failed→Bug→later Passed 试点及生产门禁；本机独立审查范围不能外推为完整 UX/生产验收。

## QUAL 当前待审查队列审核处置（2026-11-01）

- 真实有权 Human 先前对 QUAL-7 的独立 `reviewer:passed/self_review=false` 已经登记并可追溯。本次用户重新提供一次性登录码，实际 Human Session 通过 `work:review` 授权执行 `in_review v8 → done v9`；Human Actor 的真实 Audit Transition 已核实，限定为本机 Phase8 工程范围。
- QUAL-4 最近 Reviewer 为 `needs_work/self_review=true`，早期 `passed` 也属于自审；真实 PI Agent 按 `work:write` 将 `in_review v10 → in_progress v12`，保留独立 Reviewer 缺口。
- QUAL-8 有独立 Human `needs_work/self_review=false`；真实 PI Agent 将 `in_review v11 → in_progress v13`，仍需同一真实 PI Principal 在默认项目完成合法 Scope/Allow 的 Reviewer/Auditor/撤销闭环。
- 本轮重新执行 Go/Web/契约门禁与审核队列专项测试。正式待审查队列已清空；QUAL-4/8/13/14 仍在整改队列，不会被当成完成。详见 `docs/pilot/qual-pending-review-audit-2026-11-01.md`。
- Go 开发宿主 `1.27.1` 未达要求 `1.27.3+`，且全项目 VoiceOver / 真实只读 Human / 完整 E2E 等仍未结束。因此仅 QUAL-7 本机工程工作项正式 Done，**总体 Design QA 不解除阻塞**。

## 2026-11-01 P0 审计整改 — 服务端审核分桶、结构化验收和部分真实 E2E

- 不改写原有 Reviewer 历史（QUAL-4 原 Human 创建者 passed/self_review=true；QUAL-8/13/14 独立 reviewer needs_work），不将已历史核验或 PI tester passed 等同于任务 Done。
- 服务端+React 根据最新 Reviewer outcome、self_review 与 WorkItem.status 判定 review_lane。真实当前 QUAL 项目 4 条 latest needs_work：QUAL-4/8/13/14，全部归为整改；旧代码缺字段的真实回归失败，修复代码同项目回归通过。ADR-0021；源代码提交 1780550 / eb0e2bc；浏览器截图 45（实际只读 QUAL 项目数据 + 隔离模拟 Human Session），axe 21 passes/0 violations/0 incomplete。未宣称真实 Human 登录验收。
- QUAL-9～17 共9个真实 WorkItem 结构化验收此前均为空；合法 PI Agent 通过受控 CAS 与幂等 REST 写入28条验收与18条依赖，重读验证；写入时版本快照在 phase10-backlog-cas-reconciliation.json，角色/注释随后会递增当前版本。未标记任何验收通过。
- QUAL-17 已领取并启动，真实本机历史源码 `051ff65` 回归实际失败 → 修复版 `eb0e2bc` 实际通过（原来未承诺新增 `review_lane`，仅说明历史源码不满足新验收契约）。在真实 QUAL 新发布 QUAL-TC-8 v1、实际 Run cf6e8712、早期 Failed Attempt、关联真实 Bug QUAL-18、后续 Passed Attempt，旧 Bug 关系保持。私有 Ready Artifact 7a9a3c87 已下载 SHA 一致。报告 docs/pilot/qual17-real-pilot-2026-11-01.md。
- 浏览器截图 46/47/48 的 TestRun/Attempt/QUAL-18/Artifact **业务事实是真实 QUAL**；用于 UI 测试的 Human Session 仍是隔离视觉夹具，不能当作 Human E2E；页面桌面和390px axe 页顶复测 23 passes/0 violations/0 incomplete。一次滚动遮挡导致扫描 incomplete，复测前先回到页顶，不将 incomplete 错报为违规。
- QUAL-10/11/12 已按合法 Principal 领取，关联原代码提交 `08d22b9` / `ba1e13f`，本轮访客桌面/390px导航与人工切换 CSS dark token 检查截图 49/50；这只证明有限范围功能，不代表该任务验收通过。
- **仍阻塞**：真正 Human/Agent 双身份协作 Reviewer 签收、真正 VoiceOver、完整键盘、400%缩放在新界面回归、真实只读 Human、100+ 分页、QUAL-8 同一个 PI Principal Reviewer/Auditor 最小授权循环、QUAL-5 PAT 运维轮换；Mac Go 1.27.1 低于项目要求 1.27.3。总设计/试点验收不变：blocked。

## 2026-11-01 工作面板真实 Human 复审：QUAL-18 → Done，QUAL-19 独立缺陷

- 本轮用户短期码实际通过独立 Qual 自动化浏览器登录；当前 Human `work:review` 有效。QUAL-18 真实 `QUAL-TC-8` Run 的历史 Failed Attempt→Bug 关系、Later Passed、2,031-byte 私有 Artifact SHA 已实际由 Human 复核；PI 原有 `needs_work` 队列脚本重新执行 HTTP200、5条、0失败。已写入真正独立 `reviewer:passed/self_review=false`，随后授权 Human 执行 `in_review → done v10`，服务端 Audit 证实同一 Human Actor，未自动关闭 QUAL-15/16/17。
- 另发现代码级边界遗漏：`in_progress + latest Reviewer failed` 未纳入 SQL ReviewQueue，虽然 ADR-0021/领域层将 failed 纳入 needs_work。由真实 PI Agent 新建 `QUAL-19`（High/Major、`todo v2`），具有正式 Bug Details 和四项验收；**尚未在隔离PG做此条件的动态复现**，不得误记为已执行 Failed Reviewer 测试。已在 QUAL-16 留下关联评论（v14）。
- 真实面板现有19项：done 3、in_review 0、in_progress 12、todo 4。整改队列仍是 QUAL-4/8/13/14，四项以前的 Reviewer 结论保持不变。Human 看板 axe 26 passes/0 violations，但非完整 VoiceOver。
- 本机 Go仍为1.27.1，不满足1.27.3+ 发布门槛；全面 VoiceOver、只读Human、100+分页、完整QUAL-17试点均未验收，故**总体 Design QA 继续 blocked**。详细原始证据与图证见 `docs/pilot/qual-workboard-human-review-2026-11-01.md`。

## 2026-11-01 QUAL-19 independent review finding and technical remediation

- User-authorized real Human independently reviewed **QUAL-18** (reviewer passed/self_review=false) and closed that limited ReviewQueue needs_work regression as `done v10`. New **QUAL-19** is an independent Bug, not part of the already accepted scope: Query WHERE excluded `in_progress + latest Reviewer failed` while Domain ReviewLane semantics included both failed/needs_work.
- Actual PI Agent claimed QUAL-19 via legal scoped API, did **not** change real Reviewers or grant itself any `work:review`/`audit:read`.
- Isolated PostgreSQL full HTTP regression `TestPhase10ReviewQueueIncludesLatestFailedReviewer` executed against uncorrected SQL: **FAIL (expected 6 entries, got 5)**, the omitted entity is exactly `in_progress+failed`. Fixed SQL `IN ('needs_work','failed')`, reran identical integration plus Phase8/9 authorization/cross-project/revocation tests: **PASS**, also `go test -race -tags integration` PASS. Temporary DBs destroyed; no fake QUAL Reviewer failed event.
- Real QUAL published **QUAL-TC-9 v1**, TestRun `e8d7a3a9-91ea-4a58-adf3-fb583ba11019`; actual Failed Attempt `1a427739` from old integration, linked existing **QUAL-19**, later actual Passed Attempt `5a9c0618`, old link preserved. Private Ready Artifact `382191aa` uploaded and SHA verified by download. These represent actual external Go test facts, not Human signoff.
- `make check-go`, `make check-web`, `pnpm contracts:check`, Web reviews/work/quality 2/4/3 tests and Python **21/21** passed. Go host `1.27.1` remains below module `1.27.3`; release gate still blocked unless proper toolchain is verified.
- This is a code/API and isolated test verification only; **genuine Human Reviewer signoff, VoiceOver, full keyboard, 400%, 100+ real project pagination, security lifecycle still pending**. `design-qa` final remains blocked, and QUAL-19 must not be declared Done by PI.

## 2026-11-01 QUAL-13 / QUAL-17 100+ 分页与精确 Go 工具链技术回归

- 实际受限 Go HTTP 测试在**隔离 PostgreSQL** 创建126条WorkItem（125 task + 1 bug），从真实 Viewer GET分成100+26，kind task 100+25，UUID无重复，跨filter/project游标422，limit101 400，缺work:read身份403；所有临时库已删除，没有向默认 QUAL 添加假业务工作项。提交 `9a6b173`。
- Mac默认 Go 仍1.27.1，但实际下载 Go1.27.3 到 Qual项目内被忽略的临时目录并运行：`make check-go-required` PASS；精确Go1.27.3下 QUAL-19/Phase8/9隔离 PostgreSQL `-race` PASS，126条分页集成 PASS；可重复使用的工具门禁提交 `236b442`。这不构成 OS 默认 Go升级或生产发布环境验收。
- 真正 Qual 记录新增 `QUAL-TC-10 v1` / Run `da4359ba` / Passed Attempt `eea8a625`，覆盖 QUAL-13/17，私有 Ready Artifact `d1199f31` 已重新下载验证 SHA；记录见 `docs/pilot/qual17-pagination-go1273-2026-11-01.md`。
- React隔离浏览器用明确 `FIXTURE-` 前缀、仅模拟 Human Session：搜索第126条时已加载100项/匹配0，可继续加载26后已加载126/匹配1，URL与筛选保持。截图54/55/56；390px无整体横溢，axe27 passes/0 violations/0 incomplete；Tab局部路径验证输入→清除→类型→状态，**不能等同完整键盘或VoiceOver**。
- 实际 QUAL-13独立 Reviewer needs_work依旧有效，QUAL-17不具备全面 Human 签收，**final result继续 blocked**。

## 2026-11-01 真实 Human 验收续审：QUAL-19 正式通过、QUAL-13/16/17 部分回归

- 本轮用户提供的一次性登录码仅用于 PI 独立浏览器 Qual 登录，真实 Human Principal `Qual Local Owner` 经 `/auth/session` 与 `/access/me` 200 确认拥有 `work:review`/`audit:read`。没有向 PI Agent 转授权或将密钥存档。
- **QUAL-19**：真实 SQL 修复 c1b031a、隔离 PostgreSQL 原先 FAIL 预期6实际5，修复后 PASS 6/6 及 Race/权限回归有历史真实执行回执；本轮浏览器实证 QUAL-TC-9 v1 的两次不可变 Failed/Passed Attempt、旧 Failed→QUAL-19 Bug、Ready 附件 1754字节/SHA一致。重新运行精确 Go 1.27.3 工程门禁、Web/契约、2项审核队列单测与21项Python单测均 PASS。真实 Human 追加 `reviewer:passed/self_review=false`（v10），随后授权执行 `in_review → done v11`，Audit 两条工作变更是该真实 Human Actor。此为本机工程目标通过，非生产认证。
- Human `/reviews` 390px无整体横溢，原生Chrome400% DPR4/css360px无整体横溢，axe0违规；键盘Tab到整改按钮、Space切换aria-pressed成功。QUAL-13 最新19项真实Human列表/看板切换、URL搜索及详情返回、390px键盘内部滚动可核验；126条跨页真实PostgreSQL测试与浏览器夹具严格区分，不能冒称默认项目Human 100+项实测。
- 上述局部结果已由真实 Human 评论持久化到 QUAL-16（v15）、QUAL-13（v21）、QUAL-17（v16），仅为观察记录，未覆盖原独立Reviewer `needs_work`，工作项状态仍为 `in_progress`。
- 纠正QUAL-19历史开发报告中误称Human创建缺陷：实际 CreatedBy 是当前真实 PI Agent，Human为随后独立Reviewer；更正文档不改Qual历史。
- 全部项目19项当前 `done=4, in_review=0, in_progress=12, todo=3`。VoiceOver、真实只读 Human、全键盘/完整E2E、默认QUAL 100+项、多主体权限撤销及生产发布版本仍未验收。详见 `docs/pilot/qual-human-acceptance-followup-2026-11-01.md`，**总体仍 blocked**。

## 2026-11-01 QUAL-20 — 移动菜单隐藏元素与焦点循环整改（仅已测范围）

- 在本机实际 Qual 访客页面 390×844 用真实 Chrome 键盘复现：侧栏 `aria-expanded=false` 时首次 Tab 聚焦视口外 `x=-232px` 的隐藏品牌链接，第二次 Tab 聚焦 `x=-53px` 的“关闭菜单”按钮；侧栏打开时焦点也可逃逸至遮罩后“登录”链接。该真实 A11y Bug 以当前 PI Agent 创建为 QUAL-20，不是静态审查猜测。
- 修复 `apps/web/src/app/layout.tsx`：关闭状态视觉不可见且退出焦点序列；移动菜单打开变为 `role=dialog`、`aria-modal=true`、背景 inert；焦点进入可见“关闭菜单”，Tab/Shift+Tab 显式在菜单控件内循环，Escape 返回触发按钮，导航链接返回主内容，移动→桌面自动关闭并聚焦主内容。代码与测试 Git `546376d`。
- 实际前端单测 `pnpm --filter web test:a11y` **3/3 PASS**；`make check-web`、`make check-go-required`（Go1.27.3）、`pnpm contracts:check`、Python 21/21 均 PASS。访客 390 关闭 axe18/0/0、项目390关闭 axe26/0/0、项目审核320 axe20/0/0；展开访客导航 axe21 passes / 0 violations / **1 incomplete（bypass landmark）**，不能称为完整自动无障碍通过。
- 项目 Web 页面在隔离模拟 Human Session 下从**真实 PI read-only API**提供工作项/项目队列事实，仅用以验证已实现项目菜单键盘路由，不等于真实 Human 登录态。截图 `62–65` 和 `docs/design/phase10-c-light-brand/qual20-keyboard-verification.md`；没有将 PI PAT 或虚构身份写入数据库。
- QUAL-20 技术修复需独立 Human Reviewer 合法签收，不自动 Done；真正 macOS VoiceOver、完整所有页面键盘流程、400% 该菜单真实 Human 回归继续待验收，`design-qa` **final result blocked**。

## 2026-11-01 本轮真实 Human 验收：QUAL-20 退回、QUAL-2/3 技术回读

- 真实登录的 `Qual Local Owner · Human` 有有效 `work:review`、`audit:read`，不同于原 PI Agent。QUAL-20 在 390px 实测关闭菜单首次 Tab 正确聚焦可见按钮、Escape/路由恢复正常，但展开后初始焦点有时仍为 BODY；更重要的是 `controls()` 会把关闭 `<details>` 下3个隐藏链接列为可见候选，实测 `Shift+Tab` 事件已 `preventDefault` 却不能从首项环绕到最后一个可见控件。真实 Human `reviewer:needs_work/self_review=false` 已在 v9 写入，随后合法退回 `in_progress v10`。证据 `docs/pilot/qual20-human-focus-regression-evidence.json` 与本轮截图66–68，旧纯单测3/3 PASS及旧TestRun Passed不能替代新真实失败。移动菜单展开时 axe 0 violations 但1项 bypass incomplete，未实测 VoiceOver。
- QUAL-2 的真实 PI 安全脚本本轮 **PASS**（401/403/404/409、被拒绝的Agent Done没有变更），Human浏览器实际重读 QUAL-TC-4 v2/TestRun Passed 与 Ready Artifact 2238 bytes/SHA一致；QUAL-3 的真实 PI 协作脚本本轮 **PASS**（以已完成 QUAL-19 为真实 Human/PI 闭环样本），Human浏览器核对 QUAL-TC-12 Passed 与 Ready Artifact 2399 bytes/SHA一致。
- **身份限制**：QUAL-2、QUAL-3 的 CreatedBy 都是当前 Human，若写Reviewer则为 `self_review=true`。本轮只由真实 Human 追加有author_id可验证的技术审查 Comment，不冒充独立审批、不变更为 Done；二者保持 `in_review v12/v9`，等待另一真实且有 `work:review` 的独立 Principal 审核。
- 工程门禁本轮 `make check-go-required`（项目局部Go1.27.3）、`make check-web`、`pnpm contracts:check`、`pnpm --filter web test:a11y` 3/3 PASS，不能覆盖真实菜单失败。默认QUAL项目当前 **20项：4 done、2 in_review、14 in_progress、0 todo**；全局 VoiceOver/完整键盘、正式多身份权限试点与生产发布门禁仍未完成。详细原始事实见 `docs/pilot/qual-human-review-q2-q3-q20-2026-11-01.md`。

## 2026-11-01 QUAL-20 PI 技术整改补录（仍待真实 Human 复审）

- 已针对真实 Human `reviewer:needs_work/self_review=false` 的两个复现问题完成源码修复 `2ce1f3b`：原生 `details:not([open])` 下隐藏链接退出焦点候选；移动弹层过渡动画后的条件聚焦补偿；原 `aside role=dialog` 的 axe minor 违规修正为合法 `div role=dialog|complementary`。这不推翻或修改原 Human 失败记录。
- 真实本机 Chrome **未登录访客** 390/320px 的初焦、Escape、桌面1440切换通过；针对项目 `<details>` 在实际浏览器里注入标记为 **DOM-only** 的测试夹具，修复前 Shift+Tab 真正不移动、修复后可以与最后可见 summary 环绕，展开后链接可聚焦。该夹具并非新的真实登录 Human 项目 QA。
- `QUAL-TC-11 v2` 外部 `TestRun fff13eec` 真正保存 Failed→Passed Attempt，早期 Failed 关联原 **QUAL-20**；Ready 附件有经授权的 SHA-256 比对，真实同 Principal Executor/Tester 两条追加履历。Web焦点5/5、Go1.27.3、Web lint/typecheck/build、OpenAPI 与 Python45/45 均通过；打开弹层 axe 27 passes/0 violations/**1 bypass incomplete**，不能称为读屏验收。
- **当前 QUAL-20 保持 `in_progress v12`**，最新 Human Reviewer 仍为 needs_work。等待真实 Human 320/390px 项目内容与 VoiceOver/400% 复测并独立作新决策，不将自动技术通过视为 Done；完整报告 `docs/pilot/qual20-focus-after-human-needs-work-2026-11-01.md`。
- 其他新增治理任务 QUAL-21 与 QUAL-2/3 Human Done 的最新模式争议需后续独立处理，旧审计按原时点保留，不反向重写历史。

## QUAL-22 Goose12 发布包独立 Human 审核与 QUAL-20 复验（2026-11-01）

- 本轮真实 Human 登录 `Qual Local Owner`、`work:review`/`audit:read` 授权有效；受托 Agent 执行/测试身份始终是不同 Principal。QUAL-22 的Go1.27.3构建/旧回滚二进制SHA、六份原始日志SHA、真实QUAL-TC-17 Passed Attempt及3125-byte Ready Artifact SHA均经实际重算比对。精确Go1.27.3、Web/契约、A11y单测5/5、Python52/52、dogfood-verify本轮PASS。独立 Human `reviewer:passed/self_review=false`（v8），合法完成 `done v9`；Audit 中Human实际写入和状态流转成立，**仅签收PI构建与交接包，不是18080部署/Goose13上线**。
- QUAL-20 在真正已登录 Human 320/390px项目页复测闭合侧栏首Tab、展开初焦稳定为关闭菜单、折叠details可见控件双向Tab环绕、展开details Tab到权限链接、Escape/路由后的main聚焦及320→1440；原生Chrome 400% DPR4/CSS360px弹层无整页横溢，焦点/aria-modal/inert正确，已恢复100%。axe 21通过/0违反/1 incomplete（bypass），没有真实VoiceOver。为保留 QUAL-23 发布后必须验证的再送审队列样本，只追加真实Human观察评论，QUAL-20 `in_review v15` 和旧Reviewer `needs_work` 不变。
- 端口18080仍监听旧服务PID90737，readyz200但强制 `--require-resubmission QUAL-20` 实际 exit1，表明审核队列新行为尚未上线。Human Operator 任务 QUAL-23 `todo v5` 无 assignee（仅新增有真实Human author_id的交接评论）；未执行进程停止/启动、数据库迁移、PAT操作或权限修改。QUAL-24 Goose13 仍todo，QUAL-21 Stage C未完成。PI目前正常28个MCP Tools，**不需重启PI；真正需更新的是Qual Go Server并由有权Operator在安全维护窗口执行**。
- 本轮全部事实、身份边界、截图72–74及发布依赖见 `docs/pilot/qual-human-review-goose12-release-2026-11-01.md`。全项目VoiceOver/多身份/生产运维门禁仍未完成。

## 2026-11-01 QUAL-23 实际受控 launchd 上线 / QUAL-20 独立 Human 完成

- 真正 Human Owner 在 QUAL-23 本人认领及观察记录后明确同意本机launchd改造；实际操作是受限Local PI经Mac受控终端执行，其Executor/Tester历史没有假冒Human。新受控launchd脚本/测试/回滚手册已提交Git `4633ef6`。旧临时Go进程90737已正常结束，新审核二进制首次PID31359、之后经真实`launchctl kickstart -k`重启为PID34401，`gui/501/local.qual.go-server`正在受控运行。
- 切换前经真实Go1.27.3 `qual-ops`创建**带签名且实际verify成功**的私有备份（34表16对象，DB备份467282字节），隔离`restore-drill`完全通过并清理临时数据。Goose schema12始终不变，Docker数据库/S3容器和PI都没有重启；保留旧二进制SHA及自动回滚路径，但**未故意测试真实旧版整链路回滚**。
- 18181候选、正式18080及真实launchd重启后，`healthz/readyz=200`，真实受限PI ReviewQueue 5项0失败，旧needs_work历史保留，QUAL-20重新送审awaiting/resubmitted=true，四项其余needs_work不变；S3 Ready Artifact字节SHA一致、匿名接口被拒绝，Human已登录会话跨切换与重启均200；`make dogfood-security` 401/403/404/409 与`make dogfood-verify` PASS。
- 新增公开测试规范`QUAL-TC-18 v1`、实际外部TestRun `bfd75fc5`、两条真正Passed Attempt对应首次切换与管理器重启、两份Ready JSON附件并重新下载比对SHA；由真实PI写入`executor:observed`与`tester:passed`，QUAL-23处于**`in_review v13`**，等待已认领的Human Owner正式自审签收，不把预先的`reviewer:observed/self_review=true`当审核通过。
- 实际后端更新后，QUAL-20的真实Human已追加独立`reviewer:passed/self_review=false`（v16），随后显式`done v17`，只覆盖移动导航已测焦点修复，不等于完整VoiceOver/WCAG通过。QUAL-24 Goose13仍todo、QUAL-21 Stage C未完成。
- 现有24项状态：**8 done / 1 in_review / 14 in_progress / 1 todo**。本轮报告及源证据见`docs/pilot/qual23-live-launchd-goose12-2026-11-01.md`。**全产品Design QA仍blocked**。

## QUAL-23 Human Owner 本人最终完成核验（2026-11-01 19:48 +08:00）

- 用户报告已亲自点击完成。经受限 PI 对真实 Qual WorkItem 和 Project Activity **只读核对**：QUAL-23 从 `in_review v13` 进入 **`done v14`**；真实 `work_item.transition` Actor 是 `Qual Local Owner · Human`（操作时间 `2026-11-01T19:48:37.177944+08:00`），不由 PI 代替执行。
- 不可变角色历史仍是 Human `reviewer:observed/self_review=true`（v8）以及 PI Executor/Tester（v10/v11），**没有新增 reviewer:passed**。只表述为 Owner 本人有权完成，不伪称独立批准或事后补造审核记录。
- launchd Go服务仍由 PID34401 提供 `readyz=200`，Goose schema仍12；QUAL-24 Goose13未部署，QUAL-21 Stage C及全局 VoiceOver/正式安全门禁均独立保留。
- 最新24项：**9 done / 0 in_review / 14 in_progress / 1 todo**。本次回执见 `docs/pilot/qual23-human-owner-completion-2026-11-01.md`。因此**全局 Design QA 继续 blocked**。

## 2026-11-01 真正默认Goose13上线：QUAL-21阶段C完成、QUAL-24迁移及新审核防绕过

- 本轮用户要求完成而非只审计。真实Human一次性登录、`/auth/session` 和 `/access/me` 返回200，并通过有权浏览器对QUAL-21写独立 `reviewer:passed/self_review=false`、完成 `done v16`。实际Human Owner随后Claim QUAL-24、写明维护范围，再由真实Local PI Agent在授权Mac UID执行OS/PG/测试，身份和权限分离。
- **默认真实Qual数据库已从Goose12迁移为Goose13**，不是临时测试库。新固定SHA的Go1.27.3 Server由launchd管理，真正kickstart后PID78031、Ready200、Goose版本13。新版Policy、AcceptanceHistory（旧QUAL-2显示`legacy_unclassified`）、有效政策及WorkItem五个端点全部HTTP200。默认安全 `independent_review`，Owner签收默认未开放且高风险继续需独立审核。
- 首次实际迁移已成功Up，但迁移Operator临时验证缺少`tempfile`导致失败，**真正执行安全Goose13 Down回退旧Goose12**，在无治理签收记录时才允许；修复Git`8b90070`后第二次正式迁移Up成功，旧WorkItem/RoleEntry/Audit/Attempt/Artifact业务指纹未变。真实PG+S3当次私有签名备份及`verify`/`restore-drill`均PASS，旧版发布文件和客户端回退文件私有保存。**有Goose13治理历史后不可再随意Down**。
- 原子替换本地Qual CLI/MCP候选，真正CLI`review-policy`显示independent、`acceptance`显示QUAL-2 legacy_unclassified；独立MCP stdio真实`tools/list`返回34项。**已运行的PI需要用户自行`/reload`或重启PI让连接更新**，无需重启Go或Docker。
- 真实`QUAL-TC-20 v1`链接QUAL-24，外部TestRun `5e474be6`有不可变首次Failed Attempt与之后Passed Attempt、真正Ready JSON Artifact `2f07b269`（3137字节/SHA`f5a7942c...`），已授权下载字节相符；PI写入Executor/Tester，提交 `QUAL-24 in_review v12`。
- 真实Human Goose13新Done Gate负面检查：虽然提交了真实TestRun UUID，因没有符合策略的独立Reviewer `passed`，服务端拒绝显式签收，QUAL-24仍`in_review v12`、`acceptance_mode=not_completed`、`decision=null`；不能通过Owner身份或历史测试passed越权完成。旧迁移与本次`QUAL-24`尚未获得独立业务结案。
- Human审阅的新`#review-acceptance`语义区域已用`role=region`修复，真实桌面axe29PASS/0违规/0未完成、390px和Chrome原生400%axe28PASS/0/0，无页面级横溢；Chrome缩放恢复100%。**没有真正macOS VoiceOver或全部人群的端到端权限/生产安全签收**，总体Design QA继续`blocked`。
- 完整证据及指令、安全后果与截图见 `docs/pilot/qual24-default-goose13-live-upgrade-2026-11-01.md`；受检代码 `4fc964a`、`8b90070`、`cc1e4cc`，候选源 `939f35f`。Goose13已真实运行，QUAL-24仍需不同且具`work:review`的合法Reviewer作最终签收。

## QUAL-24：PI Reload 后真实 Human 权限、证据与独立性复核（2026-11-01）

- `pi mcp list` 实测当前正在运行的 PI `qual-local connected 34 tools`；三项新增政策和签收只读 MCP 实际调用并成功读取默认 Goose13 `independent_review`、`owner_acceptance_allowed=false`、QUAL-24 `acceptance_mode=not_completed`。Goose13 launchd PID78031、ready=200；`make dogfood-verify`再次PASS。
- 真正 Human 浏览器再次读回 QUAL-TC-20 TestRun的不可变 Failed#1→Passed#2，已授权下载 Ready Artifact 3137字节/散列匹配；由 Human 身份仅追加一次观察评论（ID `832227c9-e7ea-49de-b738-637ff3739504`），QUAL-24目前 `in_review v13`，最新无 Reviewer passed，Acceptance仍not_completed。没有修改项目权限、政策或工作状态。
- 真实项目仅两名Principal：执行/测试 PI 与该任务 Human Owner。两者分别因CreatedBy/Executor或Assignee会被标记为`self_review=true`，不能形成默认独立模式所需`self_review=false`。不能在一般代办授权下偷偷启用全项目Owner自审策略；应由另一个真实有权Reviewer复核，或者由Human单独、明确批准Owner非独立风险接受例外。
- 当前代码的`high_risk`自动判定仅包括 urgent Task 或 critical/blocker Bug；QUAL-24是 high Task，虽然是数据库正式迁移，代码返回high_risk=false。不能把这点误解为本业务风险低。详细核对见 `docs/pilot/qual24-post-reload-human-audit-2026-11-01.md`。全局Design QA仍blocked。

## 2026-11-01 Agent 自主签收简化落地 / QUAL-24 Owner 明确签收

- 用户正式调整项目方向为**Agent 自主决策、Human 保留操作/撤权**，不另建多级审批系统。复用 Goose13 `owner_acceptance`，领域 `IsAssignedOwnerDelegate` 只在没有独立AccountableOwner时认真实Assignee；仍双重检查 Credential `work:review`+Project授权、Review策略、CAS、真实TestRun/Ready证据及明确非独立风险说明。隔离Goose13真实PostgreSQL `-race` Agent同Principal自签PASS，旧低权PAT403、非负责人422、版本冲突409；完整证据见 `docs/pilot/qual25-agent-autonomy-2026-11-01.md`。Go/Web/契约和Python71项检查已通过。
- 真实Go1.27.3默认Server已于Goose13下安全替换到launchd PID4112（候选SHA `a7807449...`）；ReviewQueue5项、S3哈希及匿名拒绝PASS，旧版回退路径私有保存；Goose13保持不变。实现代码 `f5921a2`、部署Pin `e2631aa`。
- Human Workspace Admin已在真实浏览器审计地将项目策略改为 `owner_acceptance v1`（`owner_acceptance_allowed=true`、`force_independent_for_high_risk=true`），原Local PI Agent的 `work:review` Project Allow v1已提交；当前旧Credential Scope仍不含work:review，需要Human一次性可信终端签发同Principal新凭证，不能宣称已能用旧Token自主签收。
- 用户此前已明确授权 QUAL-24 Owner 自审例外。真实Human会话通过新签收表单提交有证据与风险说明的 `owner_acceptance`，服务器记录 `independent=false`、Actor为真实 Human、版本v13→v14，**QUAL-24 done v14**。不是独立Reviewer passed，也不包括生产TLS/HA/OIDC、完整VoiceOver等全产品门禁。

## 2026-11-02：默认 Qual 真正 Agent 自主签收完成 / MCP 新凭证刷新核验

- 用户在可信本机终端签发并安全更新同 Principal 的新 PAT，然后对 PI 执行 `/reload`。本轮**直接调用当前 PI 会话**的 `qual-local` MCP（34 个工具），实际 `project_access_self` 返回原 Local PI Agent `credential_scopes` 与 `effective_permissions` 均包含 `work:review`，仍无 `access:manage`；`work_acceptance_get` 与 `work_item_get` 真实回读成功，不只是重启的新 MCP 进程自检。
- QUAL-25 由真正 Local PI Agent 自主签收，`done v8`：`owner_acceptance`、`independent=false`、`explicit_self_review=true`、真实 Actor/同项 QUAL-TC-21 TestRun/项目策略 v1，工作版本 v7→v8。真实 Postgres 只读验证不可变 Decision/Audit/Activity/Outbox 各**恰好 1 条**，未使用 Human 会话或伪造独立 Reviewer。
- 本机正式 Goose13 `launchd` Go Server 仍正常（PID4112、Ready200），未再迁移/重启数据库或扩权。25项当前为 **12 done / 13 in_progress / 0 in_review**。最新回执为 `docs/pilot/qual25-real-agent-autonomous-acceptance-2026-11-02.md`；之前“旧 PI PAT 不具 work:review”的内容属于当时历史，不再代表当前权限。完整 VoiceOver、生产 TLS/HA/OIDC 等独立质量门禁仍 **blocked**。

**final result: blocked**
