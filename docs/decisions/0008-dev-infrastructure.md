# ADR-0008：本地开发使用 PostgreSQL 18、Goose 和 SeaweedFS S3

- **状态**：Accepted（Phase 1 工程基础已验证）
- **日期**：2026-10-30

## Context

Qual 需要关系事务、可执行版本迁移，以及与对象存储解耦的 S3 兼容开发环境。此前文档倾向 MinIO，但其原开源仓库已归档，不宜作为新项目的默认维护依赖。运行中不需要微服务消息总线或 Agent 执行器。

## Decision

1. 使用 `postgres:18-alpine` 进行本地关系存储，数据卷为 `/var/lib/postgresql`（与 PG18 镜像约定一致）。默认只在 localhost:15432 发布端口。
2. 数据库迁移采用 `pressly/goose/v3@v3.28.0`，首条迁移只创建 `qual` schema；未来真实领域表与约束另行评审。
3. 使用 `chrislusf/seaweedfs:4.51` 的单节点 S3 mini 服务作为开发对象存储，端口 localhost:18333，默认仅本地示例凭证，后端通过标准 S3 接口抽象，不绑定 SeaweedFS 专属 API。
4. 不把 Docker 本地开发拓扑作为生产部署方案；实际生产数据库密钥、TLS、备份、安全策略与 S3 服务商需要独立决策。

## Verification

本地 `docker compose up -d --wait`：PostgreSQL 健康、S3 可用。Goose `up → down → up` 成功，当前 schema 版本 1。S3 签名 ListBucket 成功（HTTP 200），未签名 403；签名 PUT/GET/DELETE 实际往返验证通过。以上不等于生产级容灾与兼容性评估。

## Consequences

可快速在开发机复现基础数据环境，但需要持续升级镜像与依赖，且必须在 Phase 5 对所需的 S3 操作（权限、临时链接、checksum、multipart）做互通测试。默认凭证只能用于本地。PostgreSQL 的数据库迁移引入不可逆数据操作前必须补足回滚或前向修复策略。

## Alternatives

- MinIO：原开源仓库归档，缺少继续作为默认镜像的积极维护依据；通过 S3 抽象仍允许兼容部署。
- SQLite：不利于初期以 PostgreSQL 事务、一致性和并发策略为主的数据库设计。
- 云托管对象存储：适合正式部署，但开发人员的离线/自部署环境不应依赖云凭证。

## References

- https://github.com/minio/minio
- https://github.com/seaweedfs/seaweedfs
- https://hub.docker.com/_/postgres
- https://github.com/pressly/goose
