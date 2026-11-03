# ADR-0004：API-first 与 MCP/CLI Adapter

- **状态**：Accepted（Phase 2 REST + Phase 3 CLI 和本机 MCP stdio 已实测；远程 MCP OAuth 尚未实施）
- **日期**：2026-10-30

## Context
同一任务由人通过 Web 和 Agent 通过 MCP/CLI 修改时，出现业务规则分叉的风险。外部 Agent 宿主迭代快，需要低耦合的接口。

## Decision
OpenAPI 是 REST 主契约；React SDK 与 CLI 使用 REST，MCP Tools 通过 REST 或可信进程内接口映射共享 Go Application Service；**外部 Agent 宿主只有 scoped Token/HTTP API 地址，无数据库管理员凭证**。MCP 按 2026-07-28 规范开发并基于官方 Go SDK 做版本互通测试；人类和 Agent 的认证在各 Transport 转换为同一 ActorContext。CLI、Skill/Plugin 不嵌入执行引擎。

## Consequences
契约、错误代码、并发和授权必须做交叉接口一致性测试；可为 MCP 设计稳定的上下文只读工具，减少 Agent 上下文成本。支持旧 MCP 版本需要专门测试，不盲目复制旧 session/GET SSE 行为。

## Alternatives
让 MCP 以 HTTP 客户端调用本机 REST 会增加网络跳转和错误处理层；为 Agent 创建单独任务业务后端会导致行为分叉。
