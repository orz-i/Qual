# 本地开发依赖

仅用于本机开发。Phase 5 Go 服务已经通过官方 AWS SDK v2 连接私有 SeaweedFS S3，并通过 pgx 连接 PostgreSQL；附件与项目归档测试已经真实执行。容器只是本机测试配置，不能公网部署。

## 启动

```bash
docker compose -f deploy/compose.dev.yaml up -d --wait
docker compose -f deploy/compose.dev.yaml ps
# postgres: postgresql://qual:qual_dev_only@127.0.0.1:15432/qual?sslmode=disable
# S3: http://127.0.0.1:18333 (bucket: qual-artifacts)
```

仅映射 localhost 端口，不应在公网开放。凭证是**固定的本地开发示例**，不得用于正式环境。可设置 `QUAL_DB_PASSWORD`、`QUAL_DB_PORT`、`QUAL_S3_ACCESS_KEY`、`QUAL_S3_SECRET_KEY` 等环境变量覆盖。设置 DB 密码后也须同步更新 `DATABASE_URL`。

PostgreSQL 18 容器的数据卷挂载到 `/var/lib/postgresql`，而不是旧版的 `/var/lib/postgresql/data`。S3 开发服务使用 SeaweedFS 4.51 的单节点 `weed mini` 默认行为；对象存储适配通过 S3 API，生产部署可替换服务实现。

检查 S3 访问（演示凭证，仅适用默认本地配置）：

```bash
curl --fail --aws-sigv4 'aws:amz:us-east-1:s3' \
  --user 'qualdev:qual_dev_only' \
  'http://127.0.0.1:18333/qual-artifacts?list-type=2'
```

未签名访问应被拒绝。已实测签名 ListBucket（200）及 PUT/GET/DELETE 往返，测试对象已删除。

## 迁移

安装 Go Goose CLI（建议固定版本，与 CI 保持一致）：

```bash
go install github.com/pressly/goose/v3/cmd/goose@v3.28.0
make migrate-up
make migrate-status
make migrate-down
```

`DATABASE_URL` 可从 shell 环境传递给 `make`。Phase 5 当前有 10 条迁移：身份、工作项、Session、测试、S3 Artifact/ExternalReference/ArchiveExport、Webhook/Delivery 及 Pending/Uploaded 清理日志。请在独立测试数据库验证 Up/Down/Up；**勿对生产数据执行删除式迁移测试**。

## Phase 5 服务配置

Go 服务启动需要 `DATABASE_URL`、`QUAL_S3_ENDPOINT=http://127.0.0.1:18333`、
`QUAL_S3_ACCESS_KEY`、`QUAL_S3_SECRET_KEY`、`QUAL_S3_BUCKET=qual-artifacts`；
不启用 S3 时附件/导出 API 返回依赖不可用，不会偷偷存到未授权磁盘。

Webhook 仅在预先配置随机 AES-256-GCM 主密钥和显式 HTTPS host allowlist 后接受订阅：

```bash
# 由管理员本地安全环境生成，不写入 Git/.env.example
openssl rand -hex 32
# 通过安全密钥管理器向服务端及独立维护进程注入
# QUAL_WEBHOOK_ENCRYPTION_KEY=<随机64位十六进制>
# QUAL_WEBHOOK_ALLOWED_HOSTS=hooks.example.com
```

管理员明确运行 `qual-maintenance deliver-once` 或 `deliver-loop` 投递 Outbox，或 `cleanup-artifacts -hours 24` 清理过期上传。**不能让 Agent 执行该命令，也不能把数据库/S3/签名主密钥下发到 Agent 宿主**。当前不具备公网身份、生产密钥轮换及网络 egress 防火墙控制；Webhook 生产部署前必须经过安全评审。

## Phase 6 运行监控与灾备

Go 1.27.3+（已修复的标准库版本）是当前服务的最低安全工具链。进程的 `GET /healthz` 为存活；`GET /readyz` 检查 PostgreSQL + schema version 12、已配置 S3 的 Bucket，可用 200，故障 503。只有配置独立 `QUAL_METRICS_TOKEN`（随机至少 32 字符）才注册 Operator-only `GET /internal/metrics`，普通 Agent PAT 不能读取。

```bash
(cd apps/server && go test -race ./... && go vet ./... && govulncheck ./...)
pnpm audit --prod --audit-level=moderate
# 仅测试独立随机库与随机临时 S3 桶，不会恢复至现有 Qual 数据库
GOOSE=/path/to/goose make recovery-drill
```

备份用 `cmd/qual-ops` 创建包含 PostgreSQL 18 Custom Dump、私有 S3 对象及逐表指纹的 HMAC 签名目录，必须在**没有业务写入的维护窗口**运行；恢复演练只允许全新随机 scratch DB/Bucket。没有热备、生产级 RPO/RTO、项目 ZIP 跨安装导入，也没有自动创建/执行 Agent。详见 [运行手册](../docs/runbooks/phase6-operations.md) 和 [本机验收记录](../docs/engineering/phase6-verification.md)。

## 停止

```bash
make dev-down
```

`down` 不删除持久卷；如需丢弃本地数据，需另行明确执行并确认。

## 参照

- https://hub.docker.com/_/postgres （PG18 卷路径变化）
- https://github.com/seaweedfs/seaweedfs （S3 mini quick start）
- https://github.com/pressly/goose
