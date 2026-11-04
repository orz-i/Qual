# QUAL-50 / GitHub Adapter：本机磁盘水位安全门禁（候选）

范围：独立外部 GitHub Adapter，与 PR #1 的受保护 Loopback/GitHub HMAC/Qual 独立 Integration 凭据边界一致。此变更没有操作正式 Qual 数据库、Human 备份密钥、正式 GitHub Webhook 或服务。

## 背景与结果

- 旧 hot queue 只统计 Pending 1,000 与 Delivered 10,000 条；即使安全移动了已投递回执到 Archived/ArchiveIndex，宿主文件系统空间仍会耗尽。不能靠删除索引恢复空间，因为同原始已签名 Body 使用新的、未被 HMAC 签名的 Delivery ID 会重新进入 Qual。
- 新增底层 statfs（本 OS 用户真实可用空间 Bavail），对文件系统容量做保守探测；计算使用溢出保护，零容量或异常统计拒绝继续。
- inspect.capacity 在原先队列指标之外附加 disk_total_bytes、disk_available_bytes、disk_reserve_bytes、disk_near_limit、disk_ingress_blocked。
- 磁盘可用空间低于 **64 MiB** 时，任何新受信任的 spool JSON 写入在实际磁盘操作前 fail closed；已成功落盘的同 Body/Delivery ID 完全一致重放，仍可返回既存 202/new=false。文件写入、文件 fsync、目录 fsync 的成功检查不变，即使探针刚通过而写入中出现 ENOSPC，也不能返回虚假的“已持久接收”。
- 物理磁盘使用达到 80% 或可用空间不高于 **256 MiB** 时 disk_near_limit=true，同时影响总体 near_limit。队列满载或磁盘进入硬保留区，ingress_blocked=true。这一门禁不等于 OS 配额、隔离用户或真实生产总容量治理。
- 对 Spool 的测试专属底层接口注入合成低磁盘统计：未认证分支不修改宿主磁盘；检验 503、没有新增 JSON、旧合法重放、统计错误拒绝、80% 告警、64 MiB 边界与真实本机临时目录的 statfs 读回。

## 上线约束

- 64 MiB 是针对小型持久队列原子写入与 fsync 的保守预留，不适用于独立 PG/S3 备份或大型 ZIP。磁盘进入预警水位后仍须由外部 Operator 检查备份、ArchiveIndex/Archived 增长与磁盘扩容；没有后端自动 Agent 或业务调度。
- 如果 statfs 不可用、目录校验失败、实际写入/同步失败，则保留未完成事实，不回填成功 Event，不自动删除旧记录。
- 本阶段未实现独立签名归档备份与隔离恢复，也未获取任何 Human-only HMAC Key。长期磁盘总量、离线可信备份、独立审核、真实 GitHub App → TLS 入站 → 受权 Integration 与 QUAL-37 完整 E2E 仍 Pending。
