# COUNT=1，本页却没有任务

[返回目录](README.md) · 下一篇：[JSON 参数边界](06-inspect-parser.md)

## 为什么时间上界没有解决问题

[ListTasks](../../internal/tasks/queries.go)用创建时间上界和稳定游标分页，但原 COUNT 与 SELECT 是两个自动提交读取。查询 pending 任务时，COUNT 读到 1，随后并发 ACK 改变状态，本页 SELECT 就可能读到 0。创建时间没变，状态过滤条件却已经变化。

本轮用只在测试中存在的 SQL driver 包装器，在 COUNT 结果关闭后提交真实 MySQL 状态更新，再运行页查询，红测试得到 `COUNT/page mixed two committed snapshots: total_count:1`。包装器最初未覆盖驱动的 prepared-statement 回退，修正为明确走参数插值并断言屏障被执行后才确认红结果；不能把未真正制造并发窗口的绿测试算证明。

## 修复、命令和证据

单次 RPC 的 COUNT 与本页 SELECT 现在共享显式只读 REPEATABLE READ 事务，读取完关闭 rows 后提交。游标仍固定创建上界，不保持跨 RPC 的长事务。

```powershell
go test -tags=integration ./internal/tasks -run 'TestListTasksCountAndPageShareSnapshot|TestA17ListStableTimestampCursorAndTenantBinding' -count=1 -v
```

两项在本轮 129.732s 定向任务组中通过。count 表示上界内满足过滤条件的总数，不是当前页大小；后续页面到来前状态仍可能改变，因此不承诺多页原子历史视图。GetTaskHistory 没有 COUNT/page 这一对读取，不能把本缺陷误报到该接口。

保留过滤条件、页大小、是否有游标、响应 total_count 和行数即可；游标是有签名的请求状态，不要尝试手改它来绕过租户或过滤绑定。
