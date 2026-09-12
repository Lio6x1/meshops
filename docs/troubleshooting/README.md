# 本地排错知识库

这里记录 MeshOps 实际遇到的问题：先看什么现象、怎样缩小范围、为何出错、怎样修复，以及什么证据足以确认修复。范围是六类模拟实体、四类 inspect 执行方、单一权威来源的本地课程工程；不把本地事务或模拟 effect_count 解释成真实设备的执行保证。

## 先按症状找入口

| 现象 | 阅读顺序 |
| --- | --- |
| 命令执行不了、Unauthenticated、PermissionDenied、Unavailable | [连接与身份诊断](01-connection-auth.md) → [机器 token 的摘要边界](12-machine-token.md) |
| 十个重试请求只有一个成功，其余报 Unavailable | [RetryDLQ 的 InnoDB 死锁](02-retry-dlq-deadlock.md) |
| 执行方没收到命令，任务却能 ACK | [pending 投递不能授权回报](03-report-dispatch-intent.md) |
| 已终态的投递又变成 dispatched 或多出 attempt | [陈旧 worker 覆盖新镜像](04-stale-dispatch-worker.md) |
| 列表总数与本页行数矛盾 | [单次响应的读快照](05-list-snapshot.md) |
| inspect 大小写别名被接受、错误文本特别长 | [JSON 精确字段与有界错误](06-inspect-parser.md) |
| executor 越跑越慢、重启后任务消失或反复失败 | [inbox 索引与原始事实](07-inbox-recovery.md) |
| 网关达到 count 后退出 0，但上传已失败 | [上传错误被取消覆盖](08-upload-shutdown.md) |
| Redis 丢失后返回空状态、Kafka 截断后“恢复成功” | [投影世代与严格重放](09-projection-recovery.md) |
| 历史清理写了 LIMIT 500 仍慢 | [用实际 EXPLAIN 判断清理范围](10-history-explain.md) |
| 本机能复制，Git 新检出指纹失败；CI 绿但场景被跳过 | [Git 原始字节与 CI 证据](11-git-ci.md) |

初学时按 01→02→03→04→05→06→07→08→09→10→11→12 阅读。02、04、05 放在一起比较：行锁保护写入顺序，版本用于识别旧事实，一致性快照让多次读取观察同一时点；三者解决的问题不同。

## 命令和证据口径

命令默认在**自己的工程根目录**执行。`go test -run` 是当前修复的回归入口；要观察原始失败，应在隔离的旧版本副本运行对应测试，不要为了复现而在正在演示的数据库中改状态。带真实 MySQL 的任务测试会创建并清理随机测试库；先确认 `MESHOPS_TEST_MYSQL_DSN` 指向课程测试服务且账号允许创建测试库。不要给它生产连接。

```powershell
go version
git status --short
if (-not $env:MESHOPS_TEST_MYSQL_DSN) { throw '先在本机加载测试配置；不要把 DSN 粘贴到报告' }
```

Redis/Kafka/ES 测试同理，需要相应测试环境变量。缺少依赖时有的测试跳过，有的显式失败；只有明确的测试名出现 PASS，才能计入该场景。截断 Kafka、故障 Redis、强杀或重建属于隔离故障演练，不能把整库清空、删卷或重置消费组当作日常排错第一步。

本知识库引用两组证据：一是[已有发布/死锁记录](../learning/from-zero/verification/2026-09-12-git-publication.md)及链接的历史验证；二是 2026-09-12 本轮审查的**定向**红绿测试和执行计划。后者不是一次完整最终验收，也不能代替某个远端提交的 Actions 结果。临时审查备忘录保存在本机 `.cache/review/`；仓库仅归档经过检查的 [SQL/EXPLAIN 证据](evidence/README.md)，不复制可能含凭证的原始运行日志。

日志建议保留时间、服务、错误码、任务/事件/attempt 标识、状态版本、消费组/世代/topic/partition/offset、重试阶段和持续时间。不要保留 authorization、token、完整 DSN、`.local/secrets.json`、未脱敏业务 payload 或带敏感 SQL 的整段数据库诊断。
