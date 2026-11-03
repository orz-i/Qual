# Qual Phase 6：本机试点运行与灾备手册

> **当前级别**：仅 loopback 的开发/试点候选；不是已获安全批准的公网服务。Qual 不运行 Agent、CI、测试或模型。参考 [ADR-0013](../decisions/0013-phase6-reliability-backups.md)。

## 1. 运行与监控

服务继续通过 `apps/server/cmd/server` 启动。必须配置本机 `DATABASE_URL`；只有设置 S3 Endpoint 时，Ready 才会包含 S3 Bucket 检测。

| API | 作用 | 预期 |
| --- | --- | --- |
| `GET /healthz` | 进程 Liveness | 运行中的 HTTP 进程 200，数据库宕机时也可以为 200 |
| `GET /readyz` | 就绪检查 | PostgreSQL Ping、当前真实 Goose schema version 12；仅阶段 C 候选代码在 Human 授权后支持13，正式迁移由 QUAL-24 单独执行、启用 S3 时的 HeadBucket；就绪 200，否则 503 |
| `GET /internal/metrics` | 运维 Prometheus 0.0.4 文本 | **默认不存在（404）**；设置独立 `QUAL_METRICS_TOKEN` ≥32 字符才注册，必须使用该密钥的 Bearer；普通 PAT/Session 不可读取 |

服务端仅记录请求 UUID、**路由模板**、HTTP 方法、状态码、耗时；不得把原始资源 Path、Query、Body、Session Cookie、PAT、S3 凭据写入日志。Metrics 低基数：`qual_http_requests_total{route,method,status_class}` 和 `qual_http_request_duration_seconds` 直方图（秒）。真正的远端日志/Prometheus/告警基础设施未安装。

本机查看 metrics（不要在聊天、截图、shell 历史中显示真实 Token）：

```bash
# 由运维密钥管理系统注入，与 Qual PAT 完全独立
export QUAL_METRICS_TOKEN='<至少32字符的随机运维密钥>'
# 与 server 使用完全相同的密钥
curl --fail --silent \
  --header "Authorization: Bearer $QUAL_METRICS_TOKEN" \
  'http://127.0.0.1:18080/internal/metrics'
```

失败排查：若 `/readyz` 为 503，先检查 PostgreSQL 容器健康、`make migrate-status` 的当前 schema 版本及 S3 Bucket 权限/网络；**不能**通过把它改成始终返回 200 来消除错误，也不要向浏览器返回数据库/S3 密钥。服务进程被终止则以 `/healthz` 无法连接识别。

## 2. 备份边界与正确流程

**必须由运维 Human 执行，不能把 DATABASE_URL、S3 key、签名密钥交给外部 Agent 宿主。** 一致性条件是单机维护窗口：先暂停业务写入、附件上传/完成、Webhook Worker、显式维护清理及任何直连 S3 的写操作。因为 PostgreSQL 与 S3 没有分布式快照，工具在 PG 的 `pg_export_snapshot`/pg_dump `--snapshot` 之外，仍需对 S3 原始 bytes 逐个确认 SHA-256。发生任何不一致就拒绝备份，不生成静默不完整结果。

`qual-ops` 使用宿主上的 Docker Compose `postgres` 容器运行 PG18 原生 `pg_dump -Fc`，不是覆盖 SQL dump；备份到**不存在**的新目录（0700），所有文件 0600。备份内容包括 PostgreSQL（含 Principal 哈希及 Webhook 加密密文）和所有 Ready/Uploaded 附件及归档对象。Manifest 为 HMAC-SHA256 签名，有逐表内容指纹、各 S3 对象 SHA-256/大小和整个 `database.dump` 的 SHA-256。

```bash
cd apps/server
go build -o ../../bin/qual-ops ./cmd/qual-ops
cd ../..
umask 077

# 这些配置仅在受控运维进程中提供。不要复制生产凭证到示例代码。
export DATABASE_URL='<loopback PostgreSQL 连接串>'
export QUAL_S3_ENDPOINT='<私有 S3 Endpoint>'
export QUAL_S3_ACCESS_KEY='<受控备份读取及隔离临时桶创建/删除权限的运维密钥>'
export QUAL_S3_SECRET_KEY='<秘密管理系统中的值>'
export QUAL_S3_BUCKET='qual-artifacts'
# 在秘密管理器中生成并长期保管随机 32 字节十六进制值；
# 签名密钥丢失就无法验证该批备份。HMAC 不加密备份内容。
export QUAL_BACKUP_HMAC_KEY='<64位十六进制密钥>'

# 输出目录不能已经存在；名称示例，实际按时间与存储策略生成
./bin/qual-ops backup -out /private/qual-backups/qual-snapshot-20261031
./bin/qual-ops verify -path /private/qual-backups/qual-snapshot-20261031

# 只在随机新建的 scratch PG + S3 Bucket 恢复并验证，结束后自动清除
./bin/qual-ops restore-drill -path /private/qual-backups/qual-snapshot-20261031
```

