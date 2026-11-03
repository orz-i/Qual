# QUAL-23：macOS launchd 安全部署手册（Goose12 历史运行手册）

> **当前已升级 Goose13（2026-11-01）**：默认业务库 `qual` 真实Schema13，新版本 launchd Go PID78031，Go CLI/MCP均已更新，真实ReviewPolicy/AcceptanceHistory功能在线。**禁止再用本手册中针对Goose12固定二进制的 `scripts/ops/qual_launchd_server.py cutover/rollback` 命令管理当前Goose13**。新的受控迁移操作代码为 `scripts/ops/qual_goose13_rollout.py`；Goose13存在真实审核治理数据后的Down按DB约束拒绝，必须由Operator做前向兼容修复或另行审批的整体恢复。最新结果见 [实际 Goose13 上线报告](../pilot/qual24-default-goose13-live-upgrade-2026-11-01.md)。下面Goose12描述属于历史部署前时点，不再代表当前默认版本。

> **状态更新（2026-11-01）**：本机已实际部署并按本手册验证。launchd服务`local.qual.go-server`在`127.0.0.1:18080`运行并通过实际kickstart重启，当前PID34401；旧二进制、私有签名备份/隔离恢复证明及自动回滚路径保留，**没有故意执行一次完整live旧版回滚**。Goose12保持不变；QUAL-23 后续由真正 Human Owner done v14，详见[实际发布/回执](../pilot/qual23-live-launchd-goose12-2026-11-01.md)。后文命令仍可用于后续安全维护。

**重要：该运行脚本固定旧 Goose12 二进制 SHA 与 schema12，且不允许覆盖已经注册的 launchd Job，严禁直接拿其 cutover/rollback 命令迁移 Goose13。** QUAL-21 Stage C/QUAL-24 的新候选/备份/前向兼容回滚与 Human Operator 条件见 [独立 Goose13 发布交接](../pilot/qual21-24-goose13-operator-release-handoff-2026-11-01.md)。

**范围**：仅 QUAL 本机 loopback 开发试点。QUAL-22 仅批准构建/回滚交接，不代表正在运行的服务已部署；QUAL-24 Goose13 是完全独立的迁移任务。

## 当前发布包

- 旧 Go 程序：`tmp/phase10/qual19-server-go1273`，SHA-256 `bd4ead46c0f5ce51300ff32e7ad3495cb5f8f5a3cf951a36f995a7cce0c5a595`。
- 新 Go1.27.3 程序：`tmp/phase10/qual22-release/qual-server-go1273`，SHA-256 `975daa0424da15b885d0b35b4bcb79c8d5edf15de1331d42f798b424eec279c9`。
- 真正的服务管理目标：`gui/501/local.qual.go-server`，监听 `127.0.0.1:18080`，**不启动 Docker 容器、也不执行 Goose13**。
- Docker Compose `qual-dev` 只运行 PostgreSQL 和 SeaweedFS；默认业务数据库 Goose12。
- Human Operator 已在 QUAL-23 以本人身份认领并留同意记录；PI Agent 本身没有 Human 认证或服务审批身份。

## 安全文件位置

| 路径 | 作用 |
| --- | --- |
| `scripts/ops/qual_launchd_server.py` | 审核过 SHA 的启动/预演/切换/回滚脚本，Python3.9 标准库 |
| `~/Library/LaunchAgents/local.qual.go-server.plist` | 不含凭据的 launchd Job，文件0600 |
| `~/.config/qual/qual-server-launchd.json` | **含本机 dev 凭据**的0600私有运行配置，不属于 Git |
| `~/Library/Logs/Qual/` | 受保护0600服务日志 |
| `~/.local/state/qual/backups/` | 私有数据库/S3备份及校验材料 |
| `~/.config/qual/qual-backup-hmac.key` | 备份签名密钥，仅本机Human运维保管，不上传 |
| `~/.local/state/qual/qual23-live-cutover.json` | 真实部署前后进程、SHA、Goose12、健康与队列结果 |

**秘密边界**：本次本地 dev 配置由源码仓库中 versioned Compose 的开发默认值建立，使用时先验证真实 PostgreSQL/S3 连接。不读取老进程环境，也不在 plist、Git 或聊天写任何密码。开发默认值不能用于公网生产；本机 Agent 与 Human 同 UID，不能称为 OS 级隔离。

## 分阶段操作

在仓库根目录执行：

```bash
# 1) 仅准备 plist 和私有配置，不启动服务
/usr/bin/python3 -B scripts/ops/qual_launchd_server.py prepare

# 2) 候选在空闲 18181 启动，并用真正受限 PI token 只读验收
/usr/bin/python3 -B scripts/ops/qual_launchd_server.py preflight

# 3) 有合法 Human 维护窗口、已签名并验证备份及隔离恢复演练之后
# 才允许优雅停止旧18080、bootstrap launchd 新服务并自动验收
/usr/bin/python3 -B scripts/ops/qual_launchd_server.py cutover

# 4) 非破坏性查询
/usr/bin/python3 -B scripts/ops/qual_launchd_server.py status

# 5) 新受管服务出现故障时，由有权Operator执行旧二进制回滚
/usr/bin/python3 -B scripts/ops/qual_launchd_server.py rollback
```

切换的强制门禁：真实 schema 12、旧/新二进制 SHA 匹配、六小时内 Signed Backup 验证与隔离 Restore Drill、18181 Ready 200、匿名权限拒绝、真实旧 Reviewer 履历未变、真实 PI 只读 ReviewQueue smoke PASS、S3 Ready Artifact 3125字节/SHA验证。任一前置失败不得停止旧服务；上线后任一验证失败，先卸载 launchd Job，再尝试旧二进制自动回滚，不使用 `kill -9`。

必须记录实际旧/新 PID、部署时刻、Ready 状态和不含密钥的证据。Goose12 不应迁移或清空。上线验证后，`QUAL-20 in_review` 的真实 ReviewQueue 应从旧版 `needs_work` 变成 `awaiting` 且 `resubmitted_after_review=true`，但其旧 Reviewer needs_work 历史仍应存在；QUAL-4/8/13/14 的真实 needs_work 不得消失。只有这项部署检查通过之后，才能决定 QUAL-20 的最终独立审核。

**不得混淆**：Go 服务的 launchd 切换不等于重启 PI；QUAL-24 的 Goose13 Done Gate 必须等待 QUAL-21 Stage C Web/CLI/MCP适配并另行审批。
