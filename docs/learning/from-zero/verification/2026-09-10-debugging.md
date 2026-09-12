# 日志与排错课验证

本轮只补教学资料及查询型实验脚本，未改变业务日志实现、协议或数据结构。使用Astra Medium、单人执行，没有新增子agent。

课程入口：[日志与排错](../lessons/debugging.md)。完整脚本：[diagnose.ps1](../exercises/diagnose.ps1)。

## 真实操作结果

在既有reference本地环境执行start.ps1，未启动模拟器；载入该目录env后执行诊断脚本，结束由finally调用stop.ps1。使用实际opctl可执行程序与运行中的Entity、Dispatcher，经TCP/gRPC调用；不是替身返回固定错误。

| 操作 | 退出码 | 实际结果 |
| --- | ---: | --- |
| 初始正确snapshot查询 | 0 | 正常JSON |
| snapshot缺少entity | 2 | --entity required；客户端拒绝，未发RPC |
| 足够长度的错误凭证 | 1 | Unauthenticated / invalid credential |
| 操作员调用管理端status | 1 | PermissionDenied / role cannot call method |
| 编号BAD ID! | 1 | InvalidArgument / invalid entity ID |
| 恢复正确snapshot请求 | 0 | 正常JSON |
| 管理员查询status | 0 | 正常JSON |

七项全部符合预期，脚本逐项比较退出码、错误码和必要文本。结果：[result.json](debugging-20260910/result.json)，四个实际诊断输出也保存在同目录。凭证没有写入报告；无效实验变量在finally恢复。

日志检查确认Entity启动记录通过slog写入entity.err.log，含INFO、role、rpc及metrics字段。这个检查不代表所有错误都自动记录服务端日志；讲义明确区分客户端错误响应和服务端日志。

## 回归示例

使用Go1.25.10与既有共享构建缓存，执行：

```powershell
go test ./internal/state ./internal/edge -run '^(TestWholeBatchAndPrefixACK|TestInboxEffectAndReportSurviveRestart)$' -count=1 -v
```

两项指定测试均实际被发现并PASS。前者验证批次预校验和连续ACK边界；后者使用真实临时bbolt文件验证结果与待报信息在关闭重开后保持幂等。这是对讲义回归命令的验证，不是新增完整故障或性能验收。

脚本PowerShell语法解析通过。服务结束后 `.local/processes.json` 为 `[]`，没有留下本次启动的课程进程；既有依赖容器保持原状态，没有删卷、清队列或创建任务。

## 限制

本轮未重新运行任务创建演示及所有任务停滞场景，讲义中的Task/Outbox/分发排查表是基于当前实现的诊断指导。完整真实任务与故障证据仍以已有参考工程验收记录为准。未新增统一JSON日志、全链路trace ID、轮转或集中采集能力。