关键限制：本机保守上限为 `pg_dump 256 MiB`、全部 S3 bytes 合计 `512 MiB`、对象不多于 5000、单对象不超过 32 MiB、单表最多 100000 行，超限应升级备份方案而不是临时删掉数据。Manifest HMAC 保证校验身份与完整性，**不代表文件被加密**；离线/异地存储前必须单独加密、设置访问权限与保留周期。定期验证恢复，不要只检查是否创建了文件。

**绝不**把 `restore-drill` 当生产恢复命令：它只生成 `qual_drill_*` 随机新库和临时 S3 桶，核对每张表的完整 JSON 指纹和每个对象的字节/散列，随后清理。当前未提供覆盖现有 DB/桶的恢复命令；完整生产恢复 SOP、切换路由、恢复密钥和跨区域 RPO/RTO 仍需 Phase 6 真正试点评审。

## 3. 每次提交的独立演练

```bash
# 已有 Docker/Compose、Go、Goose、SeaweedFS、PostgreSQL；默认演练端口 18081 必须空闲
GOOSE="$(command -v goose)" make recovery-drill
# 18081 已被占用时，只换演练服务端口；检测到端口占用会在创建临时库前拒绝执行
QUAL_DRILL_HTTP_PORT=18085 GOOSE=/path/to/goose make recovery-drill
```

脚本会创建随机 `qual_pilot_*` 源库及 DR 项目，登记 WorkItem、上传 S3 Artifact、生成 ZIP，执行备份/HMAC 验证、隔离 PG/S3 恢复、全表指纹及对象字节比对、错误密钥拒绝验证，最后清理恢复 scratch 和脚本产生的临时 S3/DB 测试资源。**它不接触默认 `qual` 数据库**。任何失败直接退出且不把一次失败当作通过。CLI 实际操作全由本地运维显式启动；用户 Agent 不能触发演练。

## 4. Webhook/Outbox 事故与安全

Webhook 只发送历史事实，独立 `qual-maintenance deliver-once|deliver-loop` 才投递。需要 `QUAL_WEBHOOK_ENCRYPTION_KEY`、`QUAL_WEBHOOK_ALLOWED_HOSTS`，未配置时不要尝试在浏览器开启外网投递。

- `retrying`：记录 HTTP/网络失败摘要，检查允许主机是否仍可信；优先排查对方幂等行为。不能直接将 Delivery 标记成功。
- `dead`：五次失败停止自动重试；由 Human 运维评审、修复回调及后续补偿流程。直接改 SQL 重置重试并非正式恢复 API。
- 目的地 DNS/IP 与证书校验必须由服务端完成；生产部署另外配置 **网络 egress 防火墙**，阻止 Worker 访问元数据/内网服务。
- 在途请求可能在管理员禁用订阅后才返回；外部消费者按 `event_id`/`delivery_id` 幂等，不能假定恰好一次。

## 5. 安全扫描与本机性能基线

```bash
cd apps/server
go version   # 需要 1.27.3 或后续已修复版本
go mod verify
go vet ./...
go test -race ./...
govulncheck ./...
QUAL_TEST_DATABASE_URL='<已迁移的独立测试库>' \
 QUAL_S3_ENDPOINT=http://127.0.0.1:18333 \
 QUAL_S3_ACCESS_KEY=qualdev QUAL_S3_SECRET_KEY=qual_dev_only \
 go test -race -tags=integration -count=1 -v -run 'TestPhase6' ./integration
cd ../..
pnpm audit --prod --audit-level=moderate
pnpm contracts:check && pnpm lint && pnpm typecheck && pnpm build
```

本机 Mac Mini + PostgreSQL/httptest 测试环境下，400 次已鉴权 WorkItem GET、16 并发的**首次采样**得到 p50≈2.8 ms、p95≈9.1 ms、p99≈26.6 ms、约 3909 req/s（带 Go race detector）。性能仅用来监控回归，**没有被作为任何公网可用性或吞吐承诺**。

Go 漏洞扫描修复了工具链 1.27.1→1.27.3 和 `golang.org/x/text` 旧版的可达漏洞；升级后 `govulncheck` 对应用可达路径报告 0，仍有一项**未调用的模块告警**。前端把 shadcn 组件生成器改为开发依赖，生产依赖 `pnpm audit --prod` 为 0 已知漏洞；全依赖扫描中组件生成器的 `deepmerge`/旧 `minimatch` 上游告警需后续关注。不能声称供应链完全无风险。

## 6. 操作后记录

每次发布/演练在 [Phase 6 验收记录](../engineering/phase6-verification.md) 中填写准确时间、Git Commit、Go/DB/S3 版本、预期/实际、回滚操作和责任人；使用单独工单跟踪失败。**不得**把无人参与的自动化测试报告成真实团队试点反馈。
