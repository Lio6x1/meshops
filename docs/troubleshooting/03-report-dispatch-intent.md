# pending 记录不能授权执行回报

[返回目录](README.md) · 下一篇：[陈旧投递 worker](04-stale-dispatch-worker.md)

## 现象、根因和复现

任务只有一个 `pending` attempt，执行方还没进入发送流程，却能引用可预测的 `<task>-execute-1` 回报 ACK；取消 attempt 同样可能被提前回报 CANCELLED。原 [ReportTaskStatus](../../internal/tasks/service.go)核对了 tenant、task、executor、execution_key 和命令类型，但没检查发送意图是否曾落盘。[GetDispatch](../../internal/tasks/dispatcher.go)返回 pending 行本身是正常查询行为，不能把查询成功等同于执行授权。

本轮红测试对 executor ACK、dispatcher DISPATCHED、CANCELLED 三种情况都得到 `never-dispatched report accepted: <nil>`。当前回归：

```powershell
go test -tags=integration ./internal/tasks -run 'TestReportRequiresDurableDispatchIntent|TestReportHistoricalDispatchAndDuplicateOutage' -count=1 -v
```

## 修复规则

新回报在绑定检查之后要求合法非空 `dispatched_at`。这个字段由 dispatcher 在 stream send **之前**持久化，表示可恢复的发送意图；不证明对端已收到字节，更不证明真实设备动作完成。

不能简单要求 `delivery_status == dispatched`：迟到 ACK 或结果可能引用已变成 timeout/DLQ 的旧 attempt，历史发送时间仍有效。已接受的 event_id/hash 重试继续先返回 DUPLICATE，不能因为 dispatcher 暂时不可用就让成功重试失败。

原有效回报测试仅调用 `persistCreated` 生成 pending 行，也意外依赖了漏洞；修复测试设置时改为运行真实 dispatcher 的意图落盘路径，再发送有效回报。不能为了让旧测试绿而放宽新检查。

## 证据、排查与限制

本轮定向 MySQL 回归已验证：拒绝时任务和 receipt 不变；落盘后同一回报可接受；历史 timeout/DLQ 可回报；已接受 receipt 在远端查询故障时仍能重试。上述测试包含在 129.732s 的定向任务组中。

排查时一起查询 Task 的业务 status/status_version 和 attempt 的 delivery_status/dispatched_at/command_kind。二者可以不同；不要拿 TaskStatus 当投递状态。取消仅表示请求被接受，是否停止看执行事实。审计 reason 中的原幂等键重复留存也已改为共享 SHA-256 关联摘要，两个创建事件仍有各自 event_id；Task 自身和旧审计仍可能保留原键，这不是加密或全量擦除。
