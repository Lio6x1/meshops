# 07｜inbox 越跑越慢，以及重启时如何保留事实

[返回导航](README.md) · 上一篇：[JSON 边界](06-inspect-parser.md) · 下一篇：[上传退出](08-upload-shutdown.md)

## 现象与复现

旧 executor 每 50ms 的 `PendingKeys` 都反序列化整个 inbox，接收新任务检查容量时又扫描一次。已完成任务越多，轮询成本越高，即使当前没有待执行任务。完成记录本身是重放去重依据，不能靠删除它们解决扫描问题。

本轮用隔离临时 bbolt 文件验证索引迁移、事务回滚、取消和重开后的重复命令；基准只改变已完成历史量：

```powershell
go test ./internal/edge -run 'TestInboxDerivedIndexesMigrateAndRollback|TestQueueVersionBoundarySurvivesReopen|TestInboxReopenRejectsUnschedulableFactsWithoutChangingFile' -count=1 -v
go test ./internal/edge -run '^$' -bench '^BenchmarkInboxPendingWithCompletedHistory$' -benchmem -benchtime=200ms -count=1
```

这些测试使用临时文件。不要把正在运行的 queue/inbox 文件交给修改工具，也不要手工删除 bucket 来“修复”真实文件。

## 原因与修复

原始 inbox、reports、results 是事实；新增的 `inbox_pending`、`inbox_active` 只是可重建索引。pending 包含尚未 Done 的记录；active 进一步排除取消、拒绝或已有 outcome 的记录。所有成员变化与原始记录写入在**同一个 bbolt 事务**中提交。启动时先验证原始事实，再原子重建两个索引，支持旧文件缺少索引以及索引陈旧的情况。

热路径现在按 pending 数量工作；容量判断最多读取配置容量数量的 active key，本项目容量上限为四。完成 tombstone、结果和不确定回报身份继续保留。启动重建与最终 Stats 仍随历史量增长，磁盘保留策略没有改变；没有可靠的最大重放期限，就不能随意给去重事实加 TTL。

## 另一个真实问题：坏事实不能被索引迁移掩盖

独立审查发现两种旧文件仍能成功打开：

- `Done=true`，却还有指向未确认 report 的 `PendingID`。重建索引排除 Done 后，报告仍在文件里，却永远不会被调度。
- 留存的可执行 EXECUTE 的 inspect duration 为 0。旧接收路径曾允许它持久化；重启后又调度，随后执行失败并停止共享运行时。

修复是在任何索引写入之前拒绝不可能的事实组合、验证留存可执行 payload，失败时保持文件字节不变。合法的“先收到取消、后收到执行”不能按普通可运行 EXECUTE 强行校验。这里的 fail closed 是**拒绝继续调度并保留证据**，不是自动删除报告、修改 Done 或重新执行。

发现此类错误时先停止对应 executor，保留原文件和只读备份，记录文件路径、错误类别、任务/attempt 标识；修复配置或从已验证备份恢复后再启动。不要输出原始 payload。逻辑验证能发现已知损坏组合，不等于文件具备密码学防篡改能力。

## 输入错误与存储错误要分开

外来 executor 命令、绑定冲突、无效 envelope/inspect 参数现在在持久化前被拒绝，当前流继续处理后续合法命令，并记录有界诊断。bbolt 写失败、持久化事实损坏仍使运行时停止，不能统统 `continue`。对应回归：

```powershell
go test ./internal/edge -run 'TestExecutorIgnoresInvalidCommandsAndContinuesSameStream|TestExecutorStorageFailureRemainsFatal' -count=1 -v
```

## 实际证据与范围

迁移回归在修复前报 `old inbox did not acquire derived indexes`；输入隔离回归报 `received command for another executor` 并停止无关工作。修复后本轮 edge 定向组通过（5.250s），排除 ForceKill 的包测试通过（20.707s）。独立 reopen 两种坏事实回归通过（3.035s），断言打开失败且文件字节不变。

同一次 200ms 基准中，历史 10 条为 484.9ns/op、504B/op，历史 10,000 条为 543.8ns/op、528B/op。这支持“轮询不再解码全部历史”，不能推算生产吞吐或启动时间。Windows 当时因 `CGO_ENABLED=0` 无法启动 race，不能把这些结果写成 race 通过。

实现与回归：[inbox.go](../../internal/edge/inbox.go)、[review_regression_test.go](../../internal/edge/review_regression_test.go)、[reopen_review_test.go](../../internal/edge/reopen_review_test.go)。本地 `effect_count=1` 只证明模拟结果事务，不证明外部设备副作用恰好一次。
