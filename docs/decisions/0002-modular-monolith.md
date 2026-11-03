# ADR-0002：Go DDD 模块化单体与 Monorepo

- **状态**：Accepted（Phase 1 骨架已按该设计创建，领域模块仍待 Phase 2）
- **日期**：2026-10-30

## Context
任务、测试、权限、审计间存在跨域一致性要求；初期团队与运维预算有限。用户指定 Go + TS React/Vite/React Router/Tailwind/shadcn、DDD、Monorepo。

## Decision
采用 Go 单服务、单 Go module 起步，DDD 限界上下文划分业务模块；PostgreSQL 是权威关系存储，S3 兼容对象存储保存附件。前端 Vite SPA；pnpm workspace 仅管理 TS/JS。`/api/v1` 和 `/mcp` 将共享应用层，不因协议拆微服务。Go module 在无远端仓库地址时暂名 `qual`，后续改为规范托管路径。

## Consequences
低部署成本、事务一致性简单；需通过模块边界测试和代码审查防止内部调用耦合。未来确有独立伸缩/团队边界时，先提取 Adapter 与契约，再评估拆服务。

## Alternatives
微服务过早引入分布式一致性与运维成本；全层次通用 CRUD 缺乏领域边界；Go 多 module 不利初期协作。
