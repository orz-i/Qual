# PR #1 · 已投递事件的热目录安全归档与崩溃恢复

- 日期：2026-11-04
- 所属：QUAL-36，仍处于 In Progress，需 QUAL-37 后续独立验收
- 适用范围：外部 GitHub Adapter、本机受控 loopback、合成签名与一次性 Goose17/PostgreSQL/S3。
- 目标：解除 10,000 条热 Delivered 回执构成的永久拒收风险，同时绝不销毁原 GitHub Delivery ID、原始已签名 Body SHA-256、Qual Event ID 及重试历史。
- 非目标：给 Qual 增加 Agent 执行器、自动业务动作；访问生产数据库；替代正式 GitHub App/Webhook/TLS；声称 Human 独立批准；提供无限磁盘或自动 GC。

## 可信模型

完成一次授权的 ExternalEvent 之后，适配器可收到 GitHub 重新投递或攻击者复用旧 Body/HMAC 并更换未签名的 Delivery ID。持久化去重必须覆盖热目录与任何归档历史。删除 JSON 或简单保留 30 天的滚动索引均会让过期重放重新生成 Qual 自然键，故拒绝以这种方式“清理容量”。

每次已完成回执在原 delivered/delivery-id.json 中包含脱敏来源信息、固定 Project/WorkItem/Thread 目标、原始 Body SHA-256、实际 Qual Event ID 和重试/人工原因轨迹。归档时：

1. 在 worker 与 queue 双锁下只筛选已完成记录，按收到时间和 Delivery ID 稳定排序，每次最多 500 条；Pending、Blocked、Manual Review、Conflict 记录不能归档。
2. 先写入 archive-index/payload-sha256.json，与原完整 Delivered 回执字段完全一致，0600 文件和 0700 父目录，执行文件与目录 fsync；直到这步确认成功，热目录内容绝不移动。
3. 同一文件系统原子 rename(delivered/id, archived/id)，再次 fsync(archived) 和 fsync(delivered)。已存在的 Archived ID 不允许被覆盖。
4. 每次 OpenSpool 在受锁保护下先处理旧 Pending/Delivered 双副本，再核对归档 SHA 索引和完整回执；中断于写入索引后、移动前的状态会继续完成移动。索引缺失、权限错误、符号链接、反向索引不一致或已有不同 Qual Event ID 均拒绝启动。过往成功记录绝不能因此变成 Pending。
5. 新 Webhook 在验证 HMAC 后，先按三个目录检查相同 Delivery ID，再按热记录与持久的 archive-index 检查 Body SHA-256。即使修改未签名 Delivery ID，不能令已归档原 Body 再次创建 Queue/Qual Event。

此方案不声称与不同 macOS UID 进行 OS 隔离；同一 UID 可以绕过所有应用层文件协议。对该威胁必须采用不同 OS 用户或可信隔离宿主，以及不可由进程操作者更改的审计/备份机制。

## 本机 Operator 使用边界

只在受保护的 OS 用户下，设置经授权的仓库数字 ID、全名和 owner-only spool 路径后：

    qual-github-adapter inspect
    qual-github-adapter archive 200 --approve
    qual-github-adapter inspect

inspect.capacity.delivered_used 是热 Delivered 数，archived_used 是不可删除的累计历史数，near_limit 对热队列 80% 提前预警。archive 返回本批移动数、热剩余数、累计已归档数。不需要 GitHub Webhook Secret、Qual Integration Bearer 或 Human 管理员 PAT，不会向 Qual 服务端发任何业务写入；它也不是新的 Qual 服务端调度器或 Agent 编排。

**磁盘约束**：Archived 与 archive-index 都存完整脱敏回执，并且无业务自动过期。归档可以恢复 10,000 个热槽位，却不能释放总磁盘空间，甚至会额外消耗索引存储。必须在启用长期无人值守投递前规划：可信的整体备份（包含两个 archive 目录）、外部磁盘监控与扩容、完整恢复演练、Operator 外部定时调用及独立告警。绝不直接删除 archive/index、清理失败历史或在默认 Qual 库上跑合成验收。

## 安全测试覆盖

- 新单元回归：归档前后容量统计；根据完整原 Body SHA-256 和 Delivery UUID 的跨目录去重；同 UUID 内容变更冲突；老 Event ID 保持；不能归档 Pending；命令必须显式 --approve 且限定 1..500。
- 故障注入：索引已 fsync 但尚未移动 Delivered；已移 Archive 但旧 Pending 尚在；双崩溃窗口重叠；删除索引、篡改索引、改成 world-readable 或符号链接，启动 fail closed。以上均只在隔离临时目录执行。
- 集成回归：TestQual36ExternalAdapterSignedSyntheticPRToRealGoose17PI 使用真实外部 Adapter 子进程、合成签名、隔离 Goose17 DB/S3。成功记录 Event 后，通过另一个受控 Adapter archive 进程完成归档，带新随机 Delivery ID 的同签名 Body 无法再次写入。仍保持同一原 Event/Audit 数量，独立的后续合成授权失败 401 等保持原判定。
- 不虚构的证据：固定本地源码 SHA 与测试运行后提交记录、GitHub 远程同 SHA CI、已登记 QUAL-36 回执；没有独立 GitHub Reviewer、人类 Operator 参与测试或者真实 GitHub Delivery 的真实性证据。

## 未完成门禁

长期磁盘上限与备份隔离、真实 GitHub App → TLS Ingress → 原封 Webhook → 专用 Integration → 授权映射 → PI 的运营闭环，以及 QUAL-37 独立技术/人员审查仍 Pending。PR #1 必须保持 Draft、不能将局部热目录容量恢复当作完整生产上限治理或无人值守就绪。
