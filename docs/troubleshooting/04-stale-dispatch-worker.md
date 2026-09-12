# 陈旧 worker 把终态镜像改回 dispatched

[返回目录](README.md) · 下一篇：[列表读快照](05-list-snapshot.md)

## 实际失败窗口

[dispatchOne](../../internal/tasks/dispatch_worker.go)先领取租约并提交，再调用 GetTask，最后开启第二个事务。原第二事务只重读 lease_owner，继续使用第一次读取的 attempt 和网络返回的 Task。

GetTask 已取得旧 Task、尚未返回时，Kafka 可以把同一 attempt 镜像推进到 TIMED_OUT。原 `mirrorTask` 虽然会忽略旧版本，后面的无条件 UPDATE/插入仍使用旧分支：本轮红测试观察到 pending attempt 被改回 dispatched；另一路已发送 attempt 在终态之后新增了 `execute-2 / pending / DISPATCH_PENDING`。

租约只阻止另一发送 worker 领取，不阻止 Kafka 更新业务镜像。把远端 RPC 搬进事务持锁也不是合适修复，会扩大网络故障对数据库的阻塞。

## 修复与回归

第二事务持有记录锁后重新读取完整 attempt；如果当前状态已关闭，或镜像版本高于本次获取的 Task，释放租约并结束本次处理，不发送、不标超时、不新建 attempt。仍可投递时才使用当前记录继续。

```powershell
go test -tags=integration ./internal/tasks -run 'TestDispatchWorkerPreservesNewerTerminalMirror|TestDispatchWorkerDefersNewerCancellationMirror' -count=1 -v
```

测试屏障卡在 GetTask **已经读取**之后，期间提交超时并消费终态事件，再放行旧结果。另一个取消测试保留 delivery_status=pending，只推进镜像版本；它证明仅检查关闭状态还不够。

## 实际证据与边界

pending/dispatched 两种红测试均明确失败；修复后它们在定向任务回归组通过。取消版本测试单独通过，14.046s。查询和发送间仍可能发生新的取消，这是跨服务流程的正常窗口，由执行方取消墓碑和 Task 终态规则收敛；本次修复的是**已在本地提交的新事实被旧 worker 覆盖**。

日志可记录 task_id、dispatch_id、lease_owner 的关联标识、读取的 Task 版本和当前镜像版本；不要为了复现直接改正在运行的业务表或长时间锁住它。
