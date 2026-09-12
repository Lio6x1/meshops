# RetryDLQ：九个 Unavailable 背后是 InnoDB 间隙锁循环

[返回目录](README.md) · 前一篇：[连接诊断](01-connection-auth.md) · 下一篇：[回报授权](03-report-dispatch-intent.md)

## 现象与复现

第二次远端 CI 的 `TestA23DurableDLQAndConcurrentManualRetry` 同时提交十个人工重试请求，九个返回 Unavailable。Windows 原测试重复十次均通过，Linux 重复十次才出现一次，因此“本机难复现”不能推翻并发缺陷。

测试后来在 GetTask 后增加屏障：十个请求都读到相同任务事实后，才一起争抢最新 DLQ 行。修复前加强测试连续三次失败。当前回归入口：

```powershell
go test -tags=integration ./internal/tasks -run '^TestA23DurableDLQAndConcurrentManualRetry$' -count=3 -v -timeout=2m
```

使用隔离 MySQL；测试自己构造 DLQ、并发请求和断言，不要手工重置正式 task_dispatches。完整历史证据及提交背景见[Git 发布记录的第二次运行](../learning/from-zero/verification/2026-09-12-git-publication.md)。

## 根因：不是重复键，也不只是“锁等得久”

原事务在默认 REPEATABLE READ 下查最新 attempt 并 `FOR UPDATE`，之后插入下一轮。InnoDB 诊断捕获了两条互相等待的边：一个事务持有最新投递行的 PRIMARY 记录锁，等 supremum 间隙上的插入意向锁；另一个持有相关间隙锁，等前者的记录锁。范围扫描产生的 next-key/gap 锁与后续 append 形成循环。

死锁受害事务被数据库回滚，再被服务映射成 Unavailable，所以仅查网络会找错方向。`FOR UPDATE` 并不天然排除死锁；锁涉及哪些记录/间隙、获取顺序及插入位置都重要。

需要本地诊断时可用已配置的 MySQL login-path，避免命令行密码：

```powershell
mysql --login-path=meshops-local --execute "SELECT @@transaction_isolation; SHOW ENGINE INNODB STATUS;"
```

`SHOW ENGINE INNODB STATUS` 可能含当前 SQL 和业务值，应留在本机，分享前只摘取脱敏的事务等待边。它记录最近死锁，需与失败时间对应，不能把旧诊断套到新故障。

## 修复与不可扩大之处

[RetryDLQ 实现](../../internal/tasks/dispatcher.go)只把**这一个事务**设为 READ COMMITTED，继续保留记录锁、唯一 dispatch ID、唯一 attempt 和重复键处理。并发请求仍只能新增一个轮次，其余正常返回 accepted=false。

没有改数据库全局隔离级别，没有增加进程互斥锁，也没有跨服务锁 Task 表。不要把 READ COMMITTED 当通用死锁开关：其他事务若锁顺序相反仍可能死锁。后续审查对“先锁某个 attempt，再镜像所有 attempts”的潜在锁序风险只记录为待单独证明的观察，没有伪称已复现或全部修复。

与此不同，[CancelTask](../../internal/tasks/service.go)的行锁设计有效：读取当前任务后，只设置一次 cancel_requested，重复请求保留首次 reason；回报与超时使用同一任务行锁。没有 expected_version 不等于缺陷，也无需修改公开协议。

## 实际证据

历史修复后：加强测试 Windows 连续五次、Linux 开启 race 连续十次通过；每次断言十个请求正常返回、恰好一个 Accepted、一个新轮次。本轮定向任务回归再次包含该测试并通过，整组耗时 129.732s；这是该组结果，不是此单测的耗时或项目最终完整验收。

历史任务整包曾因未传 Kafka 地址导致强杀测试失败，补齐后单独通过；原失败记录保留。不要把分项补验改写成第一次整包成功。建议记录 task_id、dispatch_id、attempt、retry_round、事务阶段、MySQL 错误号及持续时间，不记录完整连接字符串。
