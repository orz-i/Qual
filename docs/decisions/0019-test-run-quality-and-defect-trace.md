# ADR-0019 · TestRun 最新结果与失败缺陷追溯（Phase 10）

- 状态：Accepted for Phase 10 local Qual pilot
- 日期：2026-11-01（Mac 本地）
- 关联：QUAL-15 / Phase 10 C Light Brand

## 问题与约束

现有 TestRun/RunItem 只展示最新 status，而 `attempt_defects` 在 PostgreSQL 内保存了失败 Attempt→Bug 事实但没有读取端点。UI 因而无法在历史记录中看见过去失败所关联的 Bug。用户希望以真实通过/失败/跳过/阻塞/未上报为中心，而不是先查看技术 SHA/UUID。

## 决策

- TestRun 列表与详情以**当前已加载 TestRun 的每个 RunItem 最新结果**为基础计算 `passed/failed/blocked/skipped/unreported`；不把未上报视为跳过，不把后来 Passed 抹除前序失败。
- 提供保留全部状态的过滤选项，显式标注是“最新上报状态”、数量基于“当前 Run 的所有已返回用例”；不将“通过数”包装成业务级质量审查或 CI 执行成功。
- 为已存在的失败 Attempt 增加 `GET /api/v1/projects/{projectID}/test-attempts/{attemptID}/defects`，基于已有关系表返回该 Attempt 在同 Project 内关联的 Bug 编号、标题、当前状态、关联时间和真实操作者 UUID。读取须持有有效 `test:read`，不存在或跨项目 Attempt 返回 404。不得泄露权限或凭证。
- 新 GET 不写入任何记录、不修改 Attempt，保留已有 POST 的鉴权与不可变行为。前端只有在展开“关联缺陷”时才读取，不进行大规模 N+1 自动请求。
- 写入结果、导入 JUnit 和关联 Bug 的按钮仅在相应项目实际权限下展示；失败链接的创建需要 `work:write` + `test:read`（以及非 Integration 身份），与后端规则一致。
- SHA-256/CaseVersion/Attempt 来源仍可展开核验，默认视图优先状态、用例标识和失败定位。

## 验证

- Go Domain/Application + HTTP/PG 跨项目、只读/拒绝、失败关联、历史未改写的隔离集成测试。
- TypeScript 纯函数单元测试处理 Passed/Failed/Blocked/Skipped/Null、状态筛选及后续成功不覆盖历史。
- React 浏览器实际请求有效资格的测试运行，同时用明确标识的隔离夹具模拟存在失败/缺陷链路；不得让模拟数据污染真实 Qual。
- 真实 Human/Agent 端到端验收另依 QUAL-9/QUAL-17（原有 review needs_work 不自动撤销）。
